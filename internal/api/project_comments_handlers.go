package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

// handleAddProjectComment creates a comment on a project's public thread.
// requireViewableProject IS the permission: the owner always passes (solo
// thread even on a private project); any user passes on a shared project;
// everyone else gets 404. The body is trimmed and must be 1-2000 chars
// AFTER the trim (same rules as item notes).
func (s *Server) handleAddProjectComment(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, _, ok := s.requireViewableProject(w, r, u)
	if !ok {
		return
	}
	var req noteReq
	if !decodeJSON(w, r, &req) {
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" || len(body) > 2000 {
		writeErr(w, http.StatusBadRequest, "validation_error", "comment must be 1-2000 characters")
		return
	}
	c, err := s.q.AddProjectComment(r.Context(), dbpkg.AddProjectCommentParams{
		ProjectID: p.ID, UserID: u.ID, Body: body,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": c.ID, "author": u.DisplayName, "mine": true,
		"body": c.Body, "created_at": c.CreatedAt,
	})
}

// handleDeleteProjectComment removes a comment (204). The deletion matrix is
// author-or-project-owner; everyone else — including other sharees who can
// read the thread — gets 404 (no existence leak). A comment from a different
// project is also 404.
func (s *Server) handleDeleteProjectComment(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, _, ok := s.requireViewableProject(w, r, u)
	if !ok {
		return
	}
	cid, err := strconv.ParseInt(chi.URLParam(r, "cid"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid comment id")
		return
	}
	c, err := s.q.GetProjectComment(r.Context(), cid)
	if err != nil || c.ProjectID != p.ID {
		writeErr(w, http.StatusNotFound, "not_found", "comment not found")
		return
	}
	if c.UserID != u.ID && p.UserID != u.ID {
		writeErr(w, http.StatusNotFound, "not_found", "comment not found") // 404, not 403
		return
	}
	rows, err := s.q.DeleteProjectComment(r.Context(), cid)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if rows == 0 {
		writeErr(w, http.StatusNotFound, "not_found", "comment not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
