package api

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

// usageKinds is the item_ledger kind CHECK whitelist, mirrored at the API
// boundary so invalid kinds 400 before any write.
var usageKinds = map[string]bool{
	"used": true, "sold": true, "lost": true, "found": true,
	"restock": true, "disposed": true, "adjustment": true,
}

type usageReq struct {
	Kind      string   `json:"kind"`
	Delta     *float64 `json:"delta"`
	Note      *string  `json:"note"`
	ProjectID *int64   `json:"project_id"`
}

// ledgerEntryJSON shapes one item_ledger row; project_id is null when unset.
func ledgerEntryJSON(e dbpkg.ItemLedger) map[string]any {
	var proj any
	if e.ProjectID.Valid {
		proj = e.ProjectID.Int64
	}
	return map[string]any{
		"id": e.ID, "delta": e.Delta, "resulting_qty": e.ResultingQty,
		"kind": e.Kind, "project_id": proj, "note": e.Note,
		"created_at": e.CreatedAt,
	}
}

// insertLedgerEntry writes one ledger row via q, which may be transaction-
// backed (usage POST and qty PATCHes pair it with the items update in one tx).
func insertLedgerEntry(ctx context.Context, q *dbpkg.Queries, arg dbpkg.CreateItemLedgerParams) (dbpkg.ItemLedger, error) {
	return q.CreateItemLedger(ctx, arg)
}

// recordInitialLedger writes the baseline entry every item starts with
// (create and copy): kind=adjustment, note=initial, delta=resulting=qty.
func (s *Server) recordInitialLedger(ctx context.Context, itemID, userID int64, qty float64, now string) error {
	_, err := insertLedgerEntry(ctx, s.q, dbpkg.CreateItemLedgerParams{
		ItemID: itemID, UserID: userID, Delta: qty, ResultingQty: qty,
		Kind: "adjustment", Note: "initial", CreatedAt: now,
	})
	return err
}

// handleAddItemUsage records one usage event and applies its qty effect
// atomically: items.qty/disposed update + ledger insert in a single tx.
func (s *Server) handleAddItemUsage(w http.ResponseWriter, r *http.Request) {
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
	var req usageReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if !usageKinds[req.Kind] {
		writeErr(w, http.StatusBadRequest, "validation_error",
			"kind must be one of: used, sold, lost, found, restock, disposed, adjustment")
		return
	}
	var proj sql.NullInt64
	if req.ProjectID != nil {
		if _, err := s.q.GetProject(r.Context(), dbpkg.GetProjectParams{ID: *req.ProjectID, UserID: u.ID}); err != nil {
			writeErr(w, http.StatusNotFound, "not_found", "project not found")
			return
		}
		proj = sqlNullInt64(*req.ProjectID)
	}
	note := ""
	if req.Note != nil {
		note = *req.Note
	}

	var delta, resulting float64
	disposed := item.Disposed
	if req.Kind == "disposed" {
		// forced drain: delta = -current qty, resulting 0, flag set
		delta = -item.Qty
		resulting = 0
		disposed = 1
	} else {
		if req.Delta == nil {
			writeErr(w, http.StatusBadRequest, "validation_error", "delta is required")
			return
		}
		if *req.Delta == 0 {
			writeErr(w, http.StatusBadRequest, "validation_error", "delta must not be zero")
			return
		}
		delta = *req.Delta
		resulting = item.Qty + delta
		if resulting < 0 {
			writeErr(w, http.StatusBadRequest, "validation_error", "qty would go below zero")
			return
		}
		if resulting > 0 {
			disposed = 0 // back in circulation
		} // resulting == 0: flag unchanged
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.raw.BeginTx(r.Context(), nil)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	defer tx.Rollback()
	qtx := dbpkg.New(tx)
	if err := qtx.UpdateItem(r.Context(), dbpkg.UpdateItemParams{
		Name: item.Name, Description: item.Description, Qty: resulting,
		Unit: item.Unit, LocationID: item.LocationID, Visibility: item.Visibility,
		Disposed: disposed, UpdatedAt: now, ID: item.ID, UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	entry, err := insertLedgerEntry(r.Context(), qtx, dbpkg.CreateItemLedgerParams{
		ItemID: item.ID, UserID: u.ID, Delta: delta, ResultingQty: resulting,
		Kind: req.Kind, ProjectID: proj, Note: note, CreatedAt: now,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ledgerEntryJSON(entry))
}

// handleListItemUsage returns the item's ledger, newest first, in the
// standard {data, total} envelope.
func (s *Server) handleListItemUsage(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return
	}
	if _, err := s.q.GetItem(r.Context(), dbpkg.GetItemParams{ID: id, UserID: u.ID}); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	p := parsePage(r)
	rows, err := s.q.ListItemLedger(r.Context(), dbpkg.ListItemLedgerParams{
		ItemID: id, UserID: u.ID, Limit: p.Limit(), Offset: p.Offset(),
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	total := int64(len(rows))
	for _, row := range rows {
		out = append(out, ledgerEntryJSON(dbpkg.ItemLedger{
			ID: row.ID, ItemID: row.ItemID, UserID: row.UserID, Delta: row.Delta,
			ResultingQty: row.ResultingQty, Kind: row.Kind, ProjectID: row.ProjectID,
			Note: row.Note, CreatedAt: row.CreatedAt,
		}))
	}
	if len(rows) > 0 {
		total = rows[0].TotalCount
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: total})
}
