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
	"stasher/internal/files"
	"stasher/internal/thumbs"
)

var allowedPhotoTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true,
}

// extForMime returns a safe extension for sniffed photo types.
func extForMime(ct string) string {
	switch ct {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	}
	return "bin"
}

// requireOwnedItem writes 404 unless the item belongs to the user; returns it.
func (s *Server) requireOwnedItem(w http.ResponseWriter, r *http.Request, u dbpkg.User) (dbpkg.Item, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid item id")
		return dbpkg.Item{}, false
	}
	item, err := s.q.GetItem(r.Context(), dbpkg.GetItemParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "item not found")
		return dbpkg.Item{}, false
	}
	return item, true
}

const maxPhotoBytes = 20 << 20
const maxDocBytes = 50 << 20

func (s *Server) handleUploadPhoto(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	item, ok := s.requireOwnedItem(w, r, u)
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
	n, _ := f.Read(head)
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
	// buffer the rest (bounded by maxPhotoBytes) so the thumb generator can
	// re-read the bytes
	rest, err := io.ReadAll(f)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_upload", "bad multipart body or file too large")
		return
	}
	buf := append(head, rest...)

	key, _, err := s.st.Save(bytes.NewReader(buf), extForMime(ct))
	if err != nil {
		s.internalErr(w, err)
		return
	}

	// derived thumbnail; failure degrades to null (lists COALESCE to original)
	var thumbKey sql.NullString
	if tb, ext, err := thumbs.Generate(buf); err != nil {
		slog.Warn("thumb generation failed", "err", err)
	} else if tkey, _, err := s.st.Save(bytes.NewReader(tb), ext); err != nil {
		slog.Warn("thumb save failed", "err", err)
	} else {
		thumbKey = sql.NullString{String: tkey, Valid: true}
	}

	photo, err := s.q.AddItemPhoto(r.Context(), dbpkg.AddItemPhotoParams{
		ItemID: item.ID, StorageKey: key, ThumbKey: thumbKey, Caption: "", SortOrder: 0,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.st.Delete(key)
		s.st.Delete(thumbKey.String)
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": photo.ID, "storage_key": photo.StorageKey, "thumb_key": thumbJSON(photo.ThumbKey),
		"caption":    photo.Caption,
		"sort_order": photo.SortOrder, "created_at": photo.CreatedAt,
	})
}

type photoPatchReq struct {
	Caption   *string `json:"caption"`
	SortOrder *int64  `json:"sort_order"`
}

