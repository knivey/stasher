package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

func locationJSON(l dbpkg.Location) map[string]any {
	var parent any
	if l.ParentID.Valid {
		parent = l.ParentID.Int64
	}
	return map[string]any{
		"id": l.ID, "name": l.Name, "parent_id": parent, "notes": l.Notes,
		"created_at": l.CreatedAt, "updated_at": l.UpdatedAt,
	}
}

type locationReq struct {
	Name     string `json:"name"`
	Notes    string `json:"notes"`
	ParentID *int64 `json:"parent_id"`
}

// checkParent verifies parent is owned by u (or nil). Returns param for SQL.
func (s *Server) checkParent(w http.ResponseWriter, r *http.Request, u dbpkg.User, parentID *int64) (sql.NullInt64, bool) {
	if parentID == nil {
		return sql.NullInt64{}, true
	}
	if _, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: *parentID, UserID: u.ID}); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "parent location not found")
		return sql.NullInt64{}, false
	}
	return sqlNullInt64(*parentID), true
}

func (s *Server) handleCreateLocation(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req locationReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || len(req.Name) > 200 {
		writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
		return
	}
	parent, ok := s.checkParent(w, r, u, req.ParentID)
	if !ok {
		return
	}
	// app-logic uniqueness: (user_id, parent, name)
	if existing, err := s.q.GetLocationByName(r.Context(), dbpkg.GetLocationByNameParams{
		UserID: u.ID, Name: req.Name, ParentID: parent,
	}); err == nil && existing.ID != 0 {
		writeErr(w, http.StatusConflict, "location_exists",
			"a location with this name already exists under this parent")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	loc, err := s.q.CreateLocation(r.Context(), dbpkg.CreateLocationParams{
		UserID: u.ID, Name: req.Name, ParentID: parent, Notes: req.Notes,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, locationJSON(loc))
}

func (s *Server) handleGetLocation(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid location id")
		return
	}
	loc, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "location not found")
		return
	}
	crumbs, err := s.q.LocationBreadcrumbs(r.Context(), dbpkg.LocationBreadcrumbsParams{
		ID: id, UserID: u.ID, UserID_2: u.ID,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(crumbs))
	for _, c := range crumbs {
		out = append(out, map[string]any{"id": c.ID, "name": c.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"location": locationJSON(loc), "breadcrumbs": out})
}

func (s *Server) handleListLocations(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	locs, err := s.q.ListLocations(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(locs))
	for _, l := range locs {
		out = append(out, locationJSON(l))
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: int64(len(out))})
}

func (s *Server) handleUpdateLocation(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid location id")
		return
	}
	loc, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "location not found")
		return
	}
	var req locationReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != "" { // empty name = keep current
		if len(req.Name) > 200 {
			writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
			return
		}
		loc.Name = req.Name
	}
	loc.Notes = req.Notes

	var parent sql.NullInt64
	if req.ParentID != nil { // null explicitly clears the parent
		if *req.ParentID == id {
			writeErr(w, http.StatusBadRequest, "cycle", "a location cannot be its own parent")
			return
		}
		p, ok := s.checkParent(w, r, u, req.ParentID)
		if !ok {
			return
		}
		// cycle check: new parent must not be a descendant of id
		desc, err := s.q.LocationDescendants(r.Context(), dbpkg.LocationDescendantsParams{
			ID: id, UserID: u.ID, UserID_2: u.ID,
		})
		if err != nil {
			s.internalErr(w, err)
			return
		}
		for _, d := range desc {
			if d == *req.ParentID {
				writeErr(w, http.StatusBadRequest, "cycle", "cannot move a location under its own descendant")
				return
			}
		}
		parent = p
	} else {
		parent = loc.ParentID
	}

	// app-logic uniqueness check against the new (name, parent) combo
	if existing, err := s.q.GetLocationByName(r.Context(), dbpkg.GetLocationByNameParams{
		UserID: u.ID, Name: loc.Name, ParentID: parent,
	}); err == nil && existing.ID != id {
		writeErr(w, http.StatusConflict, "location_exists",
			"a location with this name already exists under this parent")
		return
	}

	if err := s.q.UpdateLocation(r.Context(), dbpkg.UpdateLocationParams{
		Name: loc.Name, Notes: loc.Notes, ParentID: parent,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339), ID: id, UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	loc, _ = s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: id, UserID: u.ID})
	writeJSON(w, http.StatusOK, locationJSON(loc))
}

func (s *Server) handleDeleteLocation(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid location id")
		return
	}
	loc, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "location not found")
		return
	}
	tx, err := s.raw.BeginTx(r.Context(), nil)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	defer tx.Rollback()
	qtx := dbpkg.New(tx)
	now := time.Now().UTC().Format(time.RFC3339)
	if err := qtx.ReparentChildren(r.Context(), dbpkg.ReparentChildrenParams{
		ParentID: loc.ParentID, ParentID_2: sqlNullInt64(id), UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	if err := qtx.ClearItemsLocation(r.Context(), dbpkg.ClearItemsLocationParams{
		UpdatedAt: now, LocationID: sqlNullInt64(id), UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	if err := qtx.DeleteLocation(r.Context(), dbpkg.DeleteLocationParams{ID: id, UserID: u.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
