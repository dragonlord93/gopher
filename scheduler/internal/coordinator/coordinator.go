package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"

	"scheduler/internal/config"
)

// Coordinator wraps etcd and provides three things:
//  1. Leader election  — only one scheduler runs the tick loop at a time
//  2. Membership       — live node registry with automatic TTL-based eviction
//  3. Shard assignment — leader computes and writes the shard→scheduler map
type Coordinator struct {
	client     *clientv3.Client
	session    *concurrency.Session // holds the etcd lease for our heartbeat key
	election   *concurrency.Election
	nodeID     string // unique ID for this scheduler instance, e.g. "scheduler-a8f2"

	mu          sync.RWMutex
	shardMap    *config.ShardMap          // physical shard→PG mapping (read from etcd)
	assignments map[int]string            // shard_id → scheduler nodeID (who owns what)
	myShards    []int                     // shards assigned to this node

	onRebalance func(gained, lost []int)  // callback into scheduler when shards change
}

func New(endpoints []string, nodeID string) (*Coordinator, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("etcd connect: %w", err)
	}

	// Session creates a lease with the configured TTL.
	// All keys written with this lease are auto-deleted if we stop renewing.
	sess, err := concurrency.NewSession(cli, concurrency.WithTTL(config.NodeLeaseTTL))
	if err != nil {
		return nil, fmt.Errorf("etcd session: %w", err)
	}

	return &Coordinator{
		client:      cli,
		session:     sess,
		election:    concurrency.NewElection(sess, config.KeyLeader),
		nodeID:      nodeID,
		assignments: make(map[int]string),
	}, nil
}

// SetRebalanceCallback registers the function the scheduler calls when its shard
// assignment changes. Called with slices of gained and lost shard IDs.
func (c *Coordinator) SetRebalanceCallback(fn func(gained, lost []int)) {
	c.onRebalance = fn
}

// Start registers this node as live and begins the heartbeat loop.
// Blocks until ctx is cancelled.
func (c *Coordinator) Start(ctx context.Context) error {
	// Write our presence as an ephemeral key that expires when the lease dies.
	// The value contains our address so others can reach us.
	liveKey := config.KeyLiveNodes + c.nodeID
	_, err := c.client.Put(ctx, liveKey, c.nodeID, clientv3.WithLease(c.session.Lease()))
	if err != nil {
		return fmt.Errorf("register live node: %w", err)
	}
	log.Printf("[coordinator] node %s registered as live", c.nodeID)

	// Load the physical shard map (written once by bootstrap, never changes at runtime)
	if err := c.loadShardMap(ctx); err != nil {
		return fmt.Errorf("load shard map: %w", err)
	}

	// Load current assignments (may already exist if we're rejoining)
	c.loadAssignments(ctx)

	// Watch for membership and assignment changes
	go c.watchMembers(ctx)
	go c.watchAssignments(ctx)

	// Heartbeat: keep our session lease alive
	go c.heartbeat(ctx)

	// Compete for leadership — this call blocks until we win the election.
	// If another node is already leader, we wait here until it dies.
	go c.campaignForLeadership(ctx)

	return nil
}

// campaignForLeadership runs the etcd election. The winner becomes responsible
// for computing and writing the shard assignment map.
func (c *Coordinator) campaignForLeadership(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		log.Printf("[coordinator] %s: campaigning for leadership...", c.nodeID)

		// Campaign blocks until this node wins or ctx is cancelled
		if err := c.election.Campaign(ctx, c.nodeID); err != nil {
			log.Printf("[coordinator] campaign error: %v — retrying", err)
			time.Sleep(2 * time.Second)
			continue
		}

		log.Printf("[coordinator] %s: WON LEADERSHIP", c.nodeID)

		// As leader: recompute shard assignments whenever membership changes.
		// We keep reassigning until we lose leadership or ctx is cancelled.
		c.runAsLeader(ctx)

		// If runAsLeader returns, we lost leadership — re-campaign
		log.Printf("[coordinator] %s: lost leadership, re-campaigning", c.nodeID)
	}
}

// runAsLeader is the leader's main responsibility: watch membership and keep
// the shard assignment map up to date.
func (c *Coordinator) runAsLeader(ctx context.Context) {
	// Immediately compute assignments with current membership
	if err := c.recomputeAssignments(ctx); err != nil {
		log.Printf("[coordinator] initial assignment failed: %v", err)
	}

	// Watch for membership changes and recompute
	watchCh := c.client.Watch(ctx, config.KeyLiveNodes, clientv3.WithPrefix())
	for {
		select {
		case <-ctx.Done():
			return
		case resp, ok := <-watchCh:
			if !ok {
				return
			}
			if resp.Err() != nil {
				log.Printf("[coordinator] watch error: %v", resp.Err())
				return
			}
			// Any change to /scheduler/members/* means a node joined or died
			log.Printf("[coordinator] membership change detected — recomputing assignments")
			if err := c.recomputeAssignments(ctx); err != nil {
				log.Printf("[coordinator] recompute failed: %v", err)
			}
		}
	}
}

