package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

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
	if _, err := s.q.GetTagByName(r.Context(), dbpkg.GetTagByNameParams{UserID: u.ID, Name: req.Name}); err == nil {
		writeErr(w, http.StatusConflict, "tag_exists", "tag already exists")
		return
	}
	tg, err := s.q.CreateTag(r.Context(), dbpkg.CreateTagParams{UserID: u.ID, Name: req.Name})
	if err != nil {
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
	if existing, err := s.q.GetTagByName(r.Context(), dbpkg.GetTagByNameParams{UserID: u.ID, Name: req.Name}); err == nil && existing.ID != id {
		writeErr(w, http.StatusConflict, "tag_exists", "another tag already has this name")
		return
	}
	if err := s.q.UpdateTag(r.Context(), dbpkg.UpdateTagParams{Name: req.Name, ID: id, UserID: u.ID}); err != nil {
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
	if err := s.q.DeleteTag(r.Context(), dbpkg.DeleteTagParams{ID: id, UserID: u.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
