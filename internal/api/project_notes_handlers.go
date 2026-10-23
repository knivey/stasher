package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

// ---- Project notes (owner-only log) ----

// handleAddProjectNote creates a timestamped note on an owned project. The
// body is trimmed and must be 1-2000 chars AFTER the trim (a whitespace-only
// body is empty) — same semantics as item notes. Sharees get 404 via
// requireOwnedProject even on shared projects.
func (s *Server) handleAddProjectNote(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	var req noteReq
	if !decodeJSON(w, r, &req) {
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" || len(body) > 2000 {
		writeErr(w, http.StatusBadRequest, "validation_error", "note must be 1-2000 characters")
		return
	}
	note, err := s.q.AddProjectNote(r.Context(), dbpkg.AddProjectNoteParams{
		ProjectID: p.ID, Body: body,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": note.ID, "body": note.Body, "created_at": note.CreatedAt,
	})
}

// handleDeleteProjectNote removes one of the project's notes (204). An
// absent or already-deleted note is 404 via execrows; a note on another
// project's log also misses (project_id bound in the WHERE).
func (s *Server) handleDeleteProjectNote(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	noteID, err := strconv.ParseInt(chi.URLParam(r, "noteId"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid note id")
		return
	}
	rows, err := s.q.DeleteProjectNote(r.Context(), dbpkg.DeleteProjectNoteParams{ID: noteID, ProjectID: p.ID})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if rows == 0 {
		writeErr(w, http.StatusNotFound, "not_found", "note not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
