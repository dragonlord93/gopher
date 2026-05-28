-- Run this on every Postgres shard instance.
-- The schema is identical across all shards — routing is handled by the app layer.

-- ─── Jobs ────────────────────────────────────────────────────────────────────
-- Each row is one recurring cron job.
-- next_fire_at is the ONLY column the scheduler queries at steady state.
CREATE TABLE jobs (
    job_id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       TEXT        NOT NULL,
    shard_id        INT         NOT NULL,        -- denormalised for debugging; not used in queries
    cron_expr       TEXT        NOT NULL,         -- standard 5-field cron e.g. "0 6 * * *"
    payload         JSONB       NOT NULL DEFAULT '{}',
    next_fire_at    TIMESTAMPTZ NOT NULL,
    last_fire_at    TIMESTAMPTZ,
    timezone        TEXT        NOT NULL DEFAULT 'UTC',
    max_retries     INT         NOT NULL DEFAULT 3,
    timeout_secs    INT         NOT NULL DEFAULT 30,
    enabled         BOOLEAN     NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The ONLY index the scheduler uses for refill queries.
-- Partial index on enabled=true means it covers fewer rows — faster seeks.
-- The scheduler query: WHERE next_fire_at < $window_end AND enabled = true
CREATE INDEX idx_jobs_next_fire
    ON jobs (next_fire_at)
    WHERE enabled = true;

-- Tenant lookup (for admin / API queries)
CREATE INDEX idx_jobs_tenant ON jobs (tenant_id);


-- ─── Job executions ──────────────────────────────────────────────────────────
-- Append-only log of every job run. Never updated in place except to mark completion.
-- The unique constraint on (job_id, scheduled_epoch) is the idempotency guard:
-- a worker trying to process the same event twice hits a conflict and skips.
CREATE TABLE job_executions (
    execution_id    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id          UUID        NOT NULL REFERENCES jobs(job_id),
    scheduled_epoch BIGINT      NOT NULL,    -- unix seconds of intended fire time
    worker_id       TEXT        NOT NULL,
    status          TEXT        NOT NULL,    -- running | success | failed | timeout
    error_message   TEXT,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at     TIMESTAMPTZ,
    duration_ms     BIGINT,

    -- This constraint is the idempotency key.
    -- Same job + same scheduled time = same logical execution.
    -- Even if the scheduler fires twice (failover race), only one worker wins the INSERT.
    UNIQUE (job_id, scheduled_epoch)
);

-- Lookup executions for a job (admin / monitoring)
CREATE INDEX idx_executions_job_id  ON job_executions (job_id, started_at DESC);
-- Find stuck running jobs (reaper query)
CREATE INDEX idx_executions_running ON job_executions (started_at)
    WHERE status = 'running';


-- ─── Reaper query ────────────────────────────────────────────────────────────
-- This query runs every minute from a separate reaper process.
-- It finds executions that started but never finished (worker crashed).
-- The reaper marks them failed and re-enqueues the job.
--
-- SELECT execution_id, job_id, worker_id, started_at
-- FROM job_executions
-- WHERE status = 'running'
--   AND started_at < NOW() - INTERVAL '2 minutes'  -- 2× expected max execution time
-- LIMIT 1000;


-- ─── Trigger: keep updated_at fresh ─────────────────────────────────────────
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER jobs_updated_at
    BEFORE UPDATE ON jobs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
