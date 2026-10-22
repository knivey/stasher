package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stasher/internal/db"
)

// png1x1 is a minimal valid PNG (sniffs as image/png).
var png1x1 = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
	0x0A, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

func multipartBody(t *testing.T, field, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func postFile(t *testing.T, ts *httptest.Server, path, field, filename string, content []byte, cookie *http.Cookie) *http.Response {
	t.Helper()
	body, ctype := multipartBody(t, field, filename, content)
	req, _ := http.NewRequest("POST", ts.URL+path, body)
	req.Header.Set("Content-Type", ctype)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestPhotoUploadAndServe(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "photo@test.dev", "longpassword1")
	other := registerUser(t, ts, "other@test.dev", "longpassword1")

	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "photogenic"}, owner)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	resp := postFile(t, ts, "/api/v1/items/"+fmtInt64(created.ID)+"/photos",
		"file", "pic.png", png1x1, owner)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("photo upload = %d", resp.StatusCode)
	}
	var photo struct {
		ID         int64  `json:"id"`
		StorageKey string `json:"storage_key"`
	}
	decodeBody(t, resp, &photo)
	if photo.StorageKey == "" {
		t.Fatal("no storage key returned")
	}

	// GET file as owner
	getURL := func(key string, cookie *http.Cookie) *http.Response {
		req, _ := http.NewRequest("GET", ts.URL+"/files/"+key, nil)
		req.AddCookie(cookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if r := getURL(photo.StorageKey, owner); r.StatusCode != 200 {
		t.Fatalf("owner file get = %d", r.StatusCode)
	} else {
		b, _ := io.ReadAll(r.Body)
		if !bytes.Equal(b, png1x1) {
			t.Fatal("file content mismatch")
		}
		// defense in depth: never let the browser sniff a content type
		if opt := r.Header.Get("X-Content-Type-Options"); opt != "nosniff" {
			t.Fatalf("x-content-type-options = %q, want nosniff", opt)
		}
		// safe image types keep inline serving (R15)
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
			t.Fatalf("photo content-type = %q, want image/png", ct)
		}
		if cd := r.Header.Get("Content-Disposition"); strings.HasPrefix(cd, "attachment") {
			t.Fatalf("photo disposition = %q, want inline", cd)
		}
	}
	// unauthenticated -> 401
	req, _ := http.NewRequest("GET", ts.URL+"/files/"+photo.StorageKey, nil)
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 401 {
		t.Fatalf("anon file get = %d, want 401", r.StatusCode)
	}
	// private item: other user must not read the file (404, not 403)
	if r := getURL(photo.StorageKey, other); r.StatusCode != 404 {
		t.Fatalf("other user private file = %d, want 404", r.StatusCode)
	}
	// share the item -> other user can read
	doJSON(t, ts, "PATCH", "/api/v1/items/"+fmtInt64(created.ID),
		map[string]any{"visibility": "shared"}, owner)
	if r := getURL(photo.StorageKey, other); r.StatusCode != 200 {
		t.Fatalf("other user shared file = %d, want 200", r.StatusCode)
	}

	// path traversal rejected (Review Focus #1)
	req, _ = http.NewRequest("GET", ts.URL+"/files/../../etc/passwd", nil)
	req.AddCookie(owner)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 404 {
		t.Fatalf("traversal = %v %d, want 404", err, r.StatusCode)
	} else if opt := r.Header.Get("X-Content-Type-Options"); opt != "nosniff" {
		t.Fatalf("traversal 404 x-content-type-options = %q, want nosniff", opt)
	}

	// caption/sort patch
	if r := doJSON(t, ts, "PATCH", "/api/v1/items/"+fmtInt64(created.ID)+"/photos/"+fmtInt64(photo.ID),
		map[string]any{"caption": "front view", "sort_order": 5}, owner); r.StatusCode != 200 {
		t.Fatalf("photo patch = %d", r.StatusCode)
	}

	// photo delete removes row and file
	if r := doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(created.ID)+"/photos/"+fmtInt64(photo.ID),
		nil, owner); r.StatusCode != 204 {
		t.Fatalf("photo delete = %d", r.StatusCode)
	}
	if r := getURL(photo.StorageKey, owner); r.StatusCode != 404 {
		t.Fatalf("deleted photo file get = %d, want 404", r.StatusCode)
	}
}

