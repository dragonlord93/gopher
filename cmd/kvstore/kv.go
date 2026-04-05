package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// =============================================================================
// LINEARIZABLE KV STORE WITH etcd
// =============================================================================
//
// This demonstrates three core primitives that power Kubernetes and distributed
// systems:
//
// 1. COMPARE-AND-SWAP (CAS):
//    "Update this key ONLY IF its current value/revision matches what I expect."
//    This is how Kubernetes prevents conflicting updates — the resourceVersion
//    field on every object is an etcd ModRevision. Two controllers trying to
//    update the same Pod? Only one succeeds; the other gets a conflict.
//
// 2. TRANSACTIONS:
//    "Atomically check conditions and apply multiple writes."
//    This is how you implement things like atomic bank transfers or
//    Kubernetes multi-resource atomic updates.
//
// 3. WATCH:
//    "Notify me whenever a key (or key range) changes."
//    This is the backbone of Kubernetes controllers — they Watch resources
//    and react to changes (the informer/reflector pattern). It's also how
//    Flink standbys detect leadership changes.
//
// LINEARIZABILITY means: every read sees the most recent write. There's no
// "stale read" problem like you'd have with eventually-consistent systems.
// etcd guarantees this because reads go through the Raft leader.
// =============================================================================

const etcdEndpoint = "localhost:2379"

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{etcdEndpoint},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to connect to etcd: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// Clean up from previous runs
	client.Delete(ctx, "", clientv3.WithPrefix())

	fmt.Println("=" + repeatStr("=", 69))
	fmt.Println("  DEMO 1: Compare-And-Swap (CAS) — Preventing Lost Updates")
	fmt.Println("=" + repeatStr("=", 69))
	demoCAS(ctx, client)

	fmt.Println()
	fmt.Println("=" + repeatStr("=", 69))
	fmt.Println("  DEMO 2: Transactions — Atomic Multi-Key Operations")
	fmt.Println("=" + repeatStr("=", 69))
	demoTransaction(ctx, client)

	fmt.Println()
	fmt.Println("=" + repeatStr("=", 69))
	fmt.Println("  DEMO 3: Watch — Reactive Change Notifications")
	fmt.Println("=" + repeatStr("=", 69))
	demoWatch(ctx, client)

	// Clean up
	client.Delete(ctx, "", clientv3.WithPrefix())
	fmt.Println("\n✅ All demos complete!")
}

// =============================================================================
// DEMO 1: Compare-And-Swap (CAS)
//
// Problem: Two goroutines read a counter, increment it, and write it back.
// Without CAS, you get lost updates (classic race condition).
// With CAS, only one write succeeds per revision — the other must retry.
//
// This is EXACTLY how Kubernetes handles concurrent updates:
//   kubectl get pod → modify → kubectl apply
//   If someone else modified the pod between your get and apply, you get:
//   "the object has been modified; please apply your changes to the latest version"
//   That error IS a CAS failure.
// =============================================================================

func demoCAS(ctx context.Context, client *clientv3.Client) {
	key := "/demo/counter"

	// Initialize counter to 0
	client.Put(ctx, key, "0")
	fmt.Println("\n📝 Counter initialized to 0")
	fmt.Println("🏃 Launching 5 goroutines, each incrementing 10 times (expected final: 50)")

	var wg sync.WaitGroup
	for g := 0; g < 5; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				incrementWithCAS(ctx, client, key, goroutineID)
			}
		}(g)
	}
	wg.Wait()

	// Read final value
	resp, _ := client.Get(ctx, key)
	finalValue := string(resp.Kvs[0].Value)
	fmt.Printf("\n🎯 Final counter value: %s (expected: 50)\n", finalValue)

	if finalValue == "50" {
		fmt.Println("✅ CAS prevented all lost updates!")
	} else {
		fmt.Println("❌ Something went wrong (this shouldn't happen with CAS)")
	}
}

