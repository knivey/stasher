package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"stasher/internal/db"
)

// getURL fetches /files/{key} with the given cookie (local helper so this
// file reads standalone next to the item-photo tests it mirrors).
func getURL(t *testing.T, ts *httptest.Server, key string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+"/files/"+key, nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// locationPhotoRow reads the location's photo columns straight from the DB.
func locationPhotoRow(t *testing.T, s *Server, id int64) (photoKey, thumbKey sql.NullString) {
	t.Helper()
	err := s.raw.QueryRow(`SELECT photo_key, photo_thumb_key FROM locations WHERE id = ?`, id).
		Scan(&photoKey, &thumbKey)
	if err != nil {
		t.Fatal(err)
	}
	return photoKey, thumbKey
}

// fileOnDisk reports whether the storage still holds the key.
func fileOnDisk(t *testing.T, s *Server, key string) bool {
	t.Helper()
	f, err := s.Storage().Open(key)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func TestLocationPhotoUploadServeAndGate(t *testing.T) {
	s, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "locpic@test.dev", "longpassword1")
	other := registerUser(t, ts, "locpic-other@test.dev", "longpassword1")
	locID := createLocation(t, ts, owner, "Parts Bin", nil)

	resp := postFile(t, ts, "/api/v1/locations/"+fmtInt64(locID)+"/photo",
		"file", "bin.png", png1x1, owner)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("location photo upload = %d, want 200", resp.StatusCode)
	}
	var uploaded struct {
		PhotoKey string          `json:"photo_key"`
		ThumbKey json.RawMessage `json:"thumb_key"`
	}
	decodeBody(t, resp, &uploaded)
	if uploaded.PhotoKey == "" {
		t.Fatal("no photo_key returned")
	}
	if got := string(uploaded.ThumbKey); got == "null" || got == "" {
		t.Fatalf("thumb_key = %s, want the derived thumb", got)
	}

	// columns set in the DB
	photoKey, thumbKey := locationPhotoRow(t, s, locID)
	if !photoKey.Valid || photoKey.String != uploaded.PhotoKey {
		t.Fatalf("photo_key column = %v, want %q", photoKey, uploaded.PhotoKey)
	}
	if got := string(uploaded.ThumbKey); got != jsonString(thumbKey.String) {
		t.Fatalf("thumb_key = %s, want %s", got, jsonString(thumbKey.String))
	}

	// GET /locations exposes both keys (null-when-absent fields)
	var list struct {
		Data []struct {
			ID            int64  `json:"id"`
			PhotoKey      string `json:"photo_key"`
			PhotoThumbKey string `json:"photo_thumb_key"`
		} `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/locations", nil, owner), &list)
	var row *struct {
		ID            int64  `json:"id"`
		PhotoKey      string `json:"photo_key"`
		PhotoThumbKey string `json:"photo_thumb_key"`
	}
	for i := range list.Data {
		if list.Data[i].ID == locID {
			row = &list.Data[i]
		}
	}
	if row == nil {
		t.Fatal("location missing from list")
	}
	if row.PhotoKey != uploaded.PhotoKey || row.PhotoThumbKey != thumbKey.String {
		t.Fatalf("list keys = %q/%q, want %q/%q", row.PhotoKey, row.PhotoThumbKey, uploaded.PhotoKey, thumbKey.String)
	}

	// owner serves the FULL image through the /files gate
	r := getURL(t, ts, uploaded.PhotoKey, owner)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("owner file get = %d", r.StatusCode)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if string(b) != string(png1x1) {
		t.Fatal("file content mismatch")
	}
	if opt := r.Header.Get("X-Content-Type-Options"); opt != "nosniff" {
		t.Fatalf("x-content-type-options = %q, want nosniff", opt)
	}
	if ct := r.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type = %q, want image/png", ct)
	}

	// owner-only: another user gets 404 for the photo AND the thumb
	// (shared items expose their own files, never a location's picture)
	if r := getURL(t, ts, uploaded.PhotoKey, other); r.StatusCode != http.StatusNotFound {
		t.Fatalf("other user photo get = %d, want 404", r.StatusCode)
	}
	if r := getURL(t, ts, thumbKey.String, other); r.StatusCode != http.StatusNotFound {
		t.Fatalf("other user thumb get = %d, want 404", r.StatusCode)
	}
}

func TestLocationPhotoListNullWhenAbsent(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "locnull@test.dev", "longpassword1")
	locID := createLocation(t, ts, owner, "Bare", nil)
	var list struct {
		Data []struct {
			ID            int64           `json:"id"`
			PhotoKey      json.RawMessage `json:"photo_key"`
			PhotoThumbKey json.RawMessage `json:"photo_thumb_key"`
		} `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/locations", nil, owner), &list)
	if len(list.Data) != 1 || list.Data[0].ID != locID {
		t.Fatalf("list = %+v", list)
	}
	if got := string(list.Data[0].PhotoKey); got != "null" {
		t.Fatalf("photo_key = %s, want explicit null", got)
	}
	if got := string(list.Data[0].PhotoThumbKey); got != "null" {
		t.Fatalf("photo_thumb_key = %s, want explicit null", got)
	}
}

func TestLocationPhotoRejects(t *testing.T) {
	s, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "locreject@test.dev", "longpassword1")
	other := registerUser(t, ts, "locreject-other@test.dev", "longpassword1")
	locID := createLocation(t, ts, owner, "Shelf", nil)
	foreignID := createLocation(t, ts, other, "Not Yours", nil)

	// non-image -> 415 (same sniff rules as item photos)
	resp := postFile(t, ts, "/api/v1/locations/"+fmtInt64(locID)+"/photo",
		"file", "evil.sh", []byte("#!/bin/sh\nrm -rf /\n"), owner)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("non-image = %d, want 415", resp.StatusCode)
	}
	// empty file -> 400 (mirrors handleUploadPhoto)
	resp = postFile(t, ts, "/api/v1/locations/"+fmtInt64(locID)+"/photo",
		"file", "empty.png", nil, owner)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty file = %d, want 400", resp.StatusCode)
	}
	// foreign location -> 404 (checked before any body parsing)
	resp = postFile(t, ts, "/api/v1/locations/"+fmtInt64(foreignID)+"/photo",
		"file", "pic.png", png1x1, owner)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign location upload = %d, want 404", resp.StatusCode)
	}
	// no files leaked for rejected uploads
	n := 0
	if err := s.raw.QueryRow(`SELECT COUNT(*) FROM locations WHERE id = ? AND photo_key IS NOT NULL`, locID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected upload set photo_key: %v n=%d", err, n)
	}
}

