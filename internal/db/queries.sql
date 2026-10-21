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

-- name: CreateLocation :one
INSERT INTO locations (user_id, name, parent_id, notes, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetLocation :one
SELECT * FROM locations WHERE id = ? AND user_id = ?;

-- name: ListLocations :many
SELECT * FROM locations WHERE user_id = ? ORDER BY name;

-- name: UpdateLocation :exec
UPDATE locations SET name = ?, notes = ?, parent_id = ?, updated_at = ? WHERE id = ? AND user_id = ?;

-- name: DeleteLocation :exec
DELETE FROM locations WHERE id = ? AND user_id = ?;

-- name: ReparentChildren :exec
UPDATE locations SET parent_id = ? WHERE parent_id = ? AND user_id = ?;

-- name: ClearItemsLocation :exec
UPDATE items SET location_id = NULL, updated_at = ? WHERE location_id = ? AND user_id = ?;

-- name: LocationDescendants :many
-- CTE named "d" not "desc": DESC is a reserved SQLite keyword that breaks sqlc's
-- analyzer; anchor columns are table-qualified for the same reason.
WITH RECURSIVE d(id) AS (
    SELECT locations.id FROM locations WHERE locations.id = ? AND locations.user_id = ?
    UNION ALL
    SELECT l.id FROM locations l JOIN d ON l.parent_id = d.id WHERE l.user_id = ?
) SELECT id FROM d;

-- name: LocationBreadcrumbs :many
WITH RECURSIVE up(id, name, parent_id, depth) AS (
    SELECT locations.id, locations.name, locations.parent_id, 0 AS depth
    FROM locations WHERE locations.id = ? AND locations.user_id = ?
    UNION ALL
    SELECT l.id, l.name, l.parent_id, up.depth + 1
    FROM locations l JOIN up ON l.id = up.parent_id WHERE l.user_id = ?
) SELECT id, name, depth FROM up ORDER BY depth DESC;

-- name: GetLocationByName :one
-- NULL-safe parent comparison ("IS ?") so duplicates under the same parent
-- (including the root, NULL) are detectable - spec: app-logic uniqueness.
-- (Comment uses ASCII "-" not an em dash: sqlc v1.31.1 mis-handles multi-byte
-- UTF-8 inside comments and corrupts its rewritten query text.)
SELECT * FROM locations WHERE user_id = ? AND name = ? AND parent_id IS ?;

-- name: CreateTag :one
INSERT INTO tags (user_id, name) VALUES (?, ?) RETURNING *;

-- name: GetTagByName :one
SELECT * FROM tags WHERE user_id = ? AND name = ?;

-- name: ListTagsWithCounts :many
SELECT t.id, t.name, COUNT(it.item_id) AS item_count
FROM tags t LEFT JOIN item_tags it ON it.tag_id = t.id
WHERE t.user_id = ?
GROUP BY t.id, t.name
ORDER BY t.name;

-- name: UpdateTag :exec
UPDATE tags SET name = ? WHERE id = ? AND user_id = ?;

-- name: DeleteTag :exec
DELETE FROM tags WHERE id = ? AND user_id = ?;
