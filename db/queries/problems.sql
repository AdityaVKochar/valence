-- name: ListProblems :many
SELECT id, slug, title, time_limit_ms, memory_limit_kib, visibility
FROM problems
WHERE id > @after_id
  AND (@include_private::boolean OR visibility = 'public')
ORDER BY id
LIMIT @page_size;

-- name: GetProblemBySlug :one
SELECT * FROM problems WHERE slug = @slug;

-- name: GetProblem :one
SELECT * FROM problems WHERE id = @id;

-- name: CreateProblem :one
INSERT INTO problems (slug, title, statement_md, time_limit_ms, memory_limit_kib, checker, visibility, created_by)
VALUES (@slug, @title, @statement_md, @time_limit_ms, @memory_limit_kib, @checker, @visibility, @created_by)
RETURNING *;

-- name: UpdateProblem :one
UPDATE problems SET
    title = @title,
    statement_md = @statement_md,
    time_limit_ms = @time_limit_ms,
    memory_limit_kib = @memory_limit_kib,
    checker = @checker,
    visibility = @visibility,
    revision = revision + 1
WHERE id = @id
RETURNING *;

-- name: ListTests :many
SELECT * FROM tests WHERE problem_id = @problem_id ORDER BY ordinal;

-- name: ListSampleTests :many
SELECT * FROM tests WHERE problem_id = @problem_id AND is_sample ORDER BY ordinal;

-- name: DeleteTests :exec
DELETE FROM tests WHERE problem_id = @problem_id;

-- name: InsertTest :exec
INSERT INTO tests (problem_id, ordinal, input_hash, input_size, output_hash, output_size, is_sample)
VALUES (@problem_id, @ordinal, @input_hash, @input_size, @output_hash, @output_size, @is_sample);

-- name: ListSolvedProblemIDs :many
SELECT DISTINCT problem_id FROM submissions
WHERE user_id = @user_id AND verdict = 'AC' AND problem_id = ANY (@problem_ids::bigint[]);

-- name: CountTests :one
SELECT count(*) FROM tests WHERE problem_id = @problem_id;
