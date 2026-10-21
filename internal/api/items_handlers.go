package api

import (
	"database/sql"
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
		"visibility": i.Visibility, "created_at": i.CreatedAt, "updated_at": i.UpdatedAt,
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
	out["projects"] = []map[string]any{} // populated by Task 16
	return out, nil
}

// setItemTags replaces the item's tags with the given names (creating tags as needed).
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
	item, err := s.q.GetItem(r.Context(), dbpkg.GetItemParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	var req itemReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != "" { // absent = keep
		if len(req.Name) > 200 {
			writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
			return
		}
		item.Name = req.Name
	}
	if req.Description != nil {
		item.Description = *req.Description
	}
	if req.Qty != nil {
		if *req.Qty < 0 || *req.Qty > 1e12 {
			writeErr(w, http.StatusBadRequest, "validation_error", "qty must be between 0 and 1e12")
			return
		}
		item.Qty = *req.Qty
	}
	if req.Unit != nil {
		item.Unit = *req.Unit
	}
	if req.Visibility != nil {
		if *req.Visibility != "private" && *req.Visibility != "shared" {
			writeErr(w, http.StatusBadRequest, "validation_error", "visibility must be private or shared")
			return
		}
		item.Visibility = *req.Visibility
	}
	if req.LocationID != nil { // explicit null clears; absent keeps
		if *req.LocationID == 0 {
			item.LocationID = sql.NullInt64{}
		} else {
			if _, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: *req.LocationID, UserID: u.ID}); err != nil {
				writeErr(w, http.StatusNotFound, "not_found", "location not found")
				return
			}
			item.LocationID = sqlNullInt64(*req.LocationID)
		}
	}
	if err := s.q.UpdateItem(r.Context(), dbpkg.UpdateItemParams{
		Name: item.Name, Description: item.Description, Qty: item.Qty, Unit: item.Unit,
		LocationID: item.LocationID, Visibility: item.Visibility,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339), ID: id, UserID: u.ID,
	}); err != nil {
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

func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
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
	// collect file keys BEFORE the row delete (FK cascade removes them)
	photos, _ := s.q.ListItemPhotos(r.Context(), id)
	docs, _ := s.q.ListItemDocuments(r.Context(), id)
	if err := s.q.DeleteItem(r.Context(), dbpkg.DeleteItemParams{ID: id, UserID: u.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	for _, p := range photos {
		s.st.Delete(p.StorageKey)
	}
	for _, d := range docs {
		s.st.Delete(d.StorageKey)
	}
	w.WriteHeader(http.StatusNoContent)
}
