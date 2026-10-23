package api

import (
	"bytes"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
	"stasher/internal/thumbs"
)

// handleUploadProjectFile stores one reference material on a project. Any
// file type is accepted (50MB cap, doc rules); sniffed images additionally
// get a derived thumbnail that degrades to null on failure (photo rules).
func (s *Server) handleUploadProjectFile(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDocBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_upload", "bad multipart body or file too large")
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing_file", "file form field is required")
		return
	}
	defer f.Close()

	filename := sanitizeFilename(header.Filename)
	ext := strings.TrimPrefix(path.Ext(filename), ".")
	if ext == "" || len(ext) > 8 {
		ext = "bin"
	}
	head := make([]byte, 512)
	// same hardening as the document path: short reads (small files) are
	// fine, real read errors 400 instead of sniffing a truncated buffer
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		writeErr(w, http.StatusBadRequest, "invalid_upload", "bad upload body")
		return
	}
	if n == 0 {
		writeErr(w, http.StatusBadRequest, "empty_file", "file is empty")
		return
	}
	head = head[:n]
	// stored mime is always the sniffed type; client Content-Type is never
	// trusted (R15), with the %PDF magic-bytes override
	ct := http.DetectContentType(head)
	if bytes.HasPrefix(head, []byte("%PDF-")) {
		ct = "application/pdf"
	}

	// images: buffer (bounded by the already-tripped MaxBytesReader) so the
	// thumb generator can re-read; everything else streams (doc pattern).
	var thumbKey sql.NullString
	var reader io.Reader = io.MultiReader(bytes.NewReader(head), f)
	if strings.HasPrefix(ct, "image/") {
		rest, err := io.ReadAll(f)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_upload", "bad upload body")
			return
		}
		buf := append(head, rest...)
		// derived thumbnail; failure degrades to null (never the request)
		if tb, text, err := thumbs.Generate(buf); err != nil {
			slog.Warn("thumb generation failed", "err", err)
		} else if tkey, _, err := s.st.Save(bytes.NewReader(tb), text); err != nil {
			slog.Warn("thumb save failed", "err", err)
		} else {
			thumbKey = sql.NullString{String: tkey, Valid: true}
		}
		reader = bytes.NewReader(buf)
	}

	key, size, err := s.st.Save(reader, ext)
	if err != nil {
		s.st.Delete(thumbKey.String) // no-op when empty
		s.internalErr(w, err)
		return
	}
	row, err := s.q.AddProjectFile(r.Context(), dbpkg.AddProjectFileParams{
		ProjectID: p.ID, StorageKey: key, ThumbKey: thumbKey, Filename: filename,
		MimeType: ct, SizeBytes: size,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		// row insert failed: roll back the files we already saved
		s.st.Delete(key)
		s.st.Delete(thumbKey.String)
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, projectFileJSON(row))
}

func projectFileJSON(f dbpkg.ProjectFile) map[string]any {
	return map[string]any{
		"id": f.ID, "storage_key": f.StorageKey, "thumb_key": thumbJSON(f.ThumbKey),
		"filename": f.Filename, "mime_type": f.MimeType, "size_bytes": f.SizeBytes,
		"created_at": f.CreatedAt,
	}
}

func (s *Server) handleDeleteProjectFile(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	p, ok := s.requireOwnedProject(w, r, u)
	if !ok {
		return
	}
	fid, err := strconv.ParseInt(chi.URLParam(r, "fid"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid file id")
		return
	}
	row, err := s.q.GetProjectFile(r.Context(), dbpkg.GetProjectFileParams{ID: fid, ProjectID: p.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if _, err := s.q.DeleteProjectFile(r.Context(), dbpkg.DeleteProjectFileParams{ID: fid, ProjectID: p.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	s.deleteFileBytes(row.StorageKey)
	if row.ThumbKey.Valid {
		s.deleteFileBytes(row.ThumbKey.String)
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteFileBytes warns (never 500s) — bytes gone missing is an orphan, not
// a failure of the row deletion that already succeeded.
func (s *Server) deleteFileBytes(key string) {
	if err := s.st.Delete(key); err != nil {
		slog.Warn("file delete failed", "key", key, "err", err)
	}
}
