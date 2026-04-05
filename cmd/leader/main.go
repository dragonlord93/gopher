package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

// =============================================================================
// LEADER ELECTION WITH etcd
// =============================================================================
//
// HOW IT WORKS (maps to Kubernetes Lease-based election):
//
// 1. Each candidate creates a LEASE (like a TTL heartbeat) with etcd.
//    - The lease must be renewed periodically, or etcd auto-deletes keys tied to it.
//    - This is equivalent to leaseDurationSeconds in a Kubernetes Lease object.
//
// 2. Each candidate calls Campaign() which tries to become leader by writing
//    its identity to a special election key in etcd.
//    - Only ONE candidate succeeds (etcd uses Raft consensus internally).
//    - Others BLOCK in Campaign() waiting for their turn.
//
// 3. The leader keeps running until:
//    - It calls Resign() (graceful shutdown)
//    - It crashes (lease expires, etcd deletes the key, next candidate wins)
//
// 4. Standbys detect leadership change and one of them wins the next Campaign().
//
// THIS IS EXACTLY what happens with:
//   - Flink JobManager HA (ZooKeeper or K8s Lease)
//   - Kafka controller election (KRaft or ZooKeeper)
//   - Any Kubernetes controller with --leader-elect=true
// =============================================================================

const (
	etcdEndpoint = "localhost:2379"
	electionName = "/my-app/leader-election" // election "namespace" in etcd
	leaseTTL     = 5                         // seconds — if leader crashes, new election in ~5s
)

func main() {
	// Parse node identity from command line
	nodeID := flag.String("id", "node-1", "Unique identifier for this node")
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Printf("[%s] Starting up — attempting to join leader election", *nodeID)

	// -------------------------------------------------------------------------
	// Step 1: Connect to etcd
	// -------------------------------------------------------------------------
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{etcdEndpoint},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to connect to etcd: %v", err)
	}
	defer client.Close()
	log.Printf("[%s] Connected to etcd at %s", *nodeID, etcdEndpoint)

	// -------------------------------------------------------------------------
	// Step 2: Create a Session (wraps a lease with auto-renewal)
	//
	// A Session creates an etcd lease and automatically sends KeepAlive RPCs.
	// If this process crashes, the KeepAlive stops, the lease expires after
	// TTL seconds, and all keys tied to this lease are deleted — including
	// the leadership key. This is the same mechanism as Kubernetes Lease renewal.
	// -------------------------------------------------------------------------
	session, err := concurrency.NewSession(client, concurrency.WithTTL(leaseTTL))
	if err != nil {
		log.Fatalf("Failed to create session: %v", err)
	}
	defer session.Close()
	log.Printf("[%s] Session created with lease TTL=%ds (lease ID: %x)",
		*nodeID, leaseTTL, session.Lease())

	// -------------------------------------------------------------------------
	// Step 3: Create an Election and Campaign for leadership
	//
	// Campaign() is a BLOCKING call:
	//   - If no current leader → this node becomes leader immediately
	//   - If another node is leader → this blocks until that leader resigns
	//     or its lease expires (crash detection)
	//
	// Under the hood, Campaign() creates a key like:
	//   /my-app/leader-election/<lease-id> = "node-1"
	// The candidate with the lowest key revision wins (first-come-first-serve
	// among waiting candidates).
	// -------------------------------------------------------------------------
	election := concurrency.NewElection(session, electionName)

	// Set up graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Campaign in a goroutine so we can handle signals
	leaderCh := make(chan struct{})
	go func() {
		log.Printf("[%s] Campaigning for leadership (may block if another leader exists)...", *nodeID)

		// This BLOCKS until we become leader or context is cancelled
		if err := election.Campaign(ctx, *nodeID); err != nil {
			log.Printf("[%s] Campaign ended: %v", *nodeID, err)
			return
		}

		log.Printf("🏆 [%s] ===== I AM THE LEADER =====", *nodeID)
		close(leaderCh)
	}()

	// -------------------------------------------------------------------------
	// Step 4: Observe leadership changes (from any node)
	//
	// Observe() returns a channel that emits the current leader's identity
	// whenever leadership changes. This is like a Kubernetes Watch on the
	// Lease object.
	// -------------------------------------------------------------------------
	go func() {
		observeCh := election.Observe(ctx)
		for response := range observeCh {
			for _, kv := range response.Kvs {
				log.Printf("[%s] 👀 Observed leader is: %s (key: %s, revision: %d)",
					*nodeID, string(kv.Value), string(kv.Key), kv.ModRevision)
			}
		}
	}()

	// -------------------------------------------------------------------------
	// Step 5: Do leader work or wait
	// -------------------------------------------------------------------------
	select {
	case <-leaderCh:
		// We are the leader — simulate doing work
		log.Printf("[%s] Starting leader work loop...", *nodeID)
		doLeaderWork(ctx, *nodeID)

	case sig := <-sigCh:
		log.Printf("[%s] Received signal %v while waiting for leadership", *nodeID, sig)
		cancel()
		return
	}

	// -------------------------------------------------------------------------
	// Step 6: Graceful resign on shutdown
	//
	// Resign() deletes the leadership key immediately (no waiting for TTL).
	// This triggers the fastest possible failover — the next standby's
	// Campaign() returns immediately.
	// -------------------------------------------------------------------------
	sig := <-sigCh
	log.Printf("[%s] Received signal %v — resigning leadership", *nodeID, sig)

	resignCtx, resignCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer resignCancel()

	if err := election.Resign(resignCtx); err != nil {
		log.Printf("[%s] Error resigning: %v (lease will expire in %ds anyway)", *nodeID, err, leaseTTL)
	} else {
		log.Printf("[%s] Resigned leadership gracefully", *nodeID)
	}
}

// doLeaderWork simulates the work a leader does (e.g., Flink JobManager
// coordinating checkpoints, a Kubernetes controller processing events).
func doLeaderWork(ctx context.Context, nodeID string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	taskNum := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			taskNum++
			log.Printf("🏆 [%s] Leader doing work: task #%d (scheduling checkpoint, assigning slots...)",
				nodeID, taskNum)
		}
	}
}
