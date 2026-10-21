-- name: CreateUser :one
INSERT INTO users (email, password_hash, display_name, default_item_visibility, is_admin, disabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 0, ?, ?)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: UpdateUserProfile :exec
UPDATE users SET display_name = ?, default_item_visibility = ?, updated_at = ? WHERE id = ?;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?;

-- name: SetUserAdmin :exec
UPDATE users SET is_admin = ?, updated_at = ? WHERE id = ?;

-- name: SetUserDisabled :exec
UPDATE users SET disabled = ?, updated_at = ? WHERE id = ?;

-- name: ListUsers :many
SELECT id, email, display_name, default_item_visibility, is_admin, disabled, created_at, updated_at FROM users ORDER BY id;

-- name: CreateSession :one
INSERT INTO sessions (token_hash, user_id, user_agent, last_ip, created_at, last_seen_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetSessionWithUser :one
SELECT s.id AS session_id, s.token_hash, s.last_seen_at,
       u.id AS user_id, u.email, u.password_hash, u.display_name,
       u.default_item_visibility, u.is_admin, u.disabled
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = ?;

-- name: GetSessionByID :one
SELECT * FROM sessions WHERE id = ? AND user_id = ?;

-- name: ListUserSessions :many
SELECT * FROM sessions WHERE user_id = ? ORDER BY last_seen_at DESC;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ? AND user_id = ?;

-- name: DeleteOtherUserSessions :exec
DELETE FROM sessions WHERE user_id = ? AND id != ?;

-- name: DeleteAllUserSessions :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ? WHERE id = ?;

-- name: CreateInvite :one
INSERT INTO invites (code, created_by, created_at) VALUES (?, ?, ?) RETURNING *;

-- name: GetInvite :one
SELECT * FROM invites WHERE code = ?;

-- name: ListInvites :many
SELECT i.id, i.code, i.created_at, i.used_by, i.used_at, u.email AS used_by_email
FROM invites i LEFT JOIN users u ON u.id = i.used_by ORDER BY i.id DESC;

-- name: MarkInviteUsed :exec
UPDATE invites SET used_by = ?, used_at = ? WHERE code = ?;

-- name: DeleteInvite :exec
DELETE FROM invites WHERE id = ?;
