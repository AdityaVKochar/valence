-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByHandle :one
SELECT * FROM users WHERE lower(handle) = lower(@handle::text);

-- name: GetUserByIdentity :one
SELECT u.* FROM users u
JOIN identities i ON i.user_id = u.id
WHERE i.provider = @provider AND i.subject = @subject;

-- name: HandleTaken :one
SELECT EXISTS (SELECT 1 FROM users WHERE lower(handle) = lower(@handle::text));

-- name: CreateUser :one
INSERT INTO users (handle, display_name, email, avatar_url, role)
VALUES (@handle, @display_name, @email, @avatar_url, @role)
RETURNING *;

-- name: CreateIdentity :exec
INSERT INTO identities (provider, subject, user_id, email)
VALUES (@provider, @subject, @user_id, @email);

-- name: TouchIdentity :exec
UPDATE identities SET last_used_at = now(), email = @email
WHERE provider = @provider AND subject = @subject;

-- name: RecordLogin :one
UPDATE users SET
    last_login_at = now(),
    avatar_url = coalesce(sqlc.narg('avatar_url'), avatar_url),
    email = coalesce(email, sqlc.narg('email'))
WHERE id = @id
RETURNING *;

-- name: SetUserRole :one
UPDATE users SET role = @role WHERE id = @id RETURNING *;
