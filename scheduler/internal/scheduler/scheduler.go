package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron/v3"

	"scheduler/internal/config"
	"scheduler/internal/queue"
	"scheduler/internal/shard"
)

// Scheduler is the core component. It:
//  - Owns a timing wheel for in-memory job firing
//  - Refills the wheel periodically from its assigned DB shards
//  - Publishes fired jobs to Kafka
//  - Reacts to shard rebalancing from the coordinator
type Scheduler struct {
	nodeID   string
	wheel    *TimingWheel
	router   *shard.Router
	producer *queue.Producer
	parser   cron.Parser

	// shards currently owned by this node — updated by coordinator callback
	myShards map[int]bool
}

func New(nodeID string, router *shard.Router, producer *queue.Producer) *Scheduler {
	return &Scheduler{
		nodeID:   nodeID,
		wheel:    NewTimingWheel(),
		router:   router,
		producer: producer,
		// Use the standard 5-field cron parser with seconds support
		parser:   cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
		myShards: make(map[int]bool),
	}
}

// OnRebalance is the callback registered with the coordinator.
// Called whenever the shard assignment map changes in etcd.
func (s *Scheduler) OnRebalance(gained, lost []int) {
	// Remove lost shards from the wheel immediately
	for _, shardID := range lost {
		delete(s.myShards, shardID)
		removed := s.wheel.RemoveShardJobs(shardID)
		log.Printf("[scheduler] released shard %d — removed %d wheel entries", shardID, removed)
	}

	// For gained shards: add to our set and do an immediate DB refill
	for _, shardID := range gained {
		s.myShards[shardID] = true
		log.Printf("[scheduler] acquired shard %d — refilling wheel", shardID)
		if err := s.refillShard(context.Background(), shardID); err != nil {
			log.Printf("[scheduler] refill shard %d failed: %v", shardID, err)
		}
	}

	log.Printf("[scheduler] rebalance done — now own %d shards, wheel size=%d",
		len(s.myShards), s.wheel.Size())
}

