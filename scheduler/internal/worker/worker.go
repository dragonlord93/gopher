package worker

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"scheduler/internal/config"
	"scheduler/internal/queue"
	"scheduler/internal/shard"
)

// ExecutionStatus values written to job_executions table
type ExecutionStatus string

const (
	StatusRunning ExecutionStatus = "running"
	StatusSuccess ExecutionStatus = "success"
	StatusFailed  ExecutionStatus = "failed"
	StatusTimeout ExecutionStatus = "timeout"
)

// JobHandler is the function signature for actual job business logic.
// Replace this with your real implementations (e.g. send email, run ETL, etc.)
type JobHandler func(ctx context.Context, payload []byte) error

// Worker consumes JobEvents from Kafka and executes them.
// Workers are completely stateless — all state lives in Postgres.
// Scale workers horizontally by adding more instances; Kafka distributes load.
type Worker struct {
	workerID string
	consumer *queue.Consumer
	router   *shard.Router
	handlers map[string]JobHandler // job_type → handler function
}

func New(workerID string, consumer *queue.Consumer, router *shard.Router) *Worker {
	return &Worker{
		workerID: workerID,
		consumer: consumer,
		router:   router,
		handlers: make(map[string]JobHandler),
	}
}

// Register maps a job type string to a handler function.
// In production you'd have many handlers: "send_email", "generate_report", etc.
func (w *Worker) Register(jobType string, handler JobHandler) {
	w.handlers[jobType] = handler
}

// Run starts consuming and processing jobs. Blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	log.Printf("[worker] %s starting", w.workerID)
	return w.consumer.Run(ctx, w.process)
}

// process is the core handler called for each Kafka message.
// It is intentionally idempotent: if called twice with the same event,
// the second call is a no-op (the execution row already exists with status=success).
func (w *Worker) process(ctx context.Context, event *config.JobEvent) error {
	log.Printf("[worker] %s processing job=%s attempt=%d",
		w.workerID, event.JobID, event.AttemptNumber)

	// Get the pool for this job's shard
	// We look up the shard from the router using the event's job metadata
	pool, shardID, err := w.routeEvent(ctx, event)
	if err != nil {
		return fmt.Errorf("route event: %w", err)
	}

	// Create execution record — idempotent via ON CONFLICT DO NOTHING
	// If this event was already processed, executionID will be empty and we skip
	executionID, alreadyProcessed, err := w.createExecution(ctx, pool, event)
	if err != nil {
		return fmt.Errorf("create execution: %w", err)
	}
	if alreadyProcessed {
		log.Printf("[worker] job=%s epoch=%d already processed — skipping (idempotent)",
			event.JobID, event.ScheduledEpoch)
		return nil // success — commit the Kafka offset
	}

	// Fetch the full job definition to get payload type, timeout, etc.
	job, err := w.fetchJob(ctx, pool, event.JobID)
	if err != nil {
		w.markFailed(ctx, pool, executionID, shardID, err.Error())
		return fmt.Errorf("fetch job: %w", err)
	}

	// Execute with timeout
	timeout := time.Duration(job.TimeoutSecs) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second // sensible default
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	execErr := w.execute(execCtx, job, event)
	elapsed := time.Since(start)

	if execErr != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			log.Printf("[worker] job=%s TIMED OUT after %v", event.JobID, elapsed)
			w.markStatus(ctx, pool, executionID, shardID, StatusTimeout, execErr.Error(), elapsed)
		} else {
			log.Printf("[worker] job=%s FAILED after %v: %v", event.JobID, elapsed, execErr)
			w.markStatus(ctx, pool, executionID, shardID, StatusFailed, execErr.Error(), elapsed)
		}

		// Return error so Kafka does NOT commit — message will be redelivered
		// In production, check attempt count and route to DLQ after max retries
		if event.AttemptNumber >= job.MaxRetries {
			log.Printf("[worker] job=%s exhausted retries (%d) — marking dead",
				event.JobID, job.MaxRetries)
			w.markStatus(ctx, pool, executionID, shardID, StatusFailed,
				"max retries exhausted", elapsed)
			return nil // commit to stop retrying — job is dead
		}
		return execErr
	}

	w.markStatus(ctx, pool, executionID, shardID, StatusSuccess, "", elapsed)
	log.Printf("[worker] job=%s SUCCESS in %v", event.JobID, elapsed.Round(time.Millisecond))
	return nil
}

func (w *Worker) execute(ctx context.Context, job *config.Job, event *config.JobEvent) error {
	// In production, dispatch based on a "type" field in the payload.
	// For this example we call a generic handler.
	handler, ok := w.handlers["default"]
	if !ok {
		return fmt.Errorf("no handler registered for job type")
	}
	return handler(ctx, event.Payload)
}

// createExecution inserts a new execution record and returns its ID.
// Uses ON CONFLICT DO NOTHING on the unique (job_id, scheduled_epoch) key
// to make this safe to call multiple times for the same event.
func (w *Worker) createExecution(
	ctx context.Context,
	pool *pgxpool.Pool,
	event *config.JobEvent,
) (executionID string, alreadyExists bool, err error) {

	var id string
	err = pool.QueryRow(ctx, `
		INSERT INTO job_executions
		    (job_id, scheduled_epoch, worker_id, status, started_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (job_id, scheduled_epoch) DO NOTHING
		RETURNING execution_id
	`, event.JobID, event.ScheduledEpoch, w.workerID, StatusRunning).Scan(&id)

	if err != nil {
		// No rows returned means ON CONFLICT triggered — already processed
		if id == "" {
			return "", true, nil
		}
		return "", false, err
	}
	return id, false, nil
}

func (w *Worker) markStatus(
	ctx context.Context,
	pool *pgxpool.Pool,
	executionID string,
	shardID int,
	status ExecutionStatus,
	errMsg string,
	duration time.Duration,
) {
	bg := context.Background() // use background ctx — parent may be cancelled
	_, err := pool.Exec(bg, `
		UPDATE job_executions
		SET status=$1, error_message=$2, finished_at=NOW(), duration_ms=$3
		WHERE execution_id=$4
	`, status, errMsg, duration.Milliseconds(), executionID)
	if err != nil {
		log.Printf("[worker] markStatus failed: %v", err)
	}
}

func (w *Worker) markFailed(ctx context.Context, pool *pgxpool.Pool, executionID string, shardID int, msg string) {
	w.markStatus(ctx, pool, executionID, shardID, StatusFailed, msg, 0)
}

func (w *Worker) fetchJob(ctx context.Context, pool *pgxpool.Pool, jobID string) (*config.Job, error) {
	var job config.Job
	err := pool.QueryRow(ctx, `
		SELECT job_id, tenant_id, shard_id, cron_expr, payload,
		       next_fire_at, timezone, max_retries, timeout_secs
		FROM jobs WHERE job_id=$1
	`, jobID).Scan(
		&job.JobID, &job.TenantID, &job.ShardID, &job.CronExpr,
		&job.Payload, &job.NextFireAt, &job.TimeZone,
		&job.MaxRetries, &job.TimeoutSecs,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch job %s: %w", jobID, err)
	}
	return &job, nil
}

func (w *Worker) routeEvent(ctx context.Context, event *config.JobEvent) (*pgxpool.Pool, int, error) {
	pool, shardID, err := w.router.PoolForTenant(event.TenantID)
	if err != nil {
		return nil, 0, fmt.Errorf("no pool for tenant %s: %w", event.TenantID, err)
	}
	return pool, shardID, nil
}
