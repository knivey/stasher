package api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// pngBytes returns a minimal valid PNG for project-file uploads (the same
// tiny-PNG the item photo tests use).
func pngBytes(t *testing.T) []byte {
	t.Helper()
	return png1x1
}

// uploadProjectFile wraps the multipart POST and decodes the 201 row.
func uploadProjectFile(t *testing.T, ts *httptest.Server, cookie *http.Cookie, pid, filename string, content []byte) map[string]any {
	t.Helper()
	resp := postFile(t, ts, "/api/v1/projects/"+pid+"/files", "file", filename, content, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("project file upload = %d, want 201", resp.StatusCode)
	}
	var row map[string]any
	decodeBody(t, resp, &row)
	return row
}

// uploadRawProjectFile returns the raw upload response (status asserted by
// the caller).
func uploadRawProjectFile(t *testing.T, ts *httptest.Server, cookie *http.Cookie, pid, filename string, content []byte) *http.Response {
	t.Helper()
	return postFile(t, ts, "/api/v1/projects/"+pid+"/files", "file", filename, content, cookie)
}

func TestProjectFilesLifecycle(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pf1@test.dev", "longpassword1")
	sharee := registerUser(t, ts, "pf2@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "F", "visibility": "shared"}, owner)
	var proj struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)

	// upload an image → thumb_key set; upload a text file → null thumb
	imgRow := uploadProjectFile(t, ts, owner, pid, "diagram.png", pngBytes(t))
	if imgRow["thumb_key"] == nil {
		t.Fatal("image upload missing thumb_key")
	}
	txtRow := uploadProjectFile(t, ts, owner, pid, "notes.txt", []byte("hello"))
	if txtRow["thumb_key"] != nil {
		t.Fatal("text upload has thumb_key")
	}
	if r := uploadRawProjectFile(t, ts, sharee, pid, "x.txt", []byte("no")); r.StatusCode != http.StatusNotFound {
		t.Fatalf("sharee upload = %d, want 404", r.StatusCode)
	}

	// detail carries files for BOTH views
	var detail map[string]any
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+pid, nil, sharee), &detail)
	if files := detail["files"].([]any); len(files) != 2 {
		t.Fatalf("sharee files = %d, want 2", len(files))
	}

	// gate: sharee fetches image bytes (shared project) → 200; private → 404
	key := imgRow["storage_key"].(string)
	if r := getURL(t, ts, key, sharee); r.StatusCode != http.StatusOK {
		t.Fatalf("sharee file GET = %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
	doJSON(t, ts, "PATCH", "/api/v1/projects/"+pid, map[string]any{"visibility": "private"}, owner)
	if r := getURL(t, ts, key, sharee); r.StatusCode != http.StatusNotFound {
		t.Fatalf("private-project file GET = %d, want 404", r.StatusCode)
	}
	doJSON(t, ts, "PATCH", "/api/v1/projects/"+pid, map[string]any{"visibility": "shared"}, owner)

	// delete removes row + bytes
	fid := int64(imgRow["id"].(float64))
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid+"/files/"+fmtInt64(fid), nil, owner); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete file = %d", r.StatusCode)
	}
	if r := getURL(t, ts, key, owner); r.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted file still served: %d", r.StatusCode)
	}
}

// Ported from TestUploadPhotoCorruptStillSavesOriginal (item photos): a
// sniffable-but-undecodable image still stores the original bytes on a
// project; thumb generation degrades to null thumb_key (never fails the
// upload), and lists COALESCE back to the original.
func TestUploadProjectFileCorruptStillSavesOriginal(t *testing.T) {
	s, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pf4@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Corrupt"}, owner)
	var proj struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)

	// Sniffable-as-jpeg bytes that fail full decode: a valid JPEG prefix
	// followed by truncation.
	full := jpegBytes(t, 100, 100)
	trunc := full[:len(full)/2]
	r := uploadRawProjectFile(t, ts, owner, pid, "trunc.jpg", trunc)
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("corrupt project file upload = %d, want 201", r.StatusCode)
	}
	var row map[string]any
	decodeBody(t, r, &row)
	// generation failure degrades to null thumb
	if row["thumb_key"] != nil {
		t.Fatalf("thumb_key = %v, want null", row["thumb_key"])
	}

	// the original (corrupt-but-sniffable) file must be stored byte-identical
	f, err := s.Storage().Open(row["storage_key"].(string))
	if err != nil {
		t.Fatalf("original missing after failed thumb generation: %v", err)
	}
	defer f.Close()
	orig, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(orig, trunc) {
		t.Fatalf("stored original mismatch: %v (%d vs %d bytes)", err, len(orig), len(trunc))
	}
}

func TestDeleteProjectCleansFiles(t *testing.T) {
	s, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pf3@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Doomed"}, owner)
	var proj struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &proj)
	pid := fmtInt64(proj.ID)
	uploadProjectFile(t, ts, owner, pid, "a.png", pngBytes(t))
	uploadProjectFile(t, ts, owner, pid, "b.txt", []byte("bye"))

	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+pid, nil, owner); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete project = %d", r.StatusCode)
	}
	// storage dir must be empty (same disk assertion location_photos_test
	// uses): keys were gathered before the delete, bytes removed after.
	if n := storageFileCount(t, s); n != 0 {
		t.Fatalf("orphan files after project delete: %d", n)
	}
}