func incrementWithCAS(ctx context.Context, client *clientv3.Client, key string, goroutineID int) {
	for attempt := 0; ; attempt++ {
		// Step 1: READ the current value and its ModRevision
		resp, err := client.Get(ctx, key)
		if err != nil || len(resp.Kvs) == 0 {
			log.Printf("[G%d] Read failed, retrying...", goroutineID)
			continue
		}

		currentValue, _ := strconv.Atoi(string(resp.Kvs[0].Value))
		currentRevision := resp.Kvs[0].ModRevision

		// Step 2: Compute new value
		newValue := strconv.Itoa(currentValue + 1)

		// Step 3: WRITE with CAS — "set to newValue ONLY IF ModRevision still matches"
		//
		// This is a TRANSACTION with:
		//   IF:   key's ModRevision == currentRevision  (the "compare")
		//   THEN: PUT key = newValue                     (the "swap")
		//   ELSE: do nothing (we'll retry)
		//
		// etcd executes this atomically via Raft consensus.
		txnResp, err := client.Txn(ctx).
			If(clientv3.Compare(clientv3.ModRevision(key), "=", currentRevision)).
			Then(clientv3.OpPut(key, newValue)).
			Else(). // Empty else — we just check if it succeeded
			Commit()

		if err != nil {
			log.Printf("[G%d] Transaction error: %v", goroutineID, err)
			continue
		}

		if txnResp.Succeeded {
			// CAS succeeded — our write was applied
			if attempt > 0 {
				fmt.Printf("  [G%d] ✅ CAS succeeded after %d retries: %d → %s\n",
					goroutineID, attempt, currentValue, newValue)
			}
			return
		}

		// CAS FAILED — someone else modified the key between our read and write.
		// This is the "409 Conflict" equivalent in Kubernetes.
		// We MUST re-read and retry.
		fmt.Printf("  [G%d] ⚡ CAS conflict (attempt %d): expected revision %d but it changed. Retrying...\n",
			goroutineID, attempt+1, currentRevision)
	}
}

// =============================================================================
// DEMO 2: Transactions — Atomic Multi-Key Operations
//
// Problem: Transfer money between two accounts. Both the debit and credit
// must happen atomically — no partial state.
//
// This is like Kubernetes ensuring that when a ReplicaSet controller scales
// up, it atomically updates both the Pod count and the ReplicaSet status.
// =============================================================================

func demoTransaction(ctx context.Context, client *clientv3.Client) {
	accountA := "/accounts/alice"
	accountB := "/accounts/bob"

	// Initialize accounts
	client.Put(ctx, accountA, "1000")
	client.Put(ctx, accountB, "500")
	fmt.Println("\n📝 Alice: $1000, Bob: $500")

	// Transfer $300 from Alice to Bob
	transferAmount := 300
	fmt.Printf("💸 Transferring $%d from Alice to Bob...\n", transferAmount)

	// Read current balances
	aliceResp, _ := client.Get(ctx, accountA)
	bobResp, _ := client.Get(ctx, accountB)

	aliceBalance, _ := strconv.Atoi(string(aliceResp.Kvs[0].Value))
	bobBalance, _ := strconv.Atoi(string(bobResp.Kvs[0].Value))

	if aliceBalance < transferAmount {
		fmt.Println("❌ Insufficient funds")
		return
	}

	newAlice := strconv.Itoa(aliceBalance - transferAmount)
	newBob := strconv.Itoa(bobBalance + transferAmount)

	// Atomic transaction:
	//   IF: both accounts haven't been modified since we read them
	//   THEN: update both balances atomically
	//   ELSE: abort (someone else modified an account concurrently)
	txnResp, err := client.Txn(ctx).
		If(
			// Guard: ensure neither account was modified since our read
			clientv3.Compare(clientv3.ModRevision(accountA), "=", aliceResp.Kvs[0].ModRevision),
			clientv3.Compare(clientv3.ModRevision(accountB), "=", bobResp.Kvs[0].ModRevision),
		).
		Then(
			// Both writes happen atomically — all or nothing
			clientv3.OpPut(accountA, newAlice),
			clientv3.OpPut(accountB, newBob),
		).
		Else(
			// Transaction aborted — in production, you'd retry
			clientv3.OpGet(accountA),
			clientv3.OpGet(accountB),
		).
		Commit()

	if err != nil {
		log.Fatalf("Transaction failed: %v", err)
	}

	if txnResp.Succeeded {
		fmt.Printf("✅ Transfer complete! Alice: $%s, Bob: $%s\n", newAlice, newBob)
		fmt.Println("   Both writes happened atomically — no partial state possible")
	} else {
		fmt.Println("⚡ Transaction aborted — concurrent modification detected")
		fmt.Println("   In production, you'd re-read both accounts and retry")
	}

	// Verify by reading back
	fmt.Println("\n📖 Verifying final state:")
	aliceResp, _ = client.Get(ctx, accountA)
	bobResp, _ = client.Get(ctx, accountB)
	fmt.Printf("   Alice: $%s, Bob: $%s\n", string(aliceResp.Kvs[0].Value), string(bobResp.Kvs[0].Value))

	total, _ := strconv.Atoi(string(aliceResp.Kvs[0].Value))
	bobTotal, _ := strconv.Atoi(string(bobResp.Kvs[0].Value))
	fmt.Printf("   Total: $%d (should be $1500 — conservation of money ✓)\n", total+bobTotal)
}

