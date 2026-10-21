package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

type itemReq struct {
	Name        string   `json:"name"`
	Description *string  `json:"description"`
	Qty         *float64 `json:"qty"`
	Unit        *string  `json:"unit"`
	LocationID  *int64   `json:"location_id"`
	Visibility  *string  `json:"visibility"`
	Tags        []string `json:"tags"`
}

func itemJSON(i dbpkg.Item) map[string]any {
	var loc any
	if i.LocationID.Valid {
		loc = i.LocationID.Int64
	}
	return map[string]any{
		"id": i.ID, "name": i.Name, "description": i.Description,
		"qty": i.Qty, "unit": i.Unit, "location_id": loc,
		"visibility": i.Visibility, "disposed": i.Disposed != 0,
		"created_at": i.CreatedAt, "updated_at": i.UpdatedAt,
	}
}

// itemDetailJSON assembles the full item response (tags, photos, docs, projects).
func (s *Server) itemDetailJSON(r *http.Request, i dbpkg.Item) (map[string]any, error) {
	out := itemJSON(i)
	tags, err := s.q.GetItemTags(r.Context(), i.ID)
	if err != nil {
		return nil, err
	}
	tagList := make([]map[string]any, 0, len(tags))
	for _, tg := range tags {
		tagList = append(tagList, map[string]any{"id": tg.ID, "name": tg.Name})
	}
	photos, err := s.q.ListItemPhotos(r.Context(), i.ID)
	if err != nil {
		return nil, err
	}
	photoList := make([]map[string]any, 0, len(photos))
	for _, p := range photos {
		photoList = append(photoList, map[string]any{
			"id": p.ID, "storage_key": p.StorageKey, "caption": p.Caption,
			"sort_order": p.SortOrder, "created_at": p.CreatedAt,
		})
	}
	docs, err := s.q.ListItemDocuments(r.Context(), i.ID)
	if err != nil {
		return nil, err
	}
	docList := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		docList = append(docList, map[string]any{
			"id": d.ID, "storage_key": d.StorageKey, "filename": d.Filename,
			"mime_type": d.MimeType, "size_bytes": d.SizeBytes, "created_at": d.CreatedAt,
		})
	}
	out["tags"] = tagList
	out["photos"] = photoList
	out["documents"] = docList
	projects, err := s.q.GetItemProjects(r.Context(), i.ID)
	if err != nil {
		return nil, err
	}
	projList := make([]map[string]any, 0, len(projects))
	for _, pj := range projects {
		var qtyUsed any
		if pj.QtyUsed.Valid {
			qtyUsed = pj.QtyUsed.Float64
		}
		projList = append(projList, map[string]any{
			"id": pj.ID, "name": pj.Name, "status": pj.Status, "qty_used": qtyUsed,
		})
	}
	out["projects"] = projList
	return out, nil
}