func (s *Server) handlePatchPhoto(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	item, ok := s.requireOwnedItem(w, r, u)
	if !ok {
		return
	}
	photoID, err := strconv.ParseInt(chi.URLParam(r, "photoId"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid photo id")
		return
	}
	photos, err := s.q.ListItemPhotos(r.Context(), item.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	var target *dbpkg.ItemPhoto
	for i := range photos {
		if photos[i].ID == photoID {
			target = &photos[i]
			break
		}
	}
	if target == nil {
		writeErr(w, http.StatusNotFound, "not_found", "photo not found")
		return
	}
	var req photoPatchReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Caption != nil {
		target.Caption = *req.Caption
	}
	if req.SortOrder != nil {
		target.SortOrder = *req.SortOrder
	}
	if err := s.q.UpdateItemPhoto(r.Context(), dbpkg.UpdateItemPhotoParams{
		Caption: target.Caption, SortOrder: target.SortOrder, ID: photoID, ItemID: item.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": target.ID, "storage_key": target.StorageKey, "caption": target.Caption,
		"sort_order": target.SortOrder,
	})
}

func (s *Server) handleDeletePhoto(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	item, ok := s.requireOwnedItem(w, r, u)
	if !ok {
		return
	}
	photoID, err := strconv.ParseInt(chi.URLParam(r, "photoId"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid photo id")
		return
	}
	photos, _ := s.q.ListItemPhotos(r.Context(), item.ID)
	var key string
	for _, p := range photos {
		if p.ID == photoID {
			key = p.StorageKey
			break
		}
	}
	if key == "" {
		writeErr(w, http.StatusNotFound, "not_found", "photo not found")
		return
	}
	if err := s.q.DeleteItemPhoto(r.Context(), dbpkg.DeleteItemPhotoParams{ID: photoID, ItemID: item.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	s.st.Delete(key)
	w.WriteHeader(http.StatusNoContent)
}

// sanitizeFilename keeps only the base name and safe characters.
func sanitizeFilename(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == "/" || name == "" {
		return "file"
	}
	return name[:min(len(name), 255)]
}

func (s *Server) handleUploadDocument(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	item, ok := s.requireOwnedItem(w, r, u)
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
	n, _ := f.Read(head)
	if n == 0 {
		writeErr(w, http.StatusBadRequest, "empty_file", "file is empty")
		return
	}
	head = head[:n]
	// R15: stored mime is always the sniffed type; client Content-Type is
	// never trusted (it allowed text/html injection on the app origin).
	ct := http.DetectContentType(head)
	// R15 addendum: force the stored mime for %PDF magic bytes. Go's sniff
	// table currently includes "%PDF-", but rely on that implicitly and a
	// runtime without the signature stores application/octet-stream, which
	// serve-side isSafeInline downgrades to an attachment download. PDFs
	// are safe inline (browser viewers sandbox PDF JS).
	if bytes.HasPrefix(head, []byte("%PDF-")) {
		ct = "application/pdf"
	}
	// re-feed the sniffed head, then the rest of the file from where the
	// head-read left f positioned (Seek(0) here would duplicate the head)
	key, size, err := s.st.Save(io.MultiReader(bytes.NewReader(head), f), ext)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	doc, err := s.q.AddItemDocument(r.Context(), dbpkg.AddItemDocumentParams{
		ItemID: item.ID, StorageKey: key, Filename: filename,
		MimeType: ct, SizeBytes: size,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.st.Delete(key)
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": doc.ID, "storage_key": doc.StorageKey, "filename": doc.Filename,
		"mime_type": doc.MimeType, "size_bytes": doc.SizeBytes, "created_at": doc.CreatedAt,
	})
}

func (s *Server) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	item, ok := s.requireOwnedItem(w, r, u)
	if !ok {
		return
	}
	docID, err := strconv.ParseInt(chi.URLParam(r, "docId"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid document id")
		return
	}
	docs, _ := s.q.ListItemDocuments(r.Context(), item.ID)
	var key string
	for _, d := range docs {
		if d.ID == docID {
			key = d.StorageKey
			break
		}
	}
	if key == "" {
		writeErr(w, http.StatusNotFound, "not_found", "document not found")
		return
	}
	if err := s.q.DeleteItemDocument(r.Context(), dbpkg.DeleteItemDocumentParams{ID: docID, ItemID: item.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	s.st.Delete(key)
	w.WriteHeader(http.StatusNoContent)
}

// isSafeInline reports whether ct may be rendered inline without scripting risk.
func isSafeInline(ct string) bool {
	return strings.HasPrefix(ct, "image/") ||
		ct == "application/pdf" ||
		strings.HasPrefix(ct, "text/plain")
}

// handleFileGet serves /files/{key} to the owner, or to any user if the
// owning item is shared.
func (s *Server) handleFileGet(w http.ResponseWriter, r *http.Request) {
	// Set on every /files/* response (hits and misses): never let the
	// browser sniff a content type on this origin.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	u, _ := currentUser(r.Context())
	key := chi.URLParam(r, "*")
	if !files.ValidKey(key) {
		writeErr(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	ownerID, visibility, ct, filename := int64(0), "", "", ""
	if row, err := s.q.GetPhotoByKey(r.Context(), key); err == nil {
		ownerID, visibility = row.UserID, row.Visibility // ct sniffed at serve time below
	} else if row, err := s.q.GetDocumentByKey(r.Context(), key); err == nil {
		ownerID, visibility, ct, filename = row.UserID, row.Visibility, row.MimeType, row.Filename
	} else {
		writeErr(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if ownerID != u.ID && visibility != "shared" {
		writeErr(w, http.StatusNotFound, "not_found", "file not found") // 404, not 403: no existence leak
		return
	}
	f, err := s.st.Open(key)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	defer f.Close()
	if ct == "" || strings.HasPrefix(ct, "application/octet-stream") {
		head := make([]byte, 512)
		n, _ := f.Read(head)
		ct = http.DetectContentType(head[:n])
		f.Seek(0, io.SeekStart)
	}
	// R15: active content types are never rendered inline on this origin —
	// unsafe types download as octet-stream instead. Applies to both the
	// photo (serve-time sniff) and document (stored mime) paths.
	disposition := "inline"
	if !isSafeInline(ct) {
		ct = "application/octet-stream"
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if filename != "" {
		w.Header().Set("Content-Disposition", disposition+`; filename="`+strings.ReplaceAll(filename, `"`, "")+`"`)
	} else if disposition == "attachment" {
		w.Header().Set("Content-Disposition", "attachment")
	}
	io.Copy(w, f)
}
