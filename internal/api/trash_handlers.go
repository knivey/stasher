package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

// trashSnapshot is the JSON blob stored in trash.snapshot: everything
// needed to resurrect a deleted item (under a NEW id) on restore. Storage
// keys are copied verbatim - the files stay on disk while the entry lives.
type trashSnapshot struct {
	ItemID      int64   `json:"item_id"` // original id; guards rowid reuse
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Qty         float64 `json:"qty"`
	Unit        string  `json:"unit"`
	Visibility  string  `json:"visibility"`
	Disposed    bool    `json:"disposed"`
	LocationID  *int64  `json:"location_id"` // nil = no location
	// "" in legacy snapshots = no product_url (restores to NULL)
	ProductUrl string `json:"product_url,omitempty"`

	Tags         []string                  `json:"tags"`
	Photos       []trashSnapshotPhoto      `json:"photos"`
	Documents    []trashSnapshotDocument   `json:"documents"`
	Notes        []trashSnapshotNote       `json:"notes"`
	ProjectItems []trashSnapshotProjectRef `json:"project_items"`
	Ledger       []trashSnapshotLedger     `json:"ledger"`
}

type trashSnapshotPhoto struct {
	StorageKey string `json:"storage_key"`
	ThumbKey   string `json:"thumb_key,omitempty"` // "" in old snapshots = no thumb
	Caption    string `json:"caption"`
	SortOrder  int64  `json:"sort_order"`
}

type trashSnapshotDocument struct {
	StorageKey string `json:"storage_key"`
	Filename   string `json:"filename"`
	MimeType   string `json:"mime_type"`
	SizeBytes  int64  `json:"size_bytes"`
}

