package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

func (s *Server) handleListShared(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p := parsePage(r)
	params := dbpkg.ListSharedItemsFromOthersParams{
		UserID: u.ID, Lim: p.Limit(), Off: p.Offset(),
	}
	// guard the TRANSFORMED value: whitespace-only input tokenizes to zero
	// phrases ("" would make items_fts MATCH '' an fts5 syntax error -> 500)
	if raw := r.URL.Query().Get("q"); raw != "" {
		if fq := ftsQuery(raw); fq != "" {
			params.Q = sqlNullString(fq)
		}
	}
	rows, err := s.q.ListSharedItemsFromOthers(r.Context(), params)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	total := int64(len(rows))
	for _, row := range rows {
		var thumb any // nil -> JSON null when the item has no photos
		if row.ThumbKey.Valid {
			thumb = row.ThumbKey.String
		}
		out = append(out, map[string]any{
			"id": row.ID, "name": row.Name, "description": row.Description,
			"qty": row.Qty, "unit": row.Unit, "visibility": row.Visibility,
			"photo_count": row.PhotoCount, "thumb_key": thumb,
			"owner_name": row.OwnerName,
			"created_at": row.CreatedAt, "updated_at": row.UpdatedAt,
		})
	}
	if len(rows) > 0 {
		total = rows[0].TotalCount
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: total})
}

// handleCopyItem copies an owned item (duplicate) or another user's shared
// item into the caller's inventory.
func (s *Server) handleCopyItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return
	}
	src, err := s.q.GetItemAnyOwner(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	if src.UserID != u.ID && src.Visibility != "shared" {
		writeErr(w, http.StatusNotFound, "not_found", "item not found") // 404: no existence leak
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	// location is NOT copied: locations are user-scoped
	newItem, err := s.q.CreateItem(r.Context(), dbpkg.CreateItemParams{
		UserID: u.ID, Name: src.Name, Description: src.Description,
		Qty: src.Qty, Unit: src.Unit,
		LocationID: sql.NullInt64{}, Visibility: u.DefaultItemVisibility,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}

	// tags by name (into the copier's tag space)
	if tags, err := s.q.GetItemTags(r.Context(), src.ID); err == nil {
		names := make([]string, 0, len(tags))
		for _, tg := range tags {
			names = append(names, tg.Name)
		}
		if len(names) > 0 {
			if err := s.setItemTags(r, u, newItem.ID, names); err != nil {
				s.tagErr(w, err)
				return
			}
		}
	} else {
		s.internalErr(w, err)
		return
	}

	// photos and documents: physically copy the files
	photos, err := s.q.ListItemPhotos(r.Context(), src.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	for _, ph := range photos {
		newKey, err := s.st.Copy(ph.StorageKey)
		if err != nil {
			s.internalErr(w, err)
			return
		}
		if _, err := s.q.AddItemPhoto(r.Context(), dbpkg.AddItemPhotoParams{
			ItemID: newItem.ID, StorageKey: newKey, Caption: ph.Caption,
			SortOrder: ph.SortOrder, CreatedAt: now,
		}); err != nil {
			s.st.Delete(newKey)
			s.internalErr(w, err)
			return
		}
	}
	docs, err := s.q.ListItemDocuments(r.Context(), src.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	for _, d := range docs {
		newKey, err := s.st.Copy(d.StorageKey)
		if err != nil {
			s.internalErr(w, err)
			return
		}
		if _, err := s.q.AddItemDocument(r.Context(), dbpkg.AddItemDocumentParams{
			ItemID: newItem.ID, StorageKey: newKey, Filename: d.Filename,
			MimeType: d.MimeType, SizeBytes: d.SizeBytes, CreatedAt: now,
		}); err != nil {
			s.st.Delete(newKey)
			s.internalErr(w, err)
			return
		}
	}
	// project links are personal and intentionally NOT copied.

	detail, err := s.itemDetailJSON(r, newItem)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, detail)
}
