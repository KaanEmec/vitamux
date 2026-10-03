-- Job queue (J06.1); see docs/adr/0003-postgres-job-queue.md. Every update of a running job is
-- fenced by lease_owner and attempts, so a worker whose lease was reaped changes nothing.

-- name: InsertJob :one
INSERT INTO jobs (id, kind, connection_id, exclusive, priority, run_at, dedupe_key, payload, max_attempts)
VALUES (@id, @kind, @connection_id, @exclusive, @priority, coalesce(sqlc.narg(run_at)::timestamptz, now()),
        @dedupe_key, @payload, @max_attempts)
ON CONFLICT (dedupe_key) WHERE status IN ('queued', 'running') DO NOTHING
RETURNING id;

-- name: GetActiveJobID :one
SELECT id FROM jobs WHERE dedupe_key = @dedupe_key AND status IN ('queued', 'running');

-- name: NotifyJobs :exec
SELECT pg_notify(@channel::text, '');

-- name: ClaimJob :one
WITH next AS (
  SELECT j.id FROM jobs j
  WHERE j.status = 'queued' AND j.run_at <= now() AND j.kind = ANY(@kinds::text[])
    AND NOT (j.exclusive AND EXISTS (SELECT 1 FROM jobs r
             WHERE r.connection_id = j.connection_id AND r.status = 'running' AND r.exclusive))
  ORDER BY j.priority, j.run_at
  FOR UPDATE SKIP LOCKED LIMIT 1)
UPDATE jobs SET status = 'running', lease_owner = @owner::text, lease_expires_at = now() + @lease::interval,
       attempts = attempts + 1, started_at = now()
FROM next WHERE jobs.id = next.id
RETURNING jobs.*;

-- name: InsertJobRun :one
INSERT INTO job_runs (job_id, attempt) VALUES (@job_id, @attempt) RETURNING id;

-- name: HeartbeatJob :execrows
UPDATE jobs SET lease_expires_at = now() + @lease::interval
WHERE id = @id AND status = 'running' AND lease_owner = @owner::text AND attempts = @attempt;

-- name: SaveJobCheckpoint :execrows
UPDATE jobs SET checkpoint = @checkpoint::jsonb, lease_expires_at = now() + @lease::interval
WHERE id = @id AND status = 'running' AND lease_owner = @owner::text AND attempts = @attempt;

-- name: CompleteJob :execrows
UPDATE jobs SET status = 'succeeded', finished_at = now(), lease_owner = NULL, lease_expires_at = NULL
WHERE id = @id AND status = 'running' AND lease_owner = @owner::text AND attempts = @attempt;

-- name: RetryJob :execrows
UPDATE jobs SET status = 'queued', run_at = now() + @delay::interval, lease_owner = NULL, lease_expires_at = NULL
WHERE id = @id AND status = 'running' AND lease_owner = @owner::text AND attempts = @attempt;

-- name: RequeueJob :execrows
-- Gives the attempt back: used for reschedules (rate limits) and shutdown releases.
UPDATE jobs SET status = 'queued', attempts = attempts - 1, lease_owner = NULL, lease_expires_at = NULL,
       run_at = greatest(now(), coalesce(sqlc.narg(run_at)::timestamptz, now()))
WHERE id = @id AND status = 'running' AND lease_owner = @owner::text AND attempts = @attempt;

-- name: KillJob :execrows
UPDATE jobs SET status = 'dead', finished_at = now(), lease_owner = NULL, lease_expires_at = NULL
WHERE id = @id AND status = 'running' AND lease_owner = @owner::text AND attempts = @attempt;

-- name: FinishJobRun :exec
UPDATE job_runs SET finished_at = now(), outcome = @outcome::text, error_class = sqlc.narg(error_class),
       error_message = sqlc.narg(error_message)
WHERE id = @id AND outcome IS NULL;

-- name: ReapExpiredJobs :many
-- The expired attempt stays counted; a job out of attempts becomes dead.
UPDATE jobs SET status = CASE WHEN attempts >= max_attempts THEN 'dead' ELSE 'queued' END,
       finished_at = CASE WHEN attempts >= max_attempts THEN now() END,
       run_at = now(), lease_owner = NULL, lease_expires_at = NULL
WHERE status = 'running' AND lease_expires_at < now()
RETURNING id, kind, connection_id, attempts, status;

-- name: ReleaseOwnerJobs :many
UPDATE jobs SET status = 'queued', attempts = attempts - 1, run_at = now(), lease_owner = NULL, lease_expires_at = NULL
WHERE status = 'running' AND lease_owner = @owner::text
RETURNING id;

-- name: CloseOpenJobRuns :exec
UPDATE job_runs SET finished_at = now(), outcome = @outcome::text, error_class = @error_class::text
WHERE job_id = ANY(@job_ids::uuid[]) AND outcome IS NULL;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = @id;

-- name: ListJobRuns :many
SELECT * FROM job_runs WHERE job_id = @job_id ORDER BY id;