// jpegBytes encodes a real w×h JPEG via image/jpeg, for tests that need
// decodeable (or truncatable) photo bytes.
func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x80, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUploadPhotoGeneratesThumb(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "thumbgen@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "widget"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	resp := postFile(t, ts, "/api/v1/items/"+fmtInt64(created.ID)+"/photos",
		"file", "big.jpg", jpegBytes(t, 1200, 800), cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d", resp.StatusCode)
	}

	photos, err := s.Queries().ListItemPhotos(context.Background(), created.ID)
	if err != nil || len(photos) != 1 {
		t.Fatalf("photos: %v %d", err, len(photos))
	}
	if !photos[0].ThumbKey.Valid || photos[0].ThumbKey.String == "" {
		t.Fatal("thumb_key not set on upload")
	}
	if photos[0].ThumbKey.String == photos[0].StorageKey {
		t.Fatal("thumb_key must be a derived file, not the original")
	}
	// the /items/{id}/photos response surface exposes the derived key too
	var uploaded struct {
		ThumbKey json.RawMessage `json:"thumb_key"`
	}
	decodeBody(t, resp, &uploaded)
	if got := string(uploaded.ThumbKey); got != jsonString(photos[0].ThumbKey.String) {
		t.Fatalf("upload response thumb_key = %s, want %s", got, jsonString(photos[0].ThumbKey.String))
	}
	tf, err := s.Storage().Open(photos[0].ThumbKey.String)
	if err != nil {
		t.Fatalf("thumb file missing: %v", err)
	}
	defer tf.Close()
	cfg, _, err := image.DecodeConfig(tf)
	if err != nil {
		t.Fatalf("thumb undecodable: %v", err)
	}
	if cfg.Width > 480 || cfg.Height > 480 {
		t.Fatalf("thumb %dx%d exceeds 480", cfg.Width, cfg.Height)
	}
}

func TestUploadPhotoCorruptStillSavesOriginal(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "corrupt@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "corrupt"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	// Sniffable-as-jpeg bytes that fail full decode: a valid JPEG prefix
	// followed by truncation.
	full := jpegBytes(t, 100, 100)
	trunc := full[:len(full)/2]
	resp := postFile(t, ts, "/api/v1/items/"+fmtInt64(created.ID)+"/photos",
		"file", "trunc.jpg", trunc, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("corrupt photo upload = %d, want 201", resp.StatusCode)
	}

	photos, err := s.Queries().ListItemPhotos(context.Background(), created.ID)
	if err != nil || len(photos) != 1 {
		t.Fatalf("photos: %v %d", err, len(photos))
	}
	// generation failure degrades to null thumb (lists COALESCE to original)
	if photos[0].ThumbKey.Valid {
		t.Fatalf("thumb_key = %q, want null", photos[0].ThumbKey.String)
	}
	// the original (corrupt-but-sniffable) file must still be stored
	f, err := s.Storage().Open(photos[0].StorageKey)
	if err != nil {
		t.Fatalf("original missing after failed thumb generation: %v", err)
	}
	defer f.Close()
	orig, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(orig, trunc) {
		t.Fatalf("stored original mismatch: %v (%d vs %d bytes)", err, len(orig), len(trunc))
	}
}

// TestDeletePhotoRemovesThumb: deleting a photo removes BOTH the original
// and the derived thumbnail file from disk.
func TestDeletePhotoRemovesThumb(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "del-thumb@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "deletable"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	resp := postFile(t, ts, "/api/v1/items/"+fmtInt64(created.ID)+"/photos",
		"file", "pic.png", png1x1, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("photo upload = %d", resp.StatusCode)
	}
	var photo struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &photo)
	photos, err := s.Queries().ListItemPhotos(context.Background(), created.ID)
	if err != nil || len(photos) != 1 || !photos[0].ThumbKey.Valid {
		t.Fatalf("photos: %v %+v", err, photos)
	}

	if r := doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(created.ID)+"/photos/"+fmtInt64(photo.ID),
		nil, cookie); r.StatusCode != http.StatusNoContent {
		t.Fatalf("photo delete = %d", r.StatusCode)
	}
	for _, key := range []string{photos[0].StorageKey, photos[0].ThumbKey.String} {
		if f, err := s.st.Open(key); err == nil {
			f.Close()
			t.Fatalf("file %q still on disk after photo delete", key)
		}
	}
}

