-- name: CreateSession :exec
INSERT INTO sessions (id_hash, user_id, expires_at, user_agent, ip)
VALUES (@id_hash, @user_id, @expires_at, @user_agent, @ip);

-- name: GetSession :one
SELECT * FROM sessions WHERE id_hash = @id_hash AND expires_at > now();

-- name: TouchSession :exec
UPDATE sessions SET expires_at = @expires_at WHERE id_hash = @id_hash;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id_hash = @id_hash;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = @user_id;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= now();
