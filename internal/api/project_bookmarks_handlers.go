package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

// clampBookmarkTitle trims an explicit title and caps it at 200 chars
// (matching FetchTitle's own cap).
func clampBookmarkTitle(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func (s *Server) handleAddProjectBookmark(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	var req struct {
		URL   string  `json:"url"`
		Title *string `json:"title"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validExternalURL(req.URL) {
		writeErr(w, http.StatusBadRequest, "validation_error", "url must be an http(s) URL of at most 2000 chars")
		return
	}
	title := ""
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" {
		title = clampBookmarkTitle(*req.Title)
	} else if s.fetchDirect != nil {
		// best-effort auto-title: never blocks the add (hostname fallback
		// on any failure, including a nil engine)
		title = s.fetchDirect.FetchTitle(r.Context(), req.URL)
	}
	bm, err := s.q.AddProjectBookmark(r.Context(), dbpkg.AddProjectBookmarkParams{
		ProjectID: p.ID, Url: req.URL, Title: title,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, projectBookmarkJSON(bm, dbpkg.ListProjectBookmarksRow{}))
}

// projectBookmarkJSON renders one bookmark row; the LEFT JOIN side carries
// the archived reference material when one is linked (null until Task 7
// populates archived_file_id).
func projectBookmarkJSON(bm dbpkg.ProjectBookmark, f dbpkg.ListProjectBookmarksRow) map[string]any {
	var archived any
	if f.FID.Valid {
		archived = map[string]any{
			"id": f.FID.Int64, "storage_key": f.FKey.String, "thumb_key": thumbJSON(f.FThumb),
			"filename": f.FName.String, "mime_type": f.FMime.String,
			"size_bytes": f.FSize.Int64, "created_at": f.FCreated.String,
		}
	}
	return map[string]any{
		"id": bm.ID, "url": bm.Url, "title": bm.Title,
		"created_at": bm.CreatedAt, "archived_file": archived,
	}
}

func (s *Server) handlePatchProjectBookmark(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	bid, err := strconv.ParseInt(chi.URLParam(r, "bid"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid bookmark id")
		return
	}
	bm, err := s.q.GetProjectBookmark(r.Context(), dbpkg.GetProjectBookmarkParams{ID: bid, ProjectID: p.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "bookmark not found")
		return
	}
	var req struct {
		URL   *string `json:"url"`
		Title *string `json:"title"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// absent field = keep (PATCH semantics); empty-string title is a real
	// value here — only an absent key preserves the current title
	url, title := bm.Url, bm.Title
	if req.URL != nil {
		if !validExternalURL(*req.URL) {
			writeErr(w, http.StatusBadRequest, "validation_error", "url must be an http(s) URL of at most 2000 chars")
			return
		}
		url = *req.URL
	}
	if req.Title != nil {
		title = clampBookmarkTitle(*req.Title)
	}
	if err := s.q.UpdateProjectBookmark(r.Context(), dbpkg.UpdateProjectBookmarkParams{
		Url: url, Title: title, ID: bm.ID, ProjectID: p.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	bm, _ = s.q.GetProjectBookmark(r.Context(), dbpkg.GetProjectBookmarkParams{ID: bid, ProjectID: p.ID})
	writeJSON(w, http.StatusOK, projectBookmarkJSON(bm, dbpkg.ListProjectBookmarksRow{}))
}

// completeBookmarkArchive stores the fetched PDF as a project file and links
// it to the bookmark, replacing (and deleting) any previous archive.
func (s *Server) completeBookmarkArchive(ctx context.Context, j *jobState, key string, size int64) error {
	bm, err := s.q.GetProjectBookmark(ctx, dbpkg.GetProjectBookmarkParams{ID: j.bookmarkID, ProjectID: j.bookmarkProject})
	if err != nil {
		return err // bookmark vanished mid-job: caller deletes the stored file
	}
	file, err := s.q.AddProjectFile(ctx, dbpkg.AddProjectFileParams{
		ProjectID: j.bookmarkProject, StorageKey: key,
		Filename: sanitizeFilename(j.title) + ".pdf", MimeType: "application/pdf",
		SizeBytes: size, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	if err := s.q.SetBookmarkArchive(ctx, dbpkg.SetBookmarkArchiveParams{ArchivedFileID: sqlNullInt64(file.ID), ID: bm.ID}); err != nil {
		s.q.DeleteProjectFile(ctx, dbpkg.DeleteProjectFileParams{ID: file.ID, ProjectID: j.bookmarkProject})
		s.deleteFileBytes(key)
		return err
	}
	if bm.ArchivedFileID.Valid { // re-archive: retire the previous PDF
		old, err := s.q.GetProjectFile(ctx, dbpkg.GetProjectFileParams{ID: bm.ArchivedFileID.Int64, ProjectID: j.bookmarkProject})
		if err == nil {
			if _, err := s.q.DeleteProjectFile(ctx, dbpkg.DeleteProjectFileParams{ID: old.ID, ProjectID: j.bookmarkProject}); err == nil {
				s.deleteFileBytes(old.StorageKey)
			}
		}
	}
	return nil
}

// handleArchiveBookmark queues an archive job (POST
// /projects/{id}/bookmarks/{bid}/archive). Owner-only; 404 for sharees even
// on shared projects (spec §6).
func (s *Server) handleArchiveBookmark(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	bid, err := strconv.ParseInt(chi.URLParam(r, "bid"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid bookmark id")
		return
	}
	bm, err := s.q.GetProjectBookmark(r.Context(), dbpkg.GetProjectBookmarkParams{ID: bid, ProjectID: p.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "bookmark not found")
		return
	}
	if s.fetchArchive == nil {
		writeErr(w, http.StatusServiceUnavailable, "fetching_disabled", "archiving is not available on this server")
		return
	}
	id, err := newJobID()
	if err != nil {
		s.internalErr(w, err)
		return
	}
	j := &jobState{
		id: id, userID: u.ID, kind: "bookmark-archive",
		bookmarkProject: p.ID, bookmarkID: bm.ID, title: bm.Title,
		status: "running", created: time.Now(),
	}
	s.jobsMu.Lock()
	pruneJobsLocked(s.jobs, time.Now())
	s.jobs[id] = j
	s.jobsMu.Unlock()
	// background lifecycle like handleItemFetch: the job outlives the 202
	go s.runJob(context.Background(), j, s.fetchArchive, bm.Url)
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": id})
}

func (s *Server) handleDeleteProjectBookmark(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	bid, err := strconv.ParseInt(chi.URLParam(r, "bid"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid bookmark id")
		return
	}
	bm, err := s.q.GetProjectBookmark(r.Context(), dbpkg.GetProjectBookmarkParams{ID: bid, ProjectID: p.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "bookmark not found")
		return
	}
	rows, err := s.q.DeleteProjectBookmark(r.Context(), dbpkg.DeleteProjectBookmarkParams{ID: bid, ProjectID: p.ID})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if rows == 0 {
		writeErr(w, http.StatusNotFound, "not_found", "bookmark not found")
		return
	}
	if bm.ArchivedFileID.Valid { // also retire the archive row + bytes
		if f, err := s.q.GetProjectFile(r.Context(), dbpkg.GetProjectFileParams{ID: bm.ArchivedFileID.Int64, ProjectID: p.ID}); err == nil {
			if _, err := s.q.DeleteProjectFile(r.Context(), dbpkg.DeleteProjectFileParams{ID: f.ID, ProjectID: p.ID}); err == nil {
				s.deleteFileBytes(f.StorageKey)
				if f.ThumbKey.Valid {
					s.deleteFileBytes(f.ThumbKey.String)
				}
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