func TestPhotoRejectsNonImage(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "notimg@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "x"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)
	resp := postFile(t, ts, "/api/v1/items/"+fmtInt64(created.ID)+"/photos",
		"file", "evil.sh", []byte("#!/bin/sh\nrm -rf /\n"), cookie)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("non-image photo = %d, want 415", resp.StatusCode)
	}
	// empty file rejected
	resp = postFile(t, ts, "/api/v1/items/"+fmtInt64(created.ID)+"/photos",
		"file", "empty.png", nil, cookie)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty photo = %d, want 400", resp.StatusCode)
	}
}

func TestDocumentUploadAndDelete(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "doc@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "doc item"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	resp := postFile(t, ts, "/api/v1/items/"+fmtInt64(created.ID)+"/documents",
		"file", "datasheet.pdf", []byte("%PDF-1.4 fake pdf bytes"), cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("doc upload = %d", resp.StatusCode)
	}
	var doc struct {
		ID         int64  `json:"id"`
		StorageKey string `json:"storage_key"`
		Filename   string `json:"filename"`
		MimeType   string `json:"mime_type"`
	}
	decodeBody(t, resp, &doc)
	if doc.Filename != "datasheet.pdf" {
		t.Fatalf("filename = %q", doc.Filename)
	}
	// R15 addendum: %PDF magic bytes must store application/pdf (the sniff
	// alone returns application/octet-stream) so serve-side isSafeInline
	// renders real PDFs inline instead of forcing a download.
	if doc.MimeType != "application/pdf" {
		t.Fatalf("stored mime = %q, want application/pdf", doc.MimeType)
	}
	// owner GET: served inline as application/pdf
	pdfReq, _ := http.NewRequest("GET", ts.URL+"/files/"+doc.StorageKey, nil)
	pdfReq.AddCookie(cookie)
	if r, _ := http.DefaultClient.Do(pdfReq); r.StatusCode != 200 {
		t.Fatalf("owner pdf file get = %d, want 200", r.StatusCode)
	} else {
		if ct := r.Header.Get("Content-Type"); ct != "application/pdf" {
			t.Fatalf("pdf content-type = %q, want application/pdf", ct)
		}
		if cd := r.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "inline") {
			t.Fatalf("pdf disposition = %q, want inline", cd)
		}
		r.Body.Close()
	}
	// file exists on disk
	f, err := s.st.Open(doc.StorageKey)
	if err != nil {
		t.Fatalf("stored doc not readable: %v", err)
	}
	f.Close()
	// path component stripped
	resp = postFile(t, ts, "/api/v1/items/"+fmtInt64(created.ID)+"/documents",
		"file", "../../etc/cron", []byte("junk"), cookie)
	var doc2 struct {
		Filename string `json:"filename"`
	}
	decodeBody(t, resp, &doc2)
	if doc2.Filename == "../../etc/cron" || doc2.Filename != "cron" {
		t.Fatalf("unsanitized filename: %q", doc2.Filename)
	}
	// delete
	if r := doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(created.ID)+"/documents/"+fmtInt64(doc.ID),
		nil, cookie); r.StatusCode != 204 {
		t.Fatalf("doc delete = %d", r.StatusCode)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/files/"+doc.StorageKey, nil)
	req.AddCookie(cookie)
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 404 {
		t.Fatalf("deleted doc file get = %d, want 404", r.StatusCode)
	}
}

