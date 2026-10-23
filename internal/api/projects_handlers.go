package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

func validProjectStatus(s string) bool {
	return s == "planned" || s == "active" || s == "done" || s == "archived"
}

func projectJSON(p dbpkg.Project) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "status": p.Status, "notes": p.Notes,
		"visibility": p.Visibility,
		"created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
	}
}

type projectReq struct {
	Name       string  `json:"name"`
	Status     *string `json:"status"`
	Notes      *string `json:"notes"`
	Visibility *string `json:"visibility"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req projectReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || len(req.Name) > 200 {
		writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
		return
	}
	status := "planned"
	if req.Status != nil {
		if !validProjectStatus(*req.Status) {
			writeErr(w, http.StatusBadRequest, "validation_error",
				"status must be planned, active, done, or archived")
			return
		}
		status = *req.Status
	}
	visibility := "private"
	if req.Visibility != nil {
		if *req.Visibility != "private" && *req.Visibility != "shared" {
			writeErr(w, http.StatusBadRequest, "validation_error", "visibility must be private or shared")
			return
		}
		visibility = *req.Visibility
	}
	notes := ""
	if req.Notes != nil {
		notes = *req.Notes
	}
	now := time.Now().UTC().Format(time.RFC3339)
	p, err := s.q.CreateProject(r.Context(), dbpkg.CreateProjectParams{
		UserID: u.ID, Name: req.Name, Status: status, Notes: notes,
		Visibility: visibility, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, projectJSON(p))
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	rows, err := s.q.ListProjects(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, p := range rows {
		out = append(out, projectJSON(p))
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: int64(len(out))})
}

// requireOwnedProject writes 404 unless the project belongs to the user.
func (s *Server) requireOwnedProject(w http.ResponseWriter, r *http.Request, u dbpkg.User) (dbpkg.Project, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid project id")
		return dbpkg.Project{}, false
	}
	p, err := s.q.GetProject(r.Context(), dbpkg.GetProjectParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "project not found")
		return dbpkg.Project{}, false
	}
	return p, true
}

// requireViewableProject writes 404 unless the project is the user's own or
// another user's shared project. isOwner tells handlers which projection to
// build; mutating routes use requireOwnedProject instead.
func (s *Server) requireViewableProject(w http.ResponseWriter, r *http.Request, u dbpkg.User) (p dbpkg.Project, isOwner bool, ok bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid project id")
		return dbpkg.Project{}, false, false
	}
	p, err = s.q.GetProjectAnyOwner(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "project not found")
		return dbpkg.Project{}, false, false
	}
	if p.UserID != u.ID && p.Visibility != "shared" {
		writeErr(w, http.StatusNotFound, "not_found", "project not found")
		return dbpkg.Project{}, false, false
	}
	return p, p.UserID == u.ID, true
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, isOwner, ok := s.requireViewableProject(w, r, u)
	if !ok {
		return
	}
	items, err := s.q.ListProjectItems(r.Context(), p.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	itemList := make([]map[string]any, 0, len(items))
	for _, pi := range items {
		var qtyUsed any
		if pi.QtyUsed.Valid {
			qtyUsed = pi.QtyUsed.Float64
		}
		if isOwner {
			itemList = append(itemList, map[string]any{
				"item_id": pi.ItemID, "item_name": pi.ItemName,
				"item_qty": pi.ItemQty, "item_unit": pi.ItemUnit,
				"qty_used": qtyUsed, "notes": pi.Notes, "added_at": pi.AddedAt,
				"shared": pi.ItemVisibility == "shared",
			})
		} else {
			// sharee BOM: name + qty used + shared flag only (spec §2) —
			// item identity, link notes, and added_at stay owner-private.
			itemList = append(itemList, map[string]any{
				"item_name": pi.ItemName, "qty_used": qtyUsed,
				"shared": pi.ItemVisibility == "shared",
			})
		}
	}
	body := map[string]any{
		"project": projectJSON(p), "owner_view": isOwner, "items": itemList,
	}
	if isOwner {
		// notes_log is owner-only: sharee responses OMIT the key entirely.
		notes, err := s.q.ListProjectNotes(r.Context(), p.ID)
		if err != nil {
			s.internalErr(w, err)
			return
		}
		noteList := make([]map[string]any, 0, len(notes))
		for _, n := range notes {
			noteList = append(noteList, map[string]any{"id": n.ID, "body": n.Body, "created_at": n.CreatedAt})
		}
		body["notes_log"] = noteList
	}
	// comments are the public thread: present in BOTH projections.
	comments, err := s.q.ListProjectComments(r.Context(), p.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	comList := make([]map[string]any, 0, len(comments))
	for _, c := range comments {
		comList = append(comList, map[string]any{
			"id": c.ID, "author": c.AuthorName, "mine": c.UserID == u.ID,
			"body": c.Body, "created_at": c.CreatedAt,
		})
	}
	body["comments"] = comList
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	var req projectReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != "" {
		if len(req.Name) > 200 {
			writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
			return
		}
		p.Name = req.Name
	}
	if req.Status != nil {
		if !validProjectStatus(*req.Status) {
			writeErr(w, http.StatusBadRequest, "validation_error",
				"status must be planned, active, done, or archived")
			return
		}
		p.Status = *req.Status
	}
	if req.Notes != nil {
		p.Notes = *req.Notes
	}
	if req.Visibility != nil {
		if *req.Visibility != "private" && *req.Visibility != "shared" {
			writeErr(w, http.StatusBadRequest, "validation_error", "visibility must be private or shared")
			return
		}
		p.Visibility = *req.Visibility
	}
	if err := s.q.UpdateProject(r.Context(), dbpkg.UpdateProjectParams{
		Name: p.Name, Status: p.Status, Notes: p.Notes,
		Visibility: p.Visibility,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339), ID: p.ID, UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	p, _ = s.q.GetProject(r.Context(), dbpkg.GetProjectParams{ID: p.ID, UserID: u.ID})
	writeJSON(w, http.StatusOK, projectJSON(p))
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	if err := s.q.DeleteProject(r.Context(), dbpkg.DeleteProjectParams{ID: p.ID, UserID: u.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type projectItemReq struct {
	ItemID  int64    `json:"item_id"`
	QtyUsed *float64 `json:"qty_used"`
	Notes   string   `json:"notes"`
}

// validQtyUsed matches the items.qty range: 0..1e12.
func validQtyUsed(q float64) bool { return q >= 0 && q <= 1e12 }

func (s *Server) handleAddProjectItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	var req projectItemReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.QtyUsed != nil && !validQtyUsed(*req.QtyUsed) {
		writeErr(w, http.StatusBadRequest, "validation_error", "qty_used must be between 0 and 1e12")
		return
	}
	// the item must belong to the same user
	if _, err := s.q.GetItem(r.Context(), dbpkg.GetItemParams{ID: req.ItemID, UserID: u.ID}); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	var qtyUsed sql.NullFloat64
	if req.QtyUsed != nil {
		qtyUsed = sqlNullFloat64(*req.QtyUsed)
	}
	if err := s.q.UpsertProjectItem(r.Context(), dbpkg.UpsertProjectItemParams{
		ProjectID: p.ID, ItemID: req.ItemID, QtyUsed: qtyUsed, Notes: req.Notes,
		AddedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handlePatchProjectItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	itemID, err := strconv.ParseInt(chi.URLParam(r, "itemId"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return
	}
	var req projectItemReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.QtyUsed != nil && !validQtyUsed(*req.QtyUsed) {
		writeErr(w, http.StatusBadRequest, "validation_error", "qty_used must be between 0 and 1e12")
		return
	}
	// read current row to merge patch fields
	rows, err := s.q.ListProjectItems(r.Context(), p.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	var cur *dbpkg.ListProjectItemsRow
	for i := range rows {
		if rows[i].ItemID == itemID {
			cur = &rows[i]
			break
		}
	}
	if cur == nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not in project")
		return
	}
	qtyUsed := cur.QtyUsed
	if req.QtyUsed != nil {
		qtyUsed = sqlNullFloat64(*req.QtyUsed)
	}
	notes := cur.Notes
	if req.Notes != "" {
		notes = req.Notes
	}
	if err := s.q.UpsertProjectItem(r.Context(), dbpkg.UpsertProjectItemParams{
		ProjectID: p.ID, ItemID: itemID, QtyUsed: qtyUsed, Notes: notes,
		AddedAt: cur.AddedAt,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteProjectItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	itemID, err := strconv.ParseInt(chi.URLParam(r, "itemId"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return
	}
	rows, err := s.q.DeleteProjectItem(r.Context(), dbpkg.DeleteProjectItemParams{
		ProjectID: p.ID, ItemID: itemID,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if rows == 0 {
		writeErr(w, http.StatusNotFound, "not_found", "item not in project")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
