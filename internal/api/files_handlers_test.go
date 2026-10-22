package api

import (
	"bytes"
	"context"
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