// TestBackfillThumbs: photos uploaded before thumb generation existed (row
// with NULL thumb_key, real file on disk) get a derived thumbnail on the
// startup backfill; a second run is a no-op.
func TestBackfillThumbs(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "backfill@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "old photo item"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	// simulate a pre-migration photo: file on disk, row with NULL thumb
	src := jpegBytes(t, 900, 700)
	key, _, err := s.Storage().Save(bytes.NewReader(src), "jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Queries().AddItemPhoto(context.Background(), db.AddItemPhotoParams{
		ItemID: created.ID, StorageKey: key, ThumbKey: sql.NullString{},
		Caption: "", SortOrder: 0, CreatedAt: timeNow(t),
	}); err != nil {
		t.Fatal(err)
	}

	s.BackfillThumbs(context.Background())

	photos, err := s.Queries().ListItemPhotos(context.Background(), created.ID)
	if err != nil || len(photos) != 1 {
		t.Fatalf("photos: %v %d", err, len(photos))
	}
	if !photos[0].ThumbKey.Valid || photos[0].ThumbKey.String == "" {
		t.Fatal("backfill did not set thumb_key")
	}
	if photos[0].ThumbKey.String == photos[0].StorageKey {
		t.Fatal("thumb_key must be a derived file, not the original")
	}
	tf, err := s.Storage().Open(photos[0].ThumbKey.String)
	if err != nil {
		t.Fatalf("thumb file missing: %v", err)
	}
	defer tf.Close()
	cfg, _, err := image.DecodeConfig(tf)
	if err != nil {
		t.Fatalf("thumb undecodable: %v", err)
	}
	if cfg.Width > 480 || cfg.Height > 480 {
		t.Fatalf("thumb %dx%d exceeds 480", cfg.Width, cfg.Height)
	}

	// second run is a no-op: nothing missing, thumb key unchanged
	first := photos[0].ThumbKey.String
	s.BackfillThumbs(context.Background())
	missing, err := s.Queries().ListPhotosMissingThumbs(context.Background())
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing after second run: %v %d", err, len(missing))
	}
	photos, _ = s.Queries().ListItemPhotos(context.Background(), created.ID)
	if len(photos) != 1 || photos[0].ThumbKey.String != first {
		t.Fatalf("second run changed thumb: %+v", photos)
	}
}

// TestBackfillThumbsSkipsCorrupt: a photo whose stored bytes are undecodable
// keeps its NULL thumb (lists COALESCE to the original), the run does not
// panic, and other rows are still processed.
func TestBackfillThumbsSkipsCorrupt(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "backfill-corrupt@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "mixed"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	addLegacyPhoto := func(content []byte) int64 {
		t.Helper()
		key, _, err := s.Storage().Save(bytes.NewReader(content), "jpg")
		if err != nil {
			t.Fatal(err)
		}
		p, err := s.Queries().AddItemPhoto(context.Background(), db.AddItemPhotoParams{
			ItemID: created.ID, StorageKey: key, ThumbKey: sql.NullString{},
			Caption: "", SortOrder: 0, CreatedAt: timeNow(t),
		})
		if err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	// corrupt first (lower id: processed first, must not stop the loop)
	badID := addLegacyPhoto([]byte("definitely not a jpeg, just garbage bytes"))
	goodID := addLegacyPhoto(jpegBytes(t, 1200, 300))

	s.BackfillThumbs(context.Background())

	photos, err := s.Queries().ListItemPhotos(context.Background(), created.ID)
	if err != nil || len(photos) != 2 {
		t.Fatalf("photos: %v %d", err, len(photos))
	}
	byID := map[int64]db.ItemPhoto{}
	for _, p := range photos {
		byID[p.ID] = p
	}
	if byID[badID].ThumbKey.Valid {
		t.Fatalf("corrupt photo got thumb_key %q, want NULL", byID[badID].ThumbKey.String)
	}
	if !byID[goodID].ThumbKey.Valid {
		t.Fatal("corrupt row prevented processing of the good row")
	}
	// still true after another pass (idempotent skip, no panic)
	s.BackfillThumbs(context.Background())
	photos, _ = s.Queries().ListItemPhotos(context.Background(), created.ID)
	for _, p := range photos {
		if p.ID == badID && p.ThumbKey.Valid {
			t.Fatal("corrupt photo gained a thumb on the second run")
		}
	}
}

