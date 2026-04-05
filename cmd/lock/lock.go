package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

// =============================================================================
// DISTRIBUTED LOCK (MUTEX) WITH etcd
// =============================================================================
//
// HOW IT WORKS:
//
// A distributed mutex ensures that only ONE process across your entire cluster
// can hold the lock at a time. Unlike leader election (where the leader holds
// leadership continuously), a mutex is acquired and released for each
// critical section.
//
// Real-world uses:
//   - Flink: only one task can commit a checkpoint to S3 at a time
//   - Kubernetes: only one controller instance processes a specific resource
//   - Database migrations: only one pod runs ALTER TABLE at deploy time
//   - Cron jobs: prevent duplicate execution across replicas
//
// etcd's mutex uses the same lease + revision-ordering trick as leader election:
//   1. Candidate creates a key under the lock prefix with its lease attached
//   2. If its key has the lowest revision → it holds the lock
//   3. Otherwise, it watches the key just before it and waits for deletion
//   4. When the lock holder deletes its key (Unlock) or crashes (lease expires),
//      the next candidate's Watch fires and it acquires the lock
//
// COMPARISON WITH REDIS LOCKS (interview talking point):
//   - Redis (Redlock): SET key value NX PX ttl — simpler but no consensus
//     guarantee. In network partitions, two processes can believe they hold
//     the lock simultaneously (split-brain).
//   - etcd: Uses Raft consensus — the lock is guaranteed to be held by at most
//     one process even during network partitions (linearizable).
//   - Tradeoff: etcd is slower (consensus overhead) but safer.
// =============================================================================

const (
	etcdEndpoint = "localhost:2379"
	lockPrefix   = "/my-app/distributed-lock"
	leaseTTL     = 10 // seconds — crash detection window
)

func main() {
	workerID := flag.String("worker", "worker-1", "Unique worker identifier")
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Printf("[%s] Starting distributed lock demo", *workerID)

	// Connect to etcd
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{etcdEndpoint},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to connect to etcd: %v", err)
	}
	defer client.Close()

	// Create session (lease with auto-renewal)
	session, err := concurrency.NewSession(client, concurrency.WithTTL(leaseTTL))
	if err != nil {
		log.Fatalf("Failed to create session: %v", err)
	}
	defer session.Close()

	// Create the distributed mutex
	mutex := concurrency.NewMutex(session, lockPrefix)

	// Handle graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Printf("[%s] Shutting down...", *workerID)
		cancel()
	}()

	// -------------------------------------------------------------------------
	// Main work loop: repeatedly acquire lock → do critical work → release lock
	// -------------------------------------------------------------------------
	for i := 1; ; i++ {
		if ctx.Err() != nil {
			break
		}

		jobID := fmt.Sprintf("job-%d", i)
		if err := processJobWithLock(ctx, mutex, *workerID, jobID); err != nil {
			if ctx.Err() != nil {
				break // graceful shutdown
			}
			log.Printf("[%s] Error processing %s: %v", *workerID, jobID, err)
		}

		// Small pause between jobs
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(500+rand.Intn(1000)) * time.Millisecond):
		}
	}
}

func processJobWithLock(ctx context.Context, mutex *concurrency.Mutex, workerID, jobID string) error {
	// -------------------------------------------------------------------------
	// Acquire the lock
	//
	// Lock() BLOCKS until the lock is acquired or context is cancelled.
	// Under the hood:
	//   1. Creates key: /my-app/distributed-lock/<lease-id>
	//   2. Checks if this key has the lowest revision under the prefix
	//   3. If yes → lock acquired
	//   4. If no → watches the key with the next-lowest revision, blocks until
	//      that key is deleted (meaning previous holder released or crashed)
	//
	// This is a FAIR lock — candidates are served in order of arrival (FIFO),
	// preventing starvation. Redis Redlock doesn't guarantee fairness.
	// -------------------------------------------------------------------------
	log.Printf("[%s] ⏳ Attempting to acquire lock for %s...", workerID, jobID)

	lockCtx, lockCancel := context.WithTimeout(ctx, 30*time.Second)
	defer lockCancel()

	if err := mutex.Lock(lockCtx); err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}

	log.Printf("[%s] 🔒 ACQUIRED LOCK — processing %s (other workers are blocked)", workerID, jobID)

	// -------------------------------------------------------------------------
	// Critical section — ONLY this worker executes this at a time
	//
	// Simulate work like:
	//   - Writing a Flink checkpoint to S3
	//   - Running a database migration
	//   - Processing a batch that must not be duplicated
	// -------------------------------------------------------------------------
	workDuration := time.Duration(1000+rand.Intn(2000)) * time.Millisecond
	log.Printf("[%s] 🔧 Doing critical work for %s (will take %v)...", workerID, jobID, workDuration)

	select {
	case <-time.After(workDuration):
		log.Printf("[%s] ✅ Finished critical work for %s", workerID, jobID)
	case <-ctx.Done():
		log.Printf("[%s] ⚠️  Interrupted during critical work for %s", workerID, jobID)
	}

	// -------------------------------------------------------------------------
	// Release the lock
	//
	// Unlock() deletes the key from etcd, allowing the next waiter to proceed.
	// If we crash here instead of calling Unlock(), the lease expires after
	// TTL seconds and etcd auto-deletes the key — same effect, just slower.
	//
	// IMPORTANT: Always unlock in a defer or finally block in production code.
	// Here we do it explicitly for clarity.
	// -------------------------------------------------------------------------
	if err := mutex.Unlock(ctx); err != nil {
		return fmt.Errorf("failed to release lock: %w", err)
	}

	log.Printf("[%s] 🔓 Released lock for %s — other workers can proceed", workerID, jobID)
	return nil
}
