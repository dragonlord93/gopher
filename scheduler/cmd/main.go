package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	"scheduler/internal/config"
	"scheduler/internal/coordinator"
	"scheduler/internal/queue"
	"scheduler/internal/scheduler"
	"scheduler/internal/shard"
	"scheduler/internal/worker"
)

func main() {
	// ── Configuration ────────────────────────────────────────────────────────────
	etcdEndpoints := []string{"localhost:2379"}
	kafkaBrokers  := []string{"localhost:9092"}
	pgHosts       := []string{
		"pg-0.internal:5432",
		"pg-1.internal:5432",
		"pg-2.internal:5432",
	}

	nodeID := fmt.Sprintf("scheduler-%s", uuid.New().String()[:8])
	log.Printf("starting node %s", nodeID)

	ctx, cancel := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// ── Bootstrap shard map (run only once, on first deploy) ─────────────────────
	// In production, move this to a separate CLI command: `scheduler bootstrap`
	// and guard it with a flag so it never runs on normal startup.
	if os.Getenv("BOOTSTRAP") == "true" {
		if err := bootstrap(ctx, etcdEndpoints, pgHosts); err != nil {
			log.Fatalf("bootstrap: %v", err)
		}
		log.Println("bootstrap complete")
		return
	}

	// ── Build the shard router ───────────────────────────────────────────────────
	// The coordinator will load the shard map from etcd.
	// We build the router after the coordinator has the map.
	coord, err := coordinator.New(etcdEndpoints, nodeID)
	if err != nil {
		log.Fatalf("coordinator: %v", err)
	}
	defer coord.Close()

	// Start coordinator: registers liveness, loads shard map, watches for changes
	if err := coord.Start(ctx); err != nil {
		log.Fatalf("coordinator start: %v", err)
	}

	// Build router from the shard map the coordinator just loaded
	router := shard.NewRouter(coord.ShardMap())
	if err := router.Init(ctx); err != nil {
		log.Fatalf("router init: %v", err)
	}
	defer router.Close()

	// ── Kafka producer (used by scheduler) ──────────────────────────────────────
	producer := queue.NewProducer(kafkaBrokers)
	defer producer.Close()

	// ── Scheduler ────────────────────────────────────────────────────────────────
	sched := scheduler.New(nodeID, router, producer)

	// Wire rebalance callback: coordinator calls this when shard assignments change
	coord.SetRebalanceCallback(sched.OnRebalance)

	// ── Worker pool ──────────────────────────────────────────────────────────────
	// Workers run in the same process here for simplicity.
	// In production they'd be a separate deployment that scales independently.
	consumer := queue.NewConsumer(kafkaBrokers, nodeID)
	defer consumer.Close()

	w := worker.New(nodeID+"-worker", consumer, router)

	// Register job handlers — in production each type has its own function
	w.Register("default", func(ctx context.Context, payload []byte) error {
		log.Printf("[handler] executing job with payload: %s", payload)
		time.Sleep(50 * time.Millisecond) // simulate work
		return nil
	})

	// ── Example: register a test job ─────────────────────────────────────────────
	go func() {
		time.Sleep(2 * time.Second) // wait for scheduler to be ready
		testJob := &config.Job{
			JobID:       uuid.New().String(),
			TenantID:    "acme-corp",
			CronExpr:    "* * * * *", // every minute
			Payload:     []byte(`{"type":"daily_report","params":{"format":"pdf"}}`),
			TimeZone:    "UTC",
			MaxRetries:  3,
			TimeoutSecs: 30,
		}
		if err := sched.RegisterJob(ctx, testJob); err != nil {
			log.Printf("register test job: %v", err)
		}
	}()

	// ── Start everything ─────────────────────────────────────────────────────────
	errCh := make(chan error, 2)

	go func() {
		errCh <- sched.Run(ctx, coord.MyShards())
	}()

	go func() {
		errCh <- w.Run(ctx)
	}()

	// Wait for shutdown signal or error
	select {
	case <-ctx.Done():
		log.Println("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			log.Printf("component error: %v", err)
			cancel()
		}
	}

	log.Println("node stopped")
}

// bootstrap writes the initial shard map to etcd.
// Run once: BOOTSTRAP=true ./scheduler
func bootstrap(ctx context.Context, etcdEndpoints, pgHosts []string) error {
	const numShards = 100 // chosen for 10× headroom — never change this after first deploy

	sm, err := config.BootstrapShardMap(pgHosts, numShards)
	if err != nil {
		return err
	}

	data, err := sm.ToJSON()
	if err != nil {
		return err
	}

	cli, err := coordinator.NewEtcdClient(etcdEndpoints)
	if err != nil {
		return err
	}
	defer cli.Close()

	_, err = cli.Put(ctx, config.KeyShardMap, string(data))
	if err != nil {
		return fmt.Errorf("write shard map: %w", err)
	}

	log.Printf("bootstrapped: %d shards across %d PG instances", numShards, len(pgHosts))
	for i, host := range pgHosts {
		log.Printf("  pg-%d (%s): shards %d, %d, %d, ... (+%d more)",
			i, host, i, i+len(pgHosts), i+2*len(pgHosts),
			numShards/len(pgHosts)-3)
	}
	return nil
}