// TestBackfillThumbsCanceledContext: a canceled context leaves rows untouched
// (the backfill bails out instead of doing work or panicking).
func TestBackfillThumbsCanceledContext(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "backfill-cancel@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "cancel"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	key, _, err := s.Storage().Save(bytes.NewReader(jpegBytes(t, 900, 700)), "jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Queries().AddItemPhoto(context.Background(), db.AddItemPhotoParams{
		ItemID: created.ID, StorageKey: key, ThumbKey: sql.NullString{},
		Caption: "", SortOrder: 0, CreatedAt: timeNow(t),
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.BackfillThumbs(ctx)

	photos, err := s.Queries().ListItemPhotos(context.Background(), created.ID)
	if err != nil || len(photos) != 1 {
		t.Fatalf("photos: %v %d", err, len(photos))
	}
	if photos[0].ThumbKey.Valid {
		t.Fatal("canceled context must not produce thumbs")
	}
}

// TestDocumentActiveContentNotInline covers R15: a shared document that
// sniffs as text/html must be served as a download, never rendered inline
// on the app origin (stored-XSS guard).
func TestDocumentActiveContentNotInline(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "xsstest@test.dev", "longpassword1")
	other := registerUser(t, ts, "xssother@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "xss item"}, owner)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	resp := postFile(t, ts, "/api/v1/items/"+fmtInt64(created.ID)+"/documents",
		"file", "evil.html", []byte("<!doctype html><script>alert(1)</script>"), owner)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("doc upload = %d", resp.StatusCode)
	}
	var doc struct {
		StorageKey string `json:"storage_key"`
		MimeType   string `json:"mime_type"`
	}
	decodeBody(t, resp, &doc)
	// stored mime is the SNIFFED type; no client Content-Type override (R15)
	if !strings.HasPrefix(doc.MimeType, "text/html") {
		t.Fatalf("stored mime = %q, want sniffed text/html", doc.MimeType)
	}

	// share the item so another user can fetch the file
	doJSON(t, ts, "PATCH", "/api/v1/items/"+fmtInt64(created.ID),
		map[string]any{"visibility": "shared"}, owner)
	req, _ := http.NewRequest("GET", ts.URL+"/files/"+doc.StorageKey, nil)
	req.AddCookie(other)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("other user shared doc get = %d, want 200", r.StatusCode)
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/octet-stream") {
		t.Fatalf("content-type = %q, want application/octet-stream", ct)
	}
	if cd := r.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("content-disposition = %q, want attachment", cd)
	}
}

// uploadPhotosWithOrders uploads n photos via the API and PATCHes each to
// sort_order 0..n-1 in upload order (uploads all land at 0). Returns ids.
func uploadPhotosWithOrders(t *testing.T, ts *httptest.Server, itemID int64, cookie *http.Cookie, n int) []int64 {
	t.Helper()
	var ids []int64
	for i := 0; i < n; i++ {
		resp := postFile(t, ts, "/api/v1/items/"+fmtInt64(itemID)+"/photos",
			"file", "pic.jpg", jpegBytes(t, 600, 400), cookie)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("upload %d = %d", i, resp.StatusCode)
		}
		var ph struct {
			ID int64 `json:"id"`
		}
		decodeBody(t, resp, &ph)
		if r := doJSON(t, ts, "PATCH", "/api/v1/items/"+fmtInt64(itemID)+"/photos/"+fmtInt64(ph.ID),
			map[string]any{"sort_order": i}, cookie); r.StatusCode != http.StatusOK {
			t.Fatalf("patch sort %d = %d", i, r.StatusCode)
		}
		ids = append(ids, ph.ID)
	}
	return ids
}

// photoSortsByID returns the item's photos as id -> sort_order.
func photoSortsByID(t *testing.T, s *Server, itemID int64) map[int64]int64 {
	t.Helper()
	photos, err := s.Queries().ListItemPhotos(context.Background(), itemID)
	if err != nil {
		t.Fatal(err)
	}
	m := make(map[int64]int64, len(photos))
	for _, p := range photos {
		m[p.ID] = p.SortOrder
	}
	return m
}

