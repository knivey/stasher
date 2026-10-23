package api

import (
	"bytes"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
	"stasher/internal/thumbs"
)

// requireOwnedLocation writes 404 unless the location belongs to the user;
// returns it (the fetched row also carries the CURRENT photo keys, which
// the photo handlers need for their file cleanup).
func (s *Server) requireOwnedLocation(w http.ResponseWriter, r *http.Request, u dbpkg.User) (dbpkg.Location, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid location id")
		return dbpkg.Location{}, false
	}
	loc, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "location not found")
		return dbpkg.Location{}, false
	}
	return loc, true
}

// handleUploadLocationPhoto sets a location's single picture (replace on
// re-upload). Upload rules mirror handleUploadPhoto exactly: maxPhotoBytes
// cap, content sniffing, image-only, EXIF-aware thumb generation that
// degrades to a null thumb. Crash-safe replace ordering: validate → save
// NEW files → point the row at them → delete the OLD files; a crash can
// orphan bytes on disk but never leaves the row pointing at a missing file.
func (s *Server) handleUploadLocationPhoto(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	loc, ok := s.requireOwnedLocation(w, r, u) // 404 foreign/absent first
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_upload", "bad multipart body or file too large")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing_file", "file form field is required")
		return
	}
	defer f.Close()

	head := make([]byte, 512)
	// io.ReadFull: short reads (small files) are fine, real read errors
	// must not produce a truncated sniff buffer (same as item photos)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		writeErr(w, http.StatusBadRequest, "invalid_upload", "bad upload body")
		return
	}
	head = head[:n]
	if n == 0 {
		writeErr(w, http.StatusBadRequest, "empty_file", "file is empty")
		return
	}
	ct := http.DetectContentType(head)
	if !allowedPhotoTypes[ct] {
		writeErr(w, http.StatusUnsupportedMediaType, "unsupported_type",
			"photos must be jpeg, png, webp, or gif")
		return
	}
	rest, err := io.ReadAll(f)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_upload", "bad multipart body or file too large")
		return
	}
	buf := append(head, rest...)

	// bare single-token extension from the SNIFFED type — never a client filename
	key, _, err := s.st.Save(bytes.NewReader(buf), extForMime(ct))
	if err != nil {
		s.internalErr(w, err)
		return
	}

	// derived thumbnail; failure degrades to null (the row keeps the original)
	var thumbKey sql.NullString
	if tb, ext, err := thumbs.Generate(buf); err != nil {
		slog.Warn("thumb generation failed", "err", err)
	} else if tkey, _, err := s.st.Save(bytes.NewReader(tb), ext); err != nil {
		slog.Warn("thumb save failed", "err", err)
	} else {
		thumbKey = sql.NullString{String: tkey, Valid: true}
	}

	if err := s.q.SetLocationPhoto(r.Context(), dbpkg.SetLocationPhotoParams{
		PhotoKey: sqlNullString(key), PhotoThumbKey: thumbKey,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		ID:        loc.ID, UserID: u.ID,
	}); err != nil {
		// row update failed: roll back the NEW files we already saved
		if derr := s.st.Delete(key); derr != nil {
			slog.Warn("file delete failed", "key", key, "err", derr)
		}
		if thumbKey.Valid {
			if derr := s.st.Delete(thumbKey.String); derr != nil {
				slog.Warn("file delete failed", "key", thumbKey.String, "err", derr)
			}
		}
		s.internalErr(w, err)
		return
	}

	// Success: the old picture's files are unreferenced now — best-effort
	// delete (a failed delete leaks bytes, it is not a request error).
	deleteLocationPhotoFiles(s, &loc)

	writeJSON(w, http.StatusOK, map[string]any{
		"photo_key": key, "thumb_key": thumbJSON(thumbKey),
	})
}

// handleDeleteLocationPhoto clears the location's picture. Idempotent: a
// location without a picture still answers 204 — without rewriting the row,
// so a repeat delete leaves updated_at untouched (a foreign location still
// 404s first — no existence leaks).
func (s *Server) handleDeleteLocationPhoto(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	loc, ok := s.requireOwnedLocation(w, r, u)
	if !ok {
		return
	}
	if !loc.PhotoKey.Valid { // no picture: skip the write entirely
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.q.ClearLocationPhoto(r.Context(), dbpkg.ClearLocationPhotoParams{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		ID:        loc.ID, UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	// columns are already NULL: a failed file delete only leaks bytes
	deleteLocationPhotoFiles(s, &loc)
	w.WriteHeader(http.StatusNoContent)
}

// deleteLocationPhotoFiles removes a location row's picture files from
// storage, warn-logging failures. Missing files are a no-op (Storage.Delete
// tolerates absence), which keeps photo deletion idempotent.
func deleteLocationPhotoFiles(s *Server, loc *dbpkg.Location) {
	if loc.PhotoKey.Valid {
		if err := s.st.Delete(loc.PhotoKey.String); err != nil {
			slog.Warn("file delete failed", "key", loc.PhotoKey.String, "err", err)
		}
	}
	if loc.PhotoThumbKey.Valid {
		if err := s.st.Delete(loc.PhotoThumbKey.String); err != nil {
			slog.Warn("file delete failed", "key", loc.PhotoThumbKey.String, "err", err)
		}
	}
}
