-- name: EnqueueJob :one
INSERT INTO jobs (kind, priority, payload, dedupe_key)
VALUES (@kind, @priority, @payload, sqlc.narg('dedupe_key'))
ON CONFLICT (dedupe_key) DO UPDATE SET dedupe_key = EXCLUDED.dedupe_key
RETURNING id;

-- name: LeaseJob :one
UPDATE jobs SET
    lease_token = gen_random_uuid(),
    leased_until = now() + make_interval(secs => @ttl_seconds::float8),
    attempts = attempts + 1
WHERE id = (
    SELECT j.id FROM jobs j
    WHERE j.kind = ANY (@kinds::text[])
      AND j.available_at <= now()
      AND (j.leased_until IS NULL OR j.leased_until < now())
    ORDER BY j.priority DESC, j.id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: HeartbeatJob :one
UPDATE jobs SET leased_until = now() + make_interval(secs => @ttl_seconds::float8)
WHERE id = @id AND lease_token = @lease_token
RETURNING leased_until;

-- name: AckJob :execrows
DELETE FROM jobs WHERE id = @id AND lease_token = @lease_token;

-- name: NackJob :execrows
UPDATE jobs SET
    lease_token = NULL,
    leased_until = NULL,
    available_at = now() + make_interval(secs => @delay_seconds::float8),
    last_error = sqlc.narg('last_error')
WHERE id = @id AND lease_token = @lease_token;

-- name: CountJobs :many
SELECT kind, count(*) AS ready FROM jobs
WHERE available_at <= now() AND (leased_until IS NULL OR leased_until < now())
GROUP BY kind;
