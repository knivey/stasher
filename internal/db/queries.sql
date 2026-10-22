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

-- name: CountAdmins :one
-- enabled admins only: a disabled admin cannot manage the instance, so
-- they must not count towards the last-admin guard
SELECT COUNT(*) FROM users WHERE is_admin = 1 AND disabled = 0;

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

-- name: UpdateTag :execrows
UPDATE tags SET name = ? WHERE id = ? AND user_id = ?;

-- name: DeleteTag :execrows
DELETE FROM tags WHERE id = ? AND user_id = ?;

-- name: CreateItem :one
INSERT INTO items (user_id, name, description, qty, unit, location_id, visibility, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: CreateItemWithID :one
-- restore path: an explicit id guarantees the restored item never reuses
-- the trashed item's id (SQLite recycles the max rowid after a delete)
INSERT INTO items (id, user_id, name, description, qty, unit, location_id, visibility, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: MaxItemID :one
SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) FROM items;

-- name: GetItem :one
SELECT * FROM items WHERE id = ? AND user_id = ?;

-- name: GetItemAnyOwner :one
SELECT * FROM items WHERE id = ?;

-- name: UpdateItem :exec
UPDATE items SET name = ?, description = ?, qty = ?, unit = ?, location_id = ?, visibility = ?, disposed = ?, updated_at = ?
WHERE id = ? AND user_id = ?;

-- name: DeleteItem :exec
DELETE FROM items WHERE id = ? AND user_id = ?;

-- name: GetItemTags :many
SELECT t.id, t.name FROM tags t JOIN item_tags it ON it.tag_id = t.id WHERE it.item_id = ?;

-- name: ClearItemTags :exec
DELETE FROM item_tags WHERE item_id = ?;

-- name: AttachItemTag :exec
INSERT OR IGNORE INTO item_tags (item_id, tag_id) VALUES (?, ?);

-- name: ListItemPhotos :many
SELECT * FROM item_photos WHERE item_id = ? ORDER BY sort_order, id;

-- name: ListItemDocuments :many
SELECT * FROM item_documents WHERE item_id = ? ORDER BY filename;

-- ListItems is intentionally NOT a sqlc query: ORDER BY must support the
-- ?sort=/?order= request params (final-review Finding 1), and identifiers
-- cannot be bound as placeholder args. The handler (handleListItems) builds
-- the statement via buildItemListSQL with a strict identifier whitelist;
-- all values remain bound args. Filter semantics live there.

-- name: ListSharedItemsFromOthers :many
SELECT i.id, i.name, i.description, i.qty, i.unit, i.location_id, i.visibility,
       i.created_at, i.updated_at, i.disposed AS disposed, u.display_name AS owner_name,
       (SELECT COUNT(*) FROM item_photos p WHERE p.item_id = i.id) AS photo_count,
       tp.storage_key AS thumb_key,
       count(*) OVER () AS total_count
FROM items i
JOIN users u ON u.id = i.user_id
-- thumb_key: the item's first photo (lowest sort_order, then id) via a LEFT
-- JOIN onto the subquery-picked row, so sqlc emits a nullable sql.NullString
-- (a bare scalar subquery is inferred NOT NULL and would fail to Scan the
-- no-photo NULL). Join is on the photo primary key: at most one row, so
-- multiplicity and total_count are unchanged.
LEFT JOIN item_photos tp ON tp.id = (
  SELECT p.id FROM item_photos p WHERE p.item_id = i.id ORDER BY p.sort_order, p.id LIMIT 1
)
WHERE i.user_id != @user_id AND i.visibility = 'shared'
  AND (sqlc.narg('q') IS NULL OR i.id IN (SELECT rowid FROM items_fts WHERE items_fts MATCH @q))
ORDER BY i.updated_at DESC
LIMIT @lim OFFSET @off;

-- name: AddItemPhoto :one
INSERT INTO item_photos (item_id, storage_key, thumb_key, caption, sort_order, created_at)
VALUES (?, ?, ?, ?, ?, ?) RETURNING *;

-- name: UpdateItemPhoto :exec
UPDATE item_photos SET caption = ?, sort_order = ? WHERE id = ? AND item_id = ?;

-- name: DeleteItemPhoto :exec
DELETE FROM item_photos WHERE id = ? AND item_id = ?;

-- name: SetItemPhotoThumb :exec
UPDATE item_photos SET thumb_key = ? WHERE id = ?;

-- name: ListPhotosMissingThumbs :many
SELECT * FROM item_photos WHERE thumb_key IS NULL ORDER BY id;

-- name: GetPhotoByKey :one
SELECT p.id AS photo_id, p.storage_key, p.caption, p.sort_order,
       i.user_id, i.visibility
FROM item_photos p JOIN items i ON i.id = p.item_id
WHERE p.storage_key = sqlc.arg('key') OR p.thumb_key = sqlc.arg('key');

-- name: AddItemDocument :one
INSERT INTO item_documents (item_id, storage_key, filename, mime_type, size_bytes, created_at)
VALUES (?, ?, ?, ?, ?, ?) RETURNING *;

-- name: DeleteItemDocument :exec
DELETE FROM item_documents WHERE id = ? AND item_id = ?;

-- name: GetDocumentByKey :one
SELECT d.id AS document_id, d.storage_key, d.filename, d.mime_type,
       i.user_id, i.visibility
FROM item_documents d JOIN items i ON i.id = d.item_id
WHERE d.storage_key = ?;

-- name: CreateProject :one
INSERT INTO projects (user_id, name, status, notes, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetProject :one
SELECT * FROM projects WHERE id = ? AND user_id = ?;

-- name: ListProjects :many
SELECT * FROM projects WHERE user_id = ? ORDER BY updated_at DESC;

-- name: UpdateProject :exec
UPDATE projects SET name = ?, status = ?, notes = ?, updated_at = ? WHERE id = ? AND user_id = ?;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = ? AND user_id = ?;

-- name: UpsertProjectItem :exec
INSERT INTO project_items (project_id, item_id, qty_used, notes, added_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (project_id, item_id) DO UPDATE SET qty_used = excluded.qty_used, notes = excluded.notes;

-- name: DeleteProjectItem :execrows
-- execrows so the handler can 404 on absent rows (tags' post-fix semantics)
DELETE FROM project_items WHERE project_id = ? AND item_id = ?;

-- name: ListProjectItems :many
SELECT pi.project_id, pi.item_id, pi.qty_used, pi.notes, pi.added_at,
       i.name AS item_name, i.qty AS item_qty, i.unit AS item_unit
FROM project_items pi JOIN items i ON i.id = pi.item_id
WHERE pi.project_id = ?;

-- name: GetItemProjects :many
SELECT p.id, p.name, p.status, pi.qty_used
FROM project_items pi JOIN projects p ON p.id = pi.project_id
WHERE pi.item_id = ?;

-- name: CreateItemLedger :one
INSERT INTO item_ledger (item_id, user_id, delta, resulting_qty, kind, project_id, note, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListItemLedger :many
SELECT l.*, count(*) OVER () AS total_count
FROM item_ledger l
WHERE l.item_id = ? AND l.user_id = ?
ORDER BY l.id DESC
LIMIT ? OFFSET ?;

-- name: ListItemLedgerAll :many
-- Full ledger for one item, oldest first (trash snapshots capture the
-- complete history in original order; ListItemLedger paginates DESC).
SELECT * FROM item_ledger WHERE item_id = ? AND user_id = ? ORDER BY id ASC;

-- name: ListItemProjectLinks :many
-- project_items rows for one item (trash snapshot; ListProjectItems is
-- keyed by project_id, the other direction).
SELECT project_id, qty_used, notes, added_at
FROM project_items WHERE item_id = ? ORDER BY project_id;

-- name: CreateTrashEntry :one
INSERT INTO trash (user_id, snapshot, trashed_at, purges_at)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: ListTrash :many
SELECT id, snapshot, trashed_at, purges_at, count(*) OVER () AS total_count
FROM trash WHERE user_id = ?
ORDER BY trashed_at DESC
LIMIT ? OFFSET ?;

-- name: GetTrashEntry :one
SELECT * FROM trash WHERE id = ? AND user_id = ?;

-- name: DeleteTrashEntry :exec
DELETE FROM trash WHERE id = ? AND user_id = ?;

-- name: DeleteAllUserTrash :many
-- rows come back so callers can delete the snapshot's files afterwards
DELETE FROM trash WHERE user_id = ? RETURNING snapshot;

-- name: DeleteExpiredTrash :many
-- purges_at is RFC3339 UTC text ('' = never purge), so lexicographic
-- comparison with the now-argument is a correct time comparison
DELETE FROM trash WHERE purges_at != '' AND purges_at < ? RETURNING snapshot;