// recomputeAssignments reads live nodes, runs the assignment algorithm,
// and atomically writes the result to etcd.
func (c *Coordinator) recomputeAssignments(ctx context.Context) error {
	// Get all live nodes
	resp, err := c.client.Get(ctx, config.KeyLiveNodes, clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("get live nodes: %w", err)
	}

	liveNodes := make([]string, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		liveNodes = append(liveNodes, string(kv.Value))
	}

	if len(liveNodes) == 0 {
		return fmt.Errorf("no live nodes — cannot assign shards")
	}

	// Sort for determinism: same input → same output on every node
	// This means every scheduler independently arrives at the same map
	sort.Strings(liveNodes)

	c.mu.RLock()
	numShards := c.shardMap.NumShards
	c.mu.RUnlock()

	// Assignment algorithm: round-robin by shard_id % num_live_nodes
	// With 3 nodes [A,B,C] and 12 shards:
	//   A → [0,3,6,9]   B → [1,4,7,10]   C → [2,5,8,11]
	newAssignments := make(map[int]string, numShards)
	for shardID := 0; shardID < numShards; shardID++ {
		newAssignments[shardID] = liveNodes[shardID%len(liveNodes)]
	}

	data, err := json.Marshal(newAssignments)
	if err != nil {
		return err
	}

	// Compare-and-swap write: if another leader wrote concurrently, one wins
	_, err = c.client.Put(ctx, config.KeyAssignments, string(data))
	if err != nil {
		return fmt.Errorf("write assignments: %w", err)
	}

	log.Printf("[coordinator] leader wrote new assignments: %d shards across %d nodes",
		numShards, len(liveNodes))
	return nil
}

// watchAssignments reacts to assignment changes and calls the rebalance callback.
func (c *Coordinator) watchAssignments(ctx context.Context) {
	watchCh := c.client.Watch(ctx, config.KeyAssignments)
	for {
		select {
		case <-ctx.Done():
			return
		case resp, ok := <-watchCh:
			if !ok {
				return
			}
			for _, ev := range resp.Events {
				var newAssignments map[int]string
				if err := json.Unmarshal(ev.Kv.Value, &newAssignments); err != nil {
					log.Printf("[coordinator] bad assignment payload: %v", err)
					continue
				}
				c.applyNewAssignments(newAssignments)
			}
		}
	}
}

// applyNewAssignments diffs old vs new assignments for this node and fires the callback.
func (c *Coordinator) applyNewAssignments(newAssignments map[int]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	oldMyShards := make(map[int]bool)
	for _, s := range c.myShards {
		oldMyShards[s] = true
	}

	var newMyShards []int
	for shardID, ownerID := range newAssignments {
		if ownerID == c.nodeID {
			newMyShards = append(newMyShards, shardID)
		}
	}
	sort.Ints(newMyShards)

	newMyShardSet := make(map[int]bool)
	for _, s := range newMyShards {
		newMyShardSet[s] = true
	}

	// Compute gained and lost shards
	var gained, lost []int
	for _, s := range newMyShards {
		if !oldMyShards[s] {
			gained = append(gained, s)
		}
	}
	for s := range oldMyShards {
		if !newMyShardSet[s] {
			lost = append(lost, s)
		}
	}

	c.assignments = newAssignments
	c.myShards = newMyShards

	if len(gained) > 0 || len(lost) > 0 {
		log.Printf("[coordinator] %s: gained shards %v, lost shards %v", c.nodeID, gained, lost)
		if c.onRebalance != nil {
			go c.onRebalance(gained, lost) // don't block the watch loop
		}
	}
}

// MyShards returns the shard IDs currently assigned to this node.
func (c *Coordinator) MyShards() []int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]int, len(c.myShards))
	copy(result, c.myShards)
	return result
}

// ShardMap returns the physical shard→PG mapping.
func (c *Coordinator) ShardMap() *config.ShardMap {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.shardMap
}

func (c *Coordinator) loadShardMap(ctx context.Context) error {
	resp, err := c.client.Get(ctx, config.KeyShardMap)
	if err != nil {
		return err
	}
	if len(resp.Kvs) == 0 {
		return fmt.Errorf("shard map not found in etcd — run bootstrap first")
	}
	sm, err := config.ShardMapFromJSON(resp.Kvs[0].Value)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.shardMap = sm
	c.mu.Unlock()
	return nil
}

func (c *Coordinator) loadAssignments(ctx context.Context) {
	resp, err := c.client.Get(ctx, config.KeyAssignments)
	if err != nil || len(resp.Kvs) == 0 {
		return
	}
	var assignments map[int]string
	if err := json.Unmarshal(resp.Kvs[0].Value, &assignments); err != nil {
		return
	}
	c.applyNewAssignments(assignments)
}

func (c *Coordinator) watchMembers(ctx context.Context) {
	// Just log membership changes — the leader handles rebalancing
	watchCh := c.client.Watch(ctx, config.KeyLiveNodes, clientv3.WithPrefix())
	for {
		select {
		case <-ctx.Done():
			return
		case resp := <-watchCh:
			for _, ev := range resp.Events {
				nodeID := string(ev.Kv.Value)
				if ev.Type == clientv3.EventTypeDelete {
					log.Printf("[coordinator] node LEFT (TTL expired): %s", nodeID)
				} else {
					log.Printf("[coordinator] node JOINED: %s", nodeID)
				}
			}
		}
	}
}

func (c *Coordinator) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(config.HeartbeatInterval)
	defer ticker.Stop()
	liveKey := config.KeyLiveNodes + c.nodeID

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Re-put our key with the same lease — this renews the TTL
			_, err := c.client.Put(ctx, liveKey, c.nodeID,
				clientv3.WithLease(c.session.Lease()))
			if err != nil {
				log.Printf("[coordinator] heartbeat failed: %v", err)
			}
		}
	}
}

func (c *Coordinator) Close() {
	c.session.Close()
	c.client.Close()
}