func TestMakePhotoMain(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "mainpick@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "pick me"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	ids := uploadPhotosWithOrders(t, ts, created.ID, cookie, 3) // A, B, C => 0, 1, 2
	aID, bID, cID := ids[0], ids[1], ids[2]

	if r := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(created.ID)+"/photos/"+fmtInt64(cID)+"/main",
		nil, cookie); r.StatusCode != http.StatusNoContent {
		t.Fatalf("make main = %d, want 204", r.StatusCode)
	}

	// the list thumbnail follows the new main photo: C's derived thumb
	photos, err := s.Queries().ListItemPhotos(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	cThumb := ""
	for _, p := range photos {
		if p.ID == cID {
			cThumb = p.ThumbKey.String
		}
	}
	if cThumb == "" {
		t.Fatal("C has no derived thumb")
	}
	list := doJSON(t, ts, "GET", "/api/v1/items", nil, cookie)
	var items struct {
		Data []struct {
			ThumbKey string `json:"thumb_key"`
		} `json:"data"`
	}
	decodeBody(t, list, &items)
	if len(items.Data) != 1 || items.Data[0].ThumbKey != cThumb {
		t.Fatalf("list thumb_key = %+v, want C's derived thumb %q", items.Data, cThumb)
	}

	// detail photos: C, A, B with sort_order 0, 1, 2
	detail := doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(created.ID), nil, cookie)
	var det struct {
		Photos []struct {
			ID        int64 `json:"id"`
			SortOrder int64 `json:"sort_order"`
		} `json:"photos"`
	}
	decodeBody(t, detail, &det)
	wantIDs := []int64{cID, aID, bID}
	if len(det.Photos) != len(wantIDs) {
		t.Fatalf("detail photos = %d, want %d", len(det.Photos), len(wantIDs))
	}
	for i, p := range det.Photos {
		if p.ID != wantIDs[i] || p.SortOrder != int64(i) {
			t.Fatalf("detail photos[%d] = {id %d, sort %d}, want {id %d, sort %d}",
				i, p.ID, p.SortOrder, wantIDs[i], i)
		}
	}
}

func TestMakePhotoMainNoop(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "mainnoop@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "noop"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	ids := uploadPhotosWithOrders(t, ts, created.ID, cookie, 3) // A, B, C => 0, 1, 2

	if r := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(created.ID)+"/photos/"+fmtInt64(ids[0])+"/main",
		nil, cookie); r.StatusCode != http.StatusNoContent {
		t.Fatalf("make main on already-first = %d, want 204", r.StatusCode)
	}

	// orders unchanged: UpdateItemPhoto never rewrote anything
	got := photoSortsByID(t, s, created.ID)
	for i, id := range ids {
		if got[id] != int64(i) {
			t.Fatalf("photo %d sort_order = %d, want %d (unchanged)", id, got[id], i)
		}
	}
}

func TestMakePhotoMainForeignPhoto(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "mainforeign@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "foreign"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(created.ID)+"/photos/999999/main",
		nil, cookie)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("absent photo main = %d, want 404", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, resp, &body)
	if body.Error.Code != "not_found" {
		t.Fatalf("absent photo code = %q, want not_found", body.Error.Code)
	}

	// non-numeric photo id -> 400 invalid_id
	resp = doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(created.ID)+"/photos/abc/main",
		nil, cookie)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid photo id = %d, want 400", resp.StatusCode)
	}
	decodeBody(t, resp, &body)
	if body.Error.Code != "invalid_id" {
		t.Fatalf("invalid photo id code = %q, want invalid_id", body.Error.Code)
	}
}

// TestMakePhotoMainDeterministicWithDuplicateSorts: three photos all at
// sort_order 0 (inserted directly) renumber deterministically — the target
// takes 0, the rest keep their (sort_order, id) order regardless of ties.
func TestMakePhotoMainDeterministicWithDuplicateSorts(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "maindup@test.dev", "longpassword1")
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "dups"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)

	var ids []int64 // ascending ids: p1 < p2 < p3, all sort_order 0
	for i := 0; i < 3; i++ {
		key, _, err := s.Storage().Save(bytes.NewReader(jpegBytes(t, 600, 400)), "jpg")
		if err != nil {
			t.Fatal(err)
		}
		p, err := s.Queries().AddItemPhoto(context.Background(), db.AddItemPhotoParams{
			ItemID: created.ID, StorageKey: key, ThumbKey: sql.NullString{},
			Caption: "", SortOrder: 0, CreatedAt: timeNow(t),
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}

	// main on the LAST by id: target -> 0; p1, p2 keep (sort, id) order -> 1, 2
	if r := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(created.ID)+"/photos/"+fmtInt64(ids[2])+"/main",
		nil, cookie); r.StatusCode != http.StatusNoContent {
		t.Fatalf("make main = %d, want 204", r.StatusCode)
	}

	got := photoSortsByID(t, s, created.ID)
	want := map[int64]int64{ids[2]: 0, ids[0]: 1, ids[1]: 2}
	for id, sort := range want {
		if got[id] != sort {
			t.Fatalf("photo %d sort_order = %d, want %d", id, got[id], sort)
		}
	}
}
