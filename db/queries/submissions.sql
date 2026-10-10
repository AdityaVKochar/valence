-- name: CreateSubmission :one
INSERT INTO submissions (user_id, problem_id, problem_revision, language, source)
VALUES (@user_id, @problem_id, @problem_revision, @language, @source)
RETURNING *;

-- name: CreateAttempt :exec
INSERT INTO submission_attempts (submission_id, attempt) VALUES (@submission_id, @attempt);

-- name: GetSubmission :one
SELECT sqlc.embed(s), p.slug AS problem_slug, p.title AS problem_title, u.handle AS user_handle
FROM submissions s
JOIN problems p ON p.id = s.problem_id
JOIN users u ON u.id = s.user_id
WHERE s.id = @id;

-- name: ListUserSubmissions :many
SELECT sqlc.embed(s), p.slug AS problem_slug, p.title AS problem_title, u.handle AS user_handle
FROM submissions s
JOIN problems p ON p.id = s.problem_id
JOIN users u ON u.id = s.user_id
WHERE s.user_id = @user_id
  AND (sqlc.narg('problem_id')::bigint IS NULL OR s.problem_id = sqlc.narg('problem_id'))
  AND s.id < @before_id
ORDER BY s.id DESC
LIMIT @page_size;

-- name: CountActiveSubmissions :one
SELECT count(*) FROM submissions WHERE user_id = @user_id AND status <> 'finalized';

-- name: GetAttempt :one
SELECT * FROM submission_attempts WHERE submission_id = @submission_id AND attempt = @attempt;

-- name: ListTestResults :many
SELECT * FROM test_results WHERE submission_id = @submission_id AND attempt = @attempt ORDER BY ordinal;

-- name: GetJudgeJob :one
SELECT s.id, s.attempt AS current_attempt, s.language, s.source, s.status,
       p.id AS problem_id, p.time_limit_ms, p.memory_limit_kib, p.checker,
       a.status AS attempt_status
FROM submissions s
JOIN problems p ON p.id = s.problem_id
JOIN submission_attempts a ON a.submission_id = s.id AND a.attempt = @attempt
WHERE s.id = @submission_id;

-- name: SetSubmissionProgress :execrows
UPDATE submissions SET status = @status, current_test = sqlc.narg('current_test')
WHERE id = @id AND attempt = @attempt AND status <> 'finalized';

-- name: SetAttemptProgress :execrows
UPDATE submission_attempts SET
    status = @status,
    started_at = coalesce(started_at, now()),
    provider = coalesce(sqlc.narg('provider'), provider),
    worker = coalesce(sqlc.narg('worker'), worker)
WHERE submission_id = @submission_id AND attempt = @attempt AND status <> 'finalized';

-- name: InsertTestResult :exec
INSERT INTO test_results (submission_id, attempt, ordinal, verdict, time_ms, memory_kib)
VALUES (@submission_id, @attempt, @ordinal, @verdict, @time_ms, @memory_kib)
ON CONFLICT DO NOTHING;

-- name: FinalizeAttempt :execrows
UPDATE submission_attempts SET
    status = 'finalized',
    verdict = @verdict,
    time_ms = @time_ms,
    memory_kib = @memory_kib,
    compile_output = @compile_output,
    provider = @provider,
    worker = @worker,
    infra_retries = @infra_retries,
    last_error = @last_error,
    started_at = coalesce(started_at, now()),
    finished_at = now()
WHERE submission_id = @submission_id AND attempt = @attempt AND status <> 'finalized';

-- name: FinalizeSubmission :execrows
UPDATE submissions SET
    status = 'finalized',
    verdict = @verdict,
    time_ms = @time_ms,
    memory_kib = @memory_kib,
    current_test = NULL,
    judged_at = now()
WHERE id = @id AND attempt = @attempt AND status <> 'finalized';

-- name: LockAttempt :one
SELECT * FROM submission_attempts WHERE submission_id = @submission_id AND attempt = @attempt FOR UPDATE;

-- name: LockSubmission :one
SELECT * FROM submissions WHERE id = @id FOR UPDATE;