// trashSnapshotNote keeps only body + created_at (the note's whole state;
// ids are re-assigned on restore, timestamps are preserved - ledger-style).
type trashSnapshotNote struct {
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

type trashSnapshotProjectRef struct {
	ProjectID int64    `json:"project_id"`
	QtyUsed   *float64 `json:"qty_used"` // nil = unset
	Note      string   `json:"note"`
	AddedAt   string   `json:"added_at"`
}

type trashSnapshotLedger struct {
	Delta        float64 `json:"delta"`
	ResultingQty float64 `json:"resulting_qty"`
	Kind         string  `json:"kind"`
	Note         string  `json:"note"`
	ProjectID    *int64  `json:"project_id"` // nil = no project
	CreatedAt    string  `json:"created_at"`
}

// buildTrashSnapshot reads every child row of an item (via q, which may be
// transaction-backed) and assembles the restore snapshot.
func buildTrashSnapshot(ctx context.Context, q *dbpkg.Queries, item dbpkg.Item) (trashSnapshot, error) {
	snap := trashSnapshot{
		ItemID: item.ID,
		Name:   item.Name, Description: item.Description, Qty: item.Qty,
		Unit: item.Unit, Visibility: item.Visibility,
		Disposed: item.Disposed != 0,
		// "" when unset (same convention as the photos' thumb_key)
		ProductUrl: item.ProductUrl.String,
	}
	if item.LocationID.Valid {
		loc := item.LocationID.Int64
		snap.LocationID = &loc
	}
	tags, err := q.GetItemTags(ctx, item.ID)
	if err != nil {
		return snap, err
	}
	snap.Tags = make([]string, 0, len(tags))
	for _, tg := range tags {
		snap.Tags = append(snap.Tags, tg.Name)
	}
	photos, err := q.ListItemPhotos(ctx, item.ID)
	if err != nil {
		return snap, err
	}
	snap.Photos = make([]trashSnapshotPhoto, 0, len(photos))
	for _, p := range photos {
		snap.Photos = append(snap.Photos, trashSnapshotPhoto{
			StorageKey: p.StorageKey, ThumbKey: p.ThumbKey.String, // "" when invalid
			Caption: p.Caption, SortOrder: p.SortOrder,
		})
	}
	docs, err := q.ListItemDocuments(ctx, item.ID)
	if err != nil {
		return snap, err
	}
	snap.Documents = make([]trashSnapshotDocument, 0, len(docs))
	for _, d := range docs {
		snap.Documents = append(snap.Documents, trashSnapshotDocument{
			StorageKey: d.StorageKey, Filename: d.Filename,
			MimeType: d.MimeType, SizeBytes: d.SizeBytes,
		})
	}
	notes, err := q.ListItemNotes(ctx, item.ID)
	if err != nil {
		return snap, err
	}
	snap.Notes = make([]trashSnapshotNote, 0, len(notes))
	for _, n := range notes {
		snap.Notes = append(snap.Notes, trashSnapshotNote{Body: n.Body, CreatedAt: n.CreatedAt})
	}
	links, err := q.ListItemProjectLinks(ctx, item.ID)
	if err != nil {
		return snap, err
	}
	snap.ProjectItems = make([]trashSnapshotProjectRef, 0, len(links))
	for _, l := range links {
		ref := trashSnapshotProjectRef{
			ProjectID: l.ProjectID, Note: l.Notes, AddedAt: l.AddedAt,
		}
		if l.QtyUsed.Valid {
			qty := l.QtyUsed.Float64
			ref.QtyUsed = &qty
		}
		snap.ProjectItems = append(snap.ProjectItems, ref)
	}
	ledger, err := q.ListItemLedgerAll(ctx, dbpkg.ListItemLedgerAllParams{
		ItemID: item.ID, UserID: item.UserID,
	})
	if err != nil {
		return snap, err
	}
	snap.Ledger = make([]trashSnapshotLedger, 0, len(ledger))
	for _, e := range ledger {
		entry := trashSnapshotLedger{
			Delta: e.Delta, ResultingQty: e.ResultingQty, Kind: e.Kind,
			Note: e.Note, CreatedAt: e.CreatedAt,
		}
		if e.ProjectID.Valid {
			pid := e.ProjectID.Int64
			entry.ProjectID = &pid
		}
		snap.Ledger = append(snap.Ledger, entry)
	}
	return snap, nil
}

// deleteSnapshotFiles removes every file a trash snapshot references,
// including derived thumbnails. Missing files are not an error
// (Storage.Delete treats them as no-ops); a failed delete is logged and
// swallowed — a leaked file beats a wedged purge, and the DB row is
// already gone by the time this runs on the manual purge path.
func (s *Server) deleteSnapshotFiles(snap trashSnapshot) {
	for _, p := range snap.Photos {
		if err := s.st.Delete(p.StorageKey); err != nil {
			slog.Warn("file delete failed", "key", p.StorageKey, "err", err)
		}
		if p.ThumbKey != "" {
			if err := s.st.Delete(p.ThumbKey); err != nil {
				slog.Warn("file delete failed", "key", p.ThumbKey, "err", err)
			}
		}
	}
	for _, d := range snap.Documents {
		if err := s.st.Delete(d.StorageKey); err != nil {
			slog.Warn("file delete failed", "key", d.StorageKey, "err", err)
		}
	}
}

// PurgeExpiredTrash deletes expired entries and their files. It runs at
// startup (cmd/stasher) and at the top of GET /trash.
func (s *Server) PurgeExpiredTrash(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	snapshots, err := s.q.DeleteExpiredTrash(ctx, now)
	if err != nil {
		return err
	}
	for _, blob := range snapshots {
		var snap trashSnapshot
		if json.Unmarshal([]byte(blob), &snap) == nil {
			s.deleteSnapshotFiles(snap)
		}
	}
	return nil
}

// trashRowJSON renders one GET /trash row from the stored snapshot.
func trashRowJSON(e dbpkg.ListTrashRow) (map[string]any, error) {
	var snap trashSnapshot
	if err := json.Unmarshal([]byte(e.Snapshot), &snap); err != nil {
		return nil, err
	}
	var thumb any // first photo (snapshot order = sort_order, id); derived
	// thumb when the snapshot has one, else the original (legacy snapshots)
	if len(snap.Photos) > 0 {
		k := snap.Photos[0].ThumbKey
		if k == "" {
			k = snap.Photos[0].StorageKey
		}
		thumb = k
	}
	var purges any // '' (never purge) renders as JSON null
	if e.PurgesAt != "" {
		purges = e.PurgesAt
	}
	return map[string]any{
		"id": e.ID, "name": snap.Name, "qty": snap.Qty,
		"photo_count": len(snap.Photos), "thumb_key": thumb,
		"trashed_at": e.TrashedAt, "purges_at": purges,
	}, nil
}

func (s *Server) handleListTrash(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	if err := s.PurgeExpiredTrash(r.Context()); err != nil {
		s.internalErr(w, err)
		return
	}
	p := parsePage(r)
	rows, err := s.q.ListTrash(r.Context(), dbpkg.ListTrashParams{
		UserID: u.ID, Limit: p.Limit(), Offset: p.Offset(),
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	total := int64(len(rows))
	for _, row := range rows {
		m, err := trashRowJSON(row)
		if err != nil {
			s.internalErr(w, err)
			return
		}
		out = append(out, m)
	}
	if len(rows) > 0 {
		total = rows[0].TotalCount
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: total})
}

// handleRestoreTrashEntry recreates the snapshot's item under a NEW id in
// one transaction: item row, tags (recreated by name), photos and documents
// (same storage keys), project links (only for projects still owned), and
// the full ledger history in original order. The trash entry is deleted in
// the same commit.
func (s *Server) handleRestoreTrashEntry(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid trash id")
		return
	}
	tx, err := s.raw.BeginTx(r.Context(), nil)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	defer tx.Rollback()
	qtx := dbpkg.New(tx)

	entry, err := qtx.GetTrashEntry(r.Context(), dbpkg.GetTrashEntryParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "trash entry not found")
		return
	}
	var snap trashSnapshot
	if err := json.Unmarshal([]byte(entry.Snapshot), &snap); err != nil {
		s.internalErr(w, err)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var loc sql.NullInt64
	if snap.LocationID != nil {
		// location vanished while trashed -> restore without one
		if _, err := qtx.GetLocation(r.Context(), dbpkg.GetLocationParams{
			ID: *snap.LocationID, UserID: u.ID,
		}); err == nil {
			loc = sqlNullInt64(*snap.LocationID)
		}
	}
	// SQLite recycles the max rowid after a delete, so a plain INSERT
	// would hand back the trashed item's own id when it was the newest.
	// Pick an explicit id above both the current max and the original.
	maxID, err := qtx.MaxItemID(r.Context())
	if err != nil {
		s.internalErr(w, err)
		return
	}
	newID := maxID + 1
	if newID <= snap.ItemID {
		newID = snap.ItemID + 1
	}
	item, err := qtx.CreateItemWithID(r.Context(), dbpkg.CreateItemWithIDParams{
		ID:     newID,
		UserID: u.ID, Name: snap.Name, Description: snap.Description,
		Qty: snap.Qty, Unit: snap.Unit, LocationID: loc,
		Visibility: snap.Visibility,
		ProductUrl: sql.NullString{String: snap.ProductUrl, Valid: snap.ProductUrl != ""},
		CreatedAt:  now, UpdatedAt: now,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if snap.Disposed { // CreateItem always writes disposed=0
		if err := qtx.UpdateItem(r.Context(), dbpkg.UpdateItemParams{
			Name: item.Name, Description: item.Description, Qty: item.Qty,
			Unit: item.Unit, LocationID: item.LocationID, Visibility: item.Visibility,
			ProductUrl: item.ProductUrl, Disposed: 1, UpdatedAt: now, ID: item.ID, UserID: u.ID,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
	}
	for _, name := range snap.Tags {
		tg, err := qtx.GetTagByName(r.Context(), dbpkg.GetTagByNameParams{UserID: u.ID, Name: name})
		if err != nil {
			tg, err = qtx.CreateTag(r.Context(), dbpkg.CreateTagParams{UserID: u.ID, Name: name})
			if err != nil {
				s.internalErr(w, err)
				return
			}
		}
		if err := qtx.AttachItemTag(r.Context(), dbpkg.AttachItemTagParams{ItemID: item.ID, TagID: tg.ID}); err != nil {
			s.internalErr(w, err)
			return
		}
	}
	for _, p := range snap.Photos { // same storage keys: files never moved
		if _, err := qtx.AddItemPhoto(r.Context(), dbpkg.AddItemPhotoParams{
			ItemID: item.ID, StorageKey: p.StorageKey,
			ThumbKey: sql.NullString{String: p.ThumbKey, Valid: p.ThumbKey != ""},
			Caption:  p.Caption, SortOrder: p.SortOrder, CreatedAt: now,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
	}
	for _, d := range snap.Documents {
		if _, err := qtx.AddItemDocument(r.Context(), dbpkg.AddItemDocumentParams{
			ItemID: item.ID, StorageKey: d.StorageKey, Filename: d.Filename,
			MimeType: d.MimeType, SizeBytes: d.SizeBytes, CreatedAt: now,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
	}
	// The snapshot stores notes newest-first (ListItemNotes order), but
	// inserting in that order hands the LOWEST new id to the NEWEST note,
	// inverting the same-second (created_at DESC, id DESC) tie-break.
	// Iterate in reverse so ids ascend with original chronology.
	for i := len(snap.Notes) - 1; i >= 0; i-- { // original created_at (ledger precedent)
		n := snap.Notes[i]
		if _, err := qtx.AddItemNote(r.Context(), dbpkg.AddItemNoteParams{
			ItemID: item.ID, Body: n.Body, CreatedAt: n.CreatedAt,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
	}
	for _, ref := range snap.ProjectItems {
		// project vanished while trashed -> skip that link, keep the rest
		if _, err := qtx.GetProject(r.Context(), dbpkg.GetProjectParams{
			ID: ref.ProjectID, UserID: u.ID,
		}); err != nil {
			continue
		}
		var qty sql.NullFloat64
		if ref.QtyUsed != nil {
			qty = sqlNullFloat64(*ref.QtyUsed)
		}
		if err := qtx.UpsertProjectItem(r.Context(), dbpkg.UpsertProjectItemParams{
			ProjectID: ref.ProjectID, ItemID: item.ID, QtyUsed: qty,
			Notes: ref.Note, AddedAt: ref.AddedAt,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
	}
	for _, le := range snap.Ledger { // original order, same values, new ids
		var proj sql.NullInt64
		if le.ProjectID != nil {
			// FK would reject a vanished project; store NULL instead
			if _, err := qtx.GetProject(r.Context(), dbpkg.GetProjectParams{
				ID: *le.ProjectID, UserID: u.ID,
			}); err == nil {
				proj = sqlNullInt64(*le.ProjectID)
			}
		}
		if _, err := qtx.CreateItemLedger(r.Context(), dbpkg.CreateItemLedgerParams{
			ItemID: item.ID, UserID: u.ID, Delta: le.Delta,
			ResultingQty: le.ResultingQty, Kind: le.Kind, ProjectID: proj,
			Note: le.Note, CreatedAt: le.CreatedAt,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
	}
	if err := qtx.DeleteTrashEntry(r.Context(), dbpkg.DeleteTrashEntryParams{
		ID: entry.ID, UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.internalErr(w, err)
		return
	}
	fresh, err := s.q.GetItem(r.Context(), dbpkg.GetItemParams{ID: item.ID, UserID: u.ID})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	detail, err := s.itemDetailJSON(r, fresh)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, detail)
}

// handlePurgeTrashEntry permanently deletes one trash entry and its files.
func (s *Server) handlePurgeTrashEntry(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid trash id")
		return
	}
	entry, err := s.q.GetTrashEntry(r.Context(), dbpkg.GetTrashEntryParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "trash entry not found")
		return
	}
	var snap trashSnapshot
	if err := json.Unmarshal([]byte(entry.Snapshot), &snap); err != nil {
		s.internalErr(w, err)
		return
	}
	// delete the ROW first, files second: if the row delete failed after
	// the files were gone, the entry would dangle pointing at nothing.
	// (A failed file delete afterwards only leaks bytes — deleteSnapshotFiles
	// logs it — which is the safer failure direction.)
	if err := s.q.DeleteTrashEntry(r.Context(), dbpkg.DeleteTrashEntryParams{
		ID: entry.ID, UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	s.deleteSnapshotFiles(snap)
	w.WriteHeader(http.StatusNoContent)
}

// handleEmptyTrash purges every trash entry the user owns.
func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	snapshots, err := s.q.DeleteAllUserTrash(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	for _, blob := range snapshots {
		var snap trashSnapshot
		if json.Unmarshal([]byte(blob), &snap) == nil {
			s.deleteSnapshotFiles(snap)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