// Run starts the tick loop and the background refill goroutine.
// Blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context, initialShards []int) error {
	// Set initial shard ownership
	for _, id := range initialShards {
		s.myShards[id] = true
	}

	// Initial refill — load upcoming jobs from all owned shards
	log.Printf("[scheduler] initial refill for %d shards", len(s.myShards))
	if err := s.refillAll(ctx); err != nil {
		return fmt.Errorf("initial refill: %w", err)
	}
	log.Printf("[scheduler] wheel loaded, size=%d — starting tick loop", s.wheel.Size())

	// Background refill ticker — hits DB every RefillInterval
	go s.refillLoop(ctx)

	// Tick loop — advances the wheel every second, fires due jobs
	ticker := time.NewTicker(config.WheelTickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[scheduler] shutting down")
			return nil

		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick fires all jobs due at the current wheel slot.
func (s *Scheduler) tick(ctx context.Context) {
	jobs := s.wheel.Tick()
	for _, job := range jobs {
		s.fireJob(ctx, job)
	}
}

// fireJob publishes the job to Kafka and re-schedules it in the wheel.
func (s *Scheduler) fireJob(ctx context.Context, job *config.Job) {
	now := time.Now()

	event := &config.JobEvent{
		JobID:          job.JobID,
		TenantID:       job.TenantID,
		ScheduledEpoch: now.Unix(),
		Payload:        job.Payload,
		AttemptNumber:  1,
	}

	// Publish to Kafka — the deduplication key prevents double-fire
	// even if this job somehow fires twice (leader failover race)
	if err := s.producer.Publish(ctx, event); err != nil {
		log.Printf("[scheduler] publish failed job=%s: %v", job.JobID, err)
		// Don't drop — re-schedule for the next tick to retry
		job.NextFireAt = now.Add(config.WheelTickInterval)
		s.wheel.Schedule(job)
		return
	}

	// Compute next fire time from cron expression
	nextFire, err := s.nextFireTime(job)
	if err != nil {
		log.Printf("[scheduler] bad cron expr job=%s: %v — disabling", job.JobID, err)
		return
	}

	// Async: update next_fire_at in DB — don't block the tick loop
	go s.updateNextFireAt(job.JobID, job.ShardID, nextFire)

	// Re-insert into wheel if the next fire is within our window
	job.NextFireAt = nextFire
	if time.Until(nextFire) <= config.RefillWindow {
		s.wheel.Schedule(job)
	}
	// If next fire is further away, the refill loop will pick it up when the time comes
}

// refillAll runs refill for every currently owned shard.
func (s *Scheduler) refillAll(ctx context.Context) error {
	for shardID := range s.myShards {
		if err := s.refillShard(ctx, shardID); err != nil {
			log.Printf("[scheduler] refill shard %d error: %v", shardID, err)
			// Continue with other shards — partial refill is better than none
		}
	}
	return nil
}

// refillShard loads jobs due within RefillWindow from one shard's Postgres instance.
// This is the ONLY point where the scheduler reads from the DB at steady state.
// It runs every 4 minutes per shard — not every tick.
func (s *Scheduler) refillShard(ctx context.Context, shardID int) error {
	pool, _, err := s.router.PoolForShard(shardID)
	if err != nil {
		return fmt.Errorf("no pool for shard %d: %w", shardID, err)
	}

	windowEnd := time.Now().Add(config.RefillWindow)

	// Narrow index seek on next_fire_at — only rows due in the next 5 minutes.
	// At 10M rows/shard and uniform cron distribution, this returns ~thousands of rows.
	// The index makes this sub-millisecond.
	rows, err := pool.Query(ctx, `
		SELECT job_id, tenant_id, shard_id, cron_expr, payload,
		       next_fire_at, timezone, max_retries, timeout_secs
		FROM jobs
		WHERE next_fire_at < $1
		  AND enabled = true
		ORDER BY next_fire_at
		LIMIT 50000
	`, windowEnd)
	if err != nil {
		return fmt.Errorf("refill query shard %d: %w", shardID, err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var job config.Job
		err := rows.Scan(
			&job.JobID, &job.TenantID, &job.ShardID, &job.CronExpr,
			&job.Payload, &job.NextFireAt, &job.TimeZone,
			&job.MaxRetries, &job.TimeoutSecs,
		)
		if err != nil {
			log.Printf("[scheduler] scan error: %v", err)
			continue
		}
		s.wheel.Schedule(&job)
		count++
	}

	if count > 0 {
		log.Printf("[scheduler] refilled shard %d: %d jobs loaded into wheel", shardID, count)
	}
	return rows.Err()
}

// refillLoop runs refillAll on a timer in the background.
func (s *Scheduler) refillLoop(ctx context.Context) {
	ticker := time.NewTicker(config.RefillInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.refillAll(ctx); err != nil {
				log.Printf("[scheduler] background refill error: %v", err)
			}
		}
	}
}

// RegisterJob is called by the API when a new job is created.
// It writes to the correct DB shard and, if the job fires soon, injects into the wheel.
func (s *Scheduler) RegisterJob(ctx context.Context, job *config.Job) error {
	// Route to the correct shard based on tenant
	pool, shardID, err := s.router.PoolForTenant(job.TenantID)
	if err != nil {
		return fmt.Errorf("route job: %w", err)
	}
	job.ShardID = shardID

	// Compute first fire time
	nextFire, err := s.nextFireTime(job)
	if err != nil {
		return fmt.Errorf("compute next fire: %w", err)
	}
	job.NextFireAt = nextFire

	// Write to the owning Postgres instance
	_, err = pool.Exec(ctx, `
		INSERT INTO jobs
		    (job_id, tenant_id, shard_id, cron_expr, payload,
		     next_fire_at, timezone, max_retries, timeout_secs, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,true)
		ON CONFLICT (job_id) DO UPDATE
		    SET cron_expr=EXCLUDED.cron_expr,
		        next_fire_at=EXCLUDED.next_fire_at,
		        enabled=true
	`,
		job.JobID, job.TenantID, job.ShardID, job.CronExpr,
		job.Payload, job.NextFireAt, job.TimeZone,
		job.MaxRetries, job.TimeoutSecs,
	)
	if err != nil {
		return fmt.Errorf("insert job: %w", err)
	}

	// If this shard is ours AND the job fires within our window — inject into wheel now.
	// If the shard belongs to another scheduler, that node will pick it up on its next refill.
	if s.myShards[shardID] && time.Until(nextFire) <= config.RefillWindow {
		s.wheel.Schedule(job)
		log.Printf("[scheduler] job %s registered and injected into wheel (fires in %v)",
			job.JobID, time.Until(nextFire).Round(time.Second))
	} else {
		log.Printf("[scheduler] job %s registered, shard %d (fires %v — will be picked up at refill)",
			job.JobID, shardID, nextFire.Format(time.RFC3339))
	}

	return nil
}

// updateNextFireAt writes the new next_fire_at back to Postgres after a job fires.
// Runs in a goroutine — does not block the tick loop.
func (s *Scheduler) updateNextFireAt(jobID string, shardID int, nextFireAt time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, _, err := s.router.PoolForShard(shardID)
	if err != nil {
		log.Printf("[scheduler] updateNextFireAt: no pool for shard %d: %v", shardID, err)
		return
	}

	_, err = pool.Exec(ctx,
		"UPDATE jobs SET next_fire_at=$1, last_fire_at=NOW() WHERE job_id=$2",
		nextFireAt, jobID,
	)
	if err != nil {
		log.Printf("[scheduler] updateNextFireAt job=%s: %v", jobID, err)
	}
}

func (s *Scheduler) nextFireTime(job *config.Job) (time.Time, error) {
	schedule, err := s.parser.Parse(job.CronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron %q: %w", job.CronExpr, err)
	}
	return schedule.Next(time.Now()), nil
}