// setItemTags replaces the item's tags with the given names (creating tags
// as needed). Handlers validate via validateTagNames BEFORE any write; the
// checks below stay as a defensive backstop for other callers.
func (s *Server) setItemTags(r *http.Request, u dbpkg.User, itemID int64, names []string) error {
	if len(names) > 50 {
		return errTooManyTags
	}
	tx, err := s.raw.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	qtx := dbpkg.New(tx)
	if err := qtx.ClearItemTags(r.Context(), itemID); err != nil {
		return err
	}
	for _, name := range names {
		if name == "" || len(name) > 100 {
			return errBadTagName
		}
		tg, err := qtx.GetTagByName(r.Context(), dbpkg.GetTagByNameParams{UserID: u.ID, Name: name})
		if err != nil {
			tg, err = qtx.CreateTag(r.Context(), dbpkg.CreateTagParams{UserID: u.ID, Name: name})
			if err != nil {
				return err
			}
		}
		if err := qtx.AttachItemTag(r.Context(), dbpkg.AttachItemTagParams{ItemID: itemID, TagID: tg.ID}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

var errTooManyTags = errors.New("too many tags")
var errBadTagName = errors.New("bad tag name")

// validateTagNames enforces the tag limits BEFORE any DB write so an
// invalid list can never leave a half-applied change behind (a ghost item
// row on create, or sibling fields already written on update).
func validateTagNames(names []string) error {
	if len(names) > 50 {
		return errTooManyTags
	}
	for _, n := range names {
		if n == "" || len(n) > 100 {
			return errBadTagName
		}
	}
	return nil
}

func (s *Server) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req itemReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || len(req.Name) > 200 {
		writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
		return
	}
	qty := 1.0
	if req.Qty != nil {
		if *req.Qty < 0 || *req.Qty > 1e12 {
			writeErr(w, http.StatusBadRequest, "validation_error", "qty must be between 0 and 1e12")
			return
		}
		qty = *req.Qty
	}
	visibility := u.DefaultItemVisibility
	if req.Visibility != nil {
		if *req.Visibility != "private" && *req.Visibility != "shared" {
			writeErr(w, http.StatusBadRequest, "validation_error", "visibility must be private or shared")
			return
		}
		visibility = *req.Visibility
	}
	// validate tags before ANY write (final-review Finding 2): otherwise a
	// bad list 400s only after the item row exists -> ghost item
	if req.Tags != nil {
		if err := validateTagNames(req.Tags); err != nil {
			s.tagErr(w, err)
			return
		}
	}
	var loc sql.NullInt64
	if req.LocationID != nil {
		if _, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: *req.LocationID, UserID: u.ID}); err != nil {
			writeErr(w, http.StatusNotFound, "not_found", "location not found")
			return
		}
		loc = sqlNullInt64(*req.LocationID)
	}
	desc := ""
	if req.Description != nil {
		desc = *req.Description
	}
	unit := ""
	if req.Unit != nil {
		unit = *req.Unit
	}
	now := time.Now().UTC().Format(time.RFC3339)
	item, err := s.q.CreateItem(r.Context(), dbpkg.CreateItemParams{
		UserID: u.ID, Name: req.Name, Description: desc, Qty: qty, Unit: unit,
		LocationID: loc, Visibility: visibility, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	// every item starts with a baseline ledger entry (delta=resulting=qty)
	if err := s.recordInitialLedger(r.Context(), item.ID, u.ID, qty, now); err != nil {
		s.internalErr(w, err)
		return
	}
	if req.Tags != nil {
		if err := s.setItemTags(r, u, item.ID, req.Tags); err != nil {
			s.tagErr(w, err)
			return
		}
	}
	detail, err := s.itemDetailJSON(r, item)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, detail)
}

func (s *Server) tagErr(w http.ResponseWriter, err error) {
	switch err {
	case errTooManyTags:
		writeErr(w, http.StatusBadRequest, "validation_error", "at most 50 tags per item")
	case errBadTagName:
		writeErr(w, http.StatusBadRequest, "validation_error", "tag names must be 1-100 chars")
	default:
		s.internalErr(w, err)
	}
}

func (s *Server) handleGetItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return
	}
	item, err := s.q.GetItem(r.Context(), dbpkg.GetItemParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	detail, err := s.itemDetailJSON(r, item)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleUpdateItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return
	}
	var req itemReq
	if !decodeJSON(w, r, &req) {
		return
	}
	// pure request validations run BEFORE any read or write (validate-before-
	// write invariant, final-review Finding 2); the item row itself is read
	// inside the write transaction below.
	if req.Name != "" && len(req.Name) > 200 {
		writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
		return
	}
	if req.Qty != nil {
		if *req.Qty < 0 || *req.Qty > 1e12 {
			writeErr(w, http.StatusBadRequest, "validation_error", "qty must be between 0 and 1e12")
			return
		}
	}
	if req.Visibility != nil {
		if *req.Visibility != "private" && *req.Visibility != "shared" {
			writeErr(w, http.StatusBadRequest, "validation_error", "visibility must be private or shared")
			return
		}
	}
	if req.Tags != nil {
		if err := validateTagNames(req.Tags); err != nil {
			s.tagErr(w, err)
			return
		}
	}
	if req.LocationID != nil && *req.LocationID != 0 { // 0 clears; existence check here
		if _, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: *req.LocationID, UserID: u.ID}); err != nil {
			writeErr(w, http.StatusNotFound, "not_found", "location not found")
			return
		}
	}

	// Read-modify-write inside ONE transaction: with the single-connection
	// pool, BeginTx serializes concurrent writers, so the row read here is
	// the committed predecessor of the update - a concurrent qty edit (PATCH
	// or usage) can no longer interleave and clobber the chain.
	tx, err := s.raw.BeginTx(r.Context(), nil)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	defer tx.Rollback()
	qtx := dbpkg.New(tx)
	item, err := qtx.GetItem(r.Context(), dbpkg.GetItemParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	oldQty := item.Qty
	if req.Name != "" { // absent = keep
		item.Name = req.Name
	}
	if req.Description != nil {
		item.Description = *req.Description
	}
	if req.Qty != nil {
		item.Qty = *req.Qty
	}
	if req.Unit != nil {
		item.Unit = *req.Unit
	}
	if req.Visibility != nil {
		item.Visibility = *req.Visibility
	}
	if req.LocationID != nil { // explicit 0 clears; absent keeps
		if *req.LocationID == 0 {
			item.LocationID = sql.NullInt64{}
		} else {
			item.LocationID = sqlNullInt64(*req.LocationID)
		}
	}
	// a qty change is logged to the ledger in the same transaction as the
	// update; other-field PATCHes write no entry. Any change resulting in
	// qty > 0 clears the disposed flag (same rule as the usage path); a
	// change landing on 0 leaves the flag unchanged.
	qtyChanged := req.Qty != nil && item.Qty != oldQty
	disposed := item.Disposed
	if qtyChanged && item.Qty > 0 {
		disposed = 0
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := qtx.UpdateItem(r.Context(), dbpkg.UpdateItemParams{
		Name: item.Name, Description: item.Description, Qty: item.Qty, Unit: item.Unit,
		LocationID: item.LocationID, Visibility: item.Visibility,
		Disposed: disposed, UpdatedAt: now,
		ID: id, UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	if qtyChanged {
		if _, err := insertLedgerEntry(r.Context(), qtx, dbpkg.CreateItemLedgerParams{
			ItemID: id, UserID: u.ID, Delta: item.Qty - oldQty, ResultingQty: item.Qty,
			Kind: "adjustment", Note: "", CreatedAt: now,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.internalErr(w, err)
		return
	}
	if req.Tags != nil {
		if err := s.setItemTags(r, u, id, req.Tags); err != nil {
			s.tagErr(w, err)
			return
		}
	}
	item, _ = s.q.GetItem(r.Context(), dbpkg.GetItemParams{ID: id, UserID: u.ID})
	detail, err := s.itemDetailJSON(r, item)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// itemSortColumns is the STRICT whitelist of ?sort= values to ORDER BY
// column expressions. The map values are the only strings that ever reach
// the SQL text as identifiers - raw request input is used solely as a map
// key, and an unknown key falls back to the default - so a request can
// never alter the statement structure, only pick one of four fixed
// columns. That is what makes the string interpolation in
// buildItemListSQL safe.
var itemSortColumns = map[string]string{
	"updated_at": "i.updated_at",
	"created_at": "i.created_at",
	"name":       "i.name",
	"qty":        "i.qty",
}

// itemOrderDirs is the direction whitelist: the only two fixed literals
// that may follow ORDER BY <column>.
var itemOrderDirs = map[string]string{
	"asc":  "ASC",
	"desc": "DESC",
}

// itemListRow mirrors the columns selected by buildItemListSQL (the shape
// of the former sqlc-generated ListItemsRow, now that the query is
// hand-built per-sort; i.user_id was never used and is not selected).
type itemListRow struct {
	ID          int64
	Name        string
	Description string
	Qty         float64
	Unit        string
	LocationID  sql.NullInt64
	Visibility  string
	Disposed    int64
	CreatedAt   string
	UpdatedAt   string
	PhotoCount  int64
	ThumbKey    sql.NullString
	TotalCount  int64
}

// itemFilters carries the GET /items filters. A nil optional field omits
// the predicate entirely - semantics identical to the old sqlc.narg
// "? IS NULL OR ..." form (NULL param = no filter).
type itemFilters struct {
	userID     int64
	q          *string // FTS match phrase (already transformed via ftsQuery)
	locIDsJSON *string // JSON array of location ids (self + descendants)
	tag        *string
	projectID  *int64
	visibility *string
	lim, off   int64
}

// buildItemListSQL assembles the GET /items statement and its bound args.
// Only the ORDER BY column and direction are interpolated (from the
// itemSortColumns/itemOrderDirs whitelists - see those for why that is
// injection-proof); EVERY value (user_id, q, loc_ids_json, tag,
// project_id, visibility, limit, offset) is a ? placeholder arg. WHERE
// semantics match the previous sqlc query exactly: FTS via items_fts
// MATCH, location subtree via json_each of the pre-resolved id array (R5
// fallback), tag/project/visibility filters, photo_count subquery, and
// count(*) OVER() AS total_count for the pre-pagination total.
func buildItemListSQL(f itemFilters, sortCol, dir string) (string, []any) {
	stmt := `SELECT i.id, i.name, i.description, i.qty, i.unit, i.location_id,
       i.visibility, i.disposed, i.created_at, i.updated_at,
       (SELECT COUNT(*) FROM item_photos p WHERE p.item_id = i.id) AS photo_count,
       (SELECT p.storage_key FROM item_photos p WHERE p.item_id = i.id ORDER BY p.sort_order, p.id LIMIT 1) AS thumb_key,
       count(*) OVER () AS total_count
FROM items i
WHERE i.user_id = ?`
	args := []any{f.userID}
	if f.q != nil {
		stmt += `
  AND i.id IN (SELECT rowid FROM items_fts WHERE items_fts MATCH ?)`
		args = append(args, *f.q)
	}
	if f.locIDsJSON != nil {
		stmt += `
  AND i.location_id IN (SELECT value FROM json_each(?))`
		args = append(args, *f.locIDsJSON)
	}
	if f.tag != nil {
		stmt += `
  AND i.id IN (
      SELECT it.item_id FROM item_tags it JOIN tags t ON t.id = it.tag_id
      WHERE t.name = ? AND t.user_id = ?)`
		args = append(args, *f.tag, f.userID)
	}
	if f.projectID != nil {
		stmt += `
  AND i.id IN (
      SELECT pi.item_id FROM project_items pi WHERE pi.project_id = ?)`
		args = append(args, *f.projectID)
	}
	if f.visibility != nil {
		stmt += `
  AND i.visibility = ?`
		args = append(args, *f.visibility)
	}
	stmt += `
ORDER BY ` + sortCol + ` ` + dir + `
LIMIT ? OFFSET ?`
	args = append(args, f.lim, f.off)
	return stmt, args
}

func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	q := r.URL.Query()
	p := parsePage(r)

	// ORDER BY identifiers can't be placeholder args, so they come from the
	// fixed whitelists only; unknown request values fall back to defaults.
	sortCol, ok := itemSortColumns[q.Get("sort")]
	if !ok {
		sortCol = itemSortColumns["updated_at"]
	}
	dir, ok := itemOrderDirs[q.Get("order")]
	if !ok {
		dir = itemOrderDirs["desc"]
	}

	f := itemFilters{userID: u.ID, lim: p.Limit(), off: p.Offset()}
	// guard the TRANSFORMED value: whitespace-only input tokenizes to zero
	// phrases ("" would make items_fts MATCH '' an fts5 syntax error -> 500)
	if raw := q.Get("q"); raw != "" {
		if fq := ftsQuery(raw); fq != "" {
			f.q = &fq
		}
	}
	if v := q.Get("location_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			// R5 fallback: sqlc can't embed the recursive CTE in IN(), so
			// resolve the subtree (self + descendants, user-scoped) here and
			// pass it as a JSON array for json_each().
			ids, err := s.q.LocationDescendants(r.Context(), dbpkg.LocationDescendantsParams{
				ID: id, UserID: u.ID, UserID_2: u.ID,
			})
			if err != nil {
				s.internalErr(w, err)
				return
			}
			b, err := json.Marshal(ids)
			if err != nil {
				s.internalErr(w, err)
				return
			}
			js := string(b)
			f.locIDsJSON = &js
		}
	}
	if v := q.Get("tag"); v != "" {
		f.tag = &v
	}
	if v := q.Get("project_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.projectID = &id
		}
	}
	if v := q.Get("visibility"); v == "private" || v == "shared" {
		f.visibility = &v
	}

	stmt, args := buildItemListSQL(f, sortCol, dir)
	rows, err := s.raw.QueryContext(r.Context(), stmt, args...)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	defer rows.Close()
	var list []itemListRow
	for rows.Next() {
		var row itemListRow
		if err := rows.Scan(
			&row.ID, &row.Name, &row.Description, &row.Qty, &row.Unit,
			&row.LocationID, &row.Visibility, &row.Disposed, &row.CreatedAt, &row.UpdatedAt,
			&row.PhotoCount, &row.ThumbKey, &row.TotalCount,
		); err != nil {
			s.internalErr(w, err)
			return
		}
		list = append(list, row)
	}
	if err := rows.Err(); err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	total := int64(len(list))
	for _, row := range list {
		var loc any
		if row.LocationID.Valid {
			loc = row.LocationID.Int64
		}
		var thumb any // nil -> JSON null when the item has no photos
		if row.ThumbKey.Valid {
			thumb = row.ThumbKey.String
		}
		out = append(out, map[string]any{
			"id": row.ID, "name": row.Name, "description": row.Description,
			"qty": row.Qty, "unit": row.Unit, "location_id": loc,
			"visibility": row.Visibility, "disposed": row.Disposed != 0,
			"photo_count": row.PhotoCount,
			"thumb_key":   thumb,
			"created_at":  row.CreatedAt, "updated_at": row.UpdatedAt,
		})
	}
	if len(list) > 0 {
		total = list[0].TotalCount
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: total})
}

func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return
	}
	// Snapshot + delete in ONE transaction: the snapshot (item fields,
	// tags, photos, documents, project links, ledger - storage keys
	// included) goes to trash, then the item row goes away and the FK
	// cascade takes its children. Files are NOT deleted here: they belong
	// to the trash entry now (restore reuses the same keys) and are
	// removed only when the entry is purged or the trash emptied.
	trashedAt := time.Now().UTC()
	tx, err := s.raw.BeginTx(r.Context(), nil)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	defer tx.Rollback()
	qtx := dbpkg.New(tx)
	item, err := qtx.GetItem(r.Context(), dbpkg.GetItemParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	snap, err := buildTrashSnapshot(r.Context(), qtx, item)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	blob, err := json.Marshal(snap)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if _, err := qtx.CreateTrashEntry(r.Context(), dbpkg.CreateTrashEntryParams{
		UserID: u.ID, Snapshot: string(blob),
		TrashedAt: trashedAt.Format(time.RFC3339),
		PurgesAt:  s.cfg.PurgeAfter(trashedAt),
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	if err := qtx.DeleteItem(r.Context(), dbpkg.DeleteItemParams{ID: id, UserID: u.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