// =============================================================================
// DEMO 3: Watch — Reactive Change Notifications
//
// A watcher subscribes to key changes and reacts in real-time.
//
// This is the foundation of:
//   - Kubernetes informers/controllers: watch Pod changes → reconcile
//   - Flink standby JobManagers: watch leader key → take over on deletion
//   - Service discovery: watch service endpoints → update routing tables
//
// Watches are ORDERED and RELIABLE — etcd guarantees you won't miss events
// and they arrive in the order they happened (linearizable).
// =============================================================================

func demoWatch(ctx context.Context, client *clientv3.Client) {
	watchKey := "/demo/config"

	fmt.Println("\n👁️  Starting watcher on /demo/config")
	fmt.Println("   Writer will make 5 changes; watcher reacts to each one\n")

	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()

	var wg sync.WaitGroup

	// ---- Watcher goroutine (like a Kubernetes controller) ----
	wg.Add(1)
	go func() {
		defer wg.Done()
		eventsReceived := 0

		// Watch returns a channel that emits WatchResponse on every change
		watchCh := client.Watch(watchCtx, watchKey)

		for watchResp := range watchCh {
			for _, event := range watchResp.Events {
				eventsReceived++
				switch event.Type {
				case clientv3.EventTypePut:
					fmt.Printf("   👁️  WATCH: Key %s UPDATED to \"%s\" (revision: %d)\n",
						string(event.Kv.Key), string(event.Kv.Value), event.Kv.ModRevision)

					// React to the change (like a controller reconciliation)
					fmt.Printf("   🔄 REACT: Processing config change #%d — would update downstream systems\n",
						eventsReceived)

				case clientv3.EventTypeDelete:
					fmt.Printf("   👁️  WATCH: Key %s DELETED (revision: %d)\n",
						string(event.Kv.Key), event.Kv.ModRevision)
					fmt.Printf("   🔄 REACT: Config removed — reverting to defaults\n")
				}

				if eventsReceived >= 6 { // 5 puts + 1 delete
					watchCancel()
					return
				}
			}
		}
	}()

	// ---- Writer goroutine (simulates config changes) ----
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(500 * time.Millisecond) // Let watcher start first

		configs := []string{
			"debug=false",
			"debug=true",
			"max_connections=100",
			"max_connections=200",
			"feature_flag_new_ui=enabled",
		}

		for i, config := range configs {
			fmt.Printf("   ✏️  WRITE #%d: Setting config to \"%s\"\n", i+1, config)
			client.Put(ctx, watchKey, config)
			time.Sleep(800 * time.Millisecond) // Pause so output is readable
		}

		// Final: delete the key
		fmt.Println("   🗑️  DELETE: Removing config key")
		client.Delete(ctx, watchKey)
	}()

	wg.Wait()
	fmt.Println("\n✅ Watch demo complete — all 6 events received in order")
}

func repeatStr(s string, count int) string {
	result := ""
	for i := 0; i < count; i++ {
		result += s
	}
	return result
}