// TestLocationPhotoOversizeMatchesItemPhoto pins the cap behavior to the
// item-photo endpoint's EXACT expectation for the same oversized payload
// (identical rules to handleUploadPhoto by design).
func TestLocationPhotoOversizeMatchesItemPhoto(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "locbig@test.dev", "longpassword1")
	locID := createLocation(t, ts, owner, "Big", nil)
	itemID := int64(0)
	created := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "big"}, owner)
	var c struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, created, &c)
	itemID = c.ID

	big := make([]byte, maxPhotoBytes+1) // over the cap; content irrelevant (size check first)
	locResp := postFile(t, ts, "/api/v1/locations/"+fmtInt64(locID)+"/photo",
		"file", "big.png", big, owner)
	itemResp := postFile(t, ts, "/api/v1/items/"+fmtInt64(itemID)+"/photos",
		"file", "big.png", big, owner)
	if locResp.StatusCode != itemResp.StatusCode {
		t.Fatalf("location oversize = %d, item photo oversize = %d (must match)",
			locResp.StatusCode, itemResp.StatusCode)
	}
	if locResp.StatusCode >= 500 {
		t.Fatalf("oversize = %d, want a 4xx", locResp.StatusCode)
	}
}

func TestLocationPhotoReplace(t *testing.T) {
	s, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "locreplace@test.dev", "longpassword1")
	locID := createLocation(t, ts, owner, "Drawer", nil)

	first := postFile(t, ts, "/api/v1/locations/"+fmtInt64(locID)+"/photo",
		"file", "a.jpg", jpegBytes(t, 640, 480), owner)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first upload = %d", first.StatusCode)
	}
	var firstBody struct {
		PhotoKey string `json:"photo_key"`
		ThumbKey string `json:"thumb_key"`
	}
	decodeBody(t, first, &firstBody)

	second := postFile(t, ts, "/api/v1/locations/"+fmtInt64(locID)+"/photo",
		"file", "b.jpg", jpegBytes(t, 800, 600), owner)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second upload = %d", second.StatusCode)
	}
	var secondBody struct {
		PhotoKey string `json:"photo_key"`
		ThumbKey string `json:"thumb_key"`
	}
	decodeBody(t, second, &secondBody)
	if secondBody.PhotoKey == firstBody.PhotoKey {
		t.Fatal("replace must store a new file")
	}

	// DB points at the new keys only
	photoKey, thumbKey := locationPhotoRow(t, s, locID)
	if photoKey.String != secondBody.PhotoKey || thumbKey.String != secondBody.ThumbKey {
		t.Fatalf("columns = %v/%v, want new upload", photoKey, thumbKey)
	}
	// OLD files (photo + thumb) are gone from disk; NEW files served
	if fileOnDisk(t, s, firstBody.PhotoKey) {
		t.Fatal("old photo file still on disk after replace")
	}
	if firstBody.ThumbKey != "" && fileOnDisk(t, s, firstBody.ThumbKey) {
		t.Fatal("old thumb file still on disk after replace")
	}
	if r := getURL(t, ts, secondBody.PhotoKey, owner); r.StatusCode != http.StatusOK {
		t.Fatalf("new photo get = %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
}

func TestLocationPhotoDelete(t *testing.T) {
	s, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "locdel@test.dev", "longpassword1")
	other := registerUser(t, ts, "locdel-other@test.dev", "longpassword1")
	locID := createLocation(t, ts, owner, "Cabinet", nil)
	foreignID := createLocation(t, ts, other, "Foreign", nil)

	up := postFile(t, ts, "/api/v1/locations/"+fmtInt64(locID)+"/photo",
		"file", "c.jpg", jpegBytes(t, 600, 400), owner)
	if up.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d", up.StatusCode)
	}
	var body struct {
		PhotoKey string `json:"photo_key"`
		ThumbKey string `json:"thumb_key"`
	}
	decodeBody(t, up, &body)

	// foreign location delete -> 404, owner's photo untouched
	if r := doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(foreignID)+"/photo", nil, owner); r.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign delete = %d, want 404", r.StatusCode)
	}

	if r := doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(locID)+"/photo", nil, owner); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", r.StatusCode)
	}
	photoKey, thumbKey := locationPhotoRow(t, s, locID)
	if photoKey.Valid || thumbKey.Valid {
		t.Fatalf("columns after delete = %v/%v, want NULL", photoKey, thumbKey)
	}
	if fileOnDisk(t, s, body.PhotoKey) {
		t.Fatal("photo file still on disk after delete")
	}
	if body.ThumbKey != "" && fileOnDisk(t, s, body.ThumbKey) {
		t.Fatal("thumb file still on disk after delete")
	}
	if r := getURL(t, ts, body.PhotoKey, owner); r.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted photo get = %d, want 404", r.StatusCode)
	}

	// idempotent: deleting again is still 204
	if r := doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(locID)+"/photo", nil, owner); r.StatusCode != http.StatusNoContent {
		t.Fatalf("second delete = %d, want 204", r.StatusCode)
	}
}

