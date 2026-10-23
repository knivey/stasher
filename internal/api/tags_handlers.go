package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

// uniqueTagViolation reports whether err is SQLite's UNIQUE(user_id, name)
// backstop on tags. modernc surfaces the constraint text in Error(), so the
// match is the same string style as the users.email check in
// auth_handlers.go.
func uniqueTagViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: tags.user_id, tags.name")
}

func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	rows, err := s.q.ListTagsWithCounts(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, tg := range rows {
		out = append(out, map[string]any{"id": tg.ID, "name": tg.Name, "item_count": tg.ItemCount})
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: int64(len(out))})
}

type tagReq struct {
	Name string `json:"name"`
}

func (s *Server) handleCreateTag(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req tagReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || len(req.Name) > 100 {
		writeErr(w, http.StatusBadRequest, "validation_error", "tag name must be 1-100 chars")
		return
	}
	// No check-then-insert pre-select: the UNIQUE(user_id, name) constraint
	// is the single source of truth, so a duplicate — including one inserted
	// between a check and the write — maps to 409 instead of a 500.
	tg, err := s.q.CreateTag(r.Context(), dbpkg.CreateTagParams{UserID: u.ID, Name: req.Name})
	if err != nil {
		if uniqueTagViolation(err) {
			writeErr(w, http.StatusConflict, "duplicate_tag", "tag already exists")
			return
		}
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": tg.ID, "name": tg.Name})
}

func (s *Server) handleUpdateTag(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid tag id")
		return
	}
	var req tagReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || len(req.Name) > 100 {
		writeErr(w, http.StatusBadRequest, "validation_error", "tag name must be 1-100 chars")
		return
	}
	// Ownership BEFORE collision: a foreign/absent id is 404 not_found even
	// when the new name collides with one of the caller's own tags (the old
	// collision-first order answered 409 before existence). The rename
	// itself carries the collision check via the UNIQUE backstop; renaming a
	// tag to its own current name does not violate UNIQUE and stays a 200.
	if _, err := s.q.GetTag(r.Context(), dbpkg.GetTagParams{ID: id, UserID: u.ID}); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "tag not found")
		return
	}
	if _, err := s.q.UpdateTag(r.Context(), dbpkg.UpdateTagParams{Name: req.Name, ID: id, UserID: u.ID}); err != nil {
		if uniqueTagViolation(err) {
			writeErr(w, http.StatusConflict, "tag_exists", "another tag already has this name")
			return
		}
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": req.Name})
}

func (s *Server) handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid tag id")
		return
	}
	n, err := s.q.DeleteTag(r.Context(), dbpkg.DeleteTagParams{ID: id, UserID: u.ID})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if n == 0 {
		writeErr(w, http.StatusNotFound, "not_found", "tag not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
