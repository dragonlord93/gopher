package shard

import (
	"context"
	"fmt"
	"hash/fnv"
	"log"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"scheduler/internal/config"
)

// Router maps tenant IDs to the correct Postgres connection pool.
// It caches one pool per unique physical host — so if pg-0 holds shards 0,10,20
// they all share the same pool, avoiding redundant connections.
//
// The routing is purely in-memory after initialisation:
//   hash(tenant_id) % num_shards  →  shard_id
//   shard_id  →  pg host  (from shardMap)
//   pg host   →  *pgxpool.Pool  (from pools cache)
type Router struct {
	mu       sync.RWMutex
	shardMap *config.ShardMap
	pools    map[string]*pgxpool.Pool // host → pool
}

func NewRouter(sm *config.ShardMap) *Router {
	return &Router{
		shardMap: sm,
		pools:    make(map[string]*pgxpool.Pool),
	}
}

// Init opens connection pools for all unique hosts in the shard map.
// Called once at startup. Each unique host gets exactly one pool.
func (r *Router) Init(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	seen := make(map[string]bool)
	for _, info := range r.shardMap.Shards {
		if seen[info.Host] {
			continue // already opened a pool for this host
		}
		seen[info.Host] = true

		pool, err := openPool(ctx, info.Host, info.Database)
		if err != nil {
			return fmt.Errorf("open pool for %s: %w", info.Host, err)
		}
		r.pools[info.Host] = pool
		log.Printf("[router] opened pool → %s/%s", info.Host, info.Database)
	}
	return nil
}

// PoolForTenant returns the pgxpool for the given tenant.
// This is the hot path — called on every job registration.
// Zero network I/O: just a hash + two map lookups.
func (r *Router) PoolForTenant(tenantID string) (*pgxpool.Pool, int, error) {
	shardID := r.ShardID(tenantID)
	return r.PoolForShard(shardID)
}

// PoolForShard returns the pool that owns a specific shard ID.
// Used by the scheduler during DB refill (it already knows its shard IDs).
func (r *Router) PoolForShard(shardID int) (*pgxpool.Pool, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, ok := r.shardMap.Shards[shardID]
	if !ok {
		return nil, shardID, fmt.Errorf("unknown shard_id %d", shardID)
	}

	pool, ok := r.pools[info.Host]
	if !ok {
		return nil, shardID, fmt.Errorf("no pool for host %s (shard %d)", info.Host, shardID)
	}

	return pool, shardID, nil
}

// ShardID computes which logical shard a tenant belongs to.
// Deterministic: hash("acme") % 100 is always the same number.
// Uses FNV-1a — fast, non-cryptographic, good distribution.
func (r *Router) ShardID(tenantID string) int {
	h := fnv.New32a()
	h.Write([]byte(tenantID))
	return int(h.Sum32()) % r.shardMap.NumShards
}

// UpdateShardMap is called when etcd notifies us of a shard map change
// (e.g. a new DB instance was added). Opens pools for any new hosts.
func (r *Router) UpdateShardMap(ctx context.Context, newMap *config.ShardMap) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, info := range newMap.Shards {
		if _, exists := r.pools[info.Host]; !exists {
			pool, err := openPool(ctx, info.Host, info.Database)
			if err != nil {
				return fmt.Errorf("open new pool for %s: %w", info.Host, err)
			}
			r.pools[info.Host] = pool
			log.Printf("[router] opened new pool → %s (shard map update)", info.Host)
		}
	}

	r.shardMap = newMap
	return nil
}

func (r *Router) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for host, pool := range r.pools {
		pool.Close()
		log.Printf("[router] closed pool → %s", host)
	}
}

func openPool(ctx context.Context, host, database string) (*pgxpool.Pool, error) {
	dsn := fmt.Sprintf("postgres://scheduler:password@%s/%s?pool_max_conns=20&pool_min_conns=5",
		host, database)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	// Verify connectivity
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping %s: %w", host, err)
	}
	return pool, nil
}