func TestDeleteLocationRemovesPhotoFiles(t *testing.T) {
	s, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "locgone@test.dev", "longpassword1")
	locID := createLocation(t, ts, owner, "Doomed", nil)

	up := postFile(t, ts, "/api/v1/locations/"+fmtInt64(locID)+"/photo",
		"file", "gone.jpg", jpegBytes(t, 600, 400), owner)
	if up.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d", up.StatusCode)
	}
	var body struct {
		PhotoKey string `json:"photo_key"`
		ThumbKey string `json:"thumb_key"`
	}
	decodeBody(t, up, &body)

	if r := doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(locID), nil, owner); r.StatusCode != http.StatusNoContent {
		t.Fatalf("location delete = %d, want 204", r.StatusCode)
	}
	if fileOnDisk(t, s, body.PhotoKey) {
		t.Fatal("photo file still on disk after location delete")
	}
	if body.ThumbKey != "" && fileOnDisk(t, s, body.ThumbKey) {
		t.Fatal("thumb file still on disk after location delete")
	}
}

// TestLocationPhotoQueryByKey pins GetLocationPhotoByKey: it resolves both
// the photo and the thumb key to the owning user (used by the /files gate),
// and unknown keys return no rows.
func TestLocationPhotoQueryByKey(t *testing.T) {
	s, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "loctestkey@test.dev", "longpassword1")
	locID := createLocation(t, ts, owner, "Keyed", nil)
	up := postFile(t, ts, "/api/v1/locations/"+fmtInt64(locID)+"/photo",
		"file", "k.jpg", jpegBytes(t, 600, 400), owner)
	var body struct {
		PhotoKey string `json:"photo_key"`
		ThumbKey string `json:"thumb_key"`
	}
	decodeBody(t, up, &body)

	ownerRow, err := s.Queries().GetUserByEmail(context.Background(), "loctestkey@test.dev")
	if err != nil {
		t.Fatal(err)
	}
	byPhoto, err := s.Queries().GetLocationPhotoByKey(context.Background(), db.GetLocationPhotoByKeyParams{
		PhotoKey: sqlNullString(body.PhotoKey),
	})
	if err != nil || byPhoto != ownerRow.ID {
		t.Fatalf("by photo: %v %d (want user %d)", err, byPhoto, ownerRow.ID)
	}
	byThumb, err := s.Queries().GetLocationPhotoByKey(context.Background(), db.GetLocationPhotoByKeyParams{
		PhotoThumbKey: sqlNullString(body.ThumbKey),
	})
	if err != nil || byThumb != ownerRow.ID {
		t.Fatalf("by thumb: %v %d (want user %d)", err, byThumb, ownerRow.ID)
	}
	if _, err := s.Queries().GetLocationPhotoByKey(context.Background(), db.GetLocationPhotoByKeyParams{
		PhotoKey: sqlNullString("no/such/key"),
	}); err == nil {
		t.Fatal("unknown key must not match")
	}
}
