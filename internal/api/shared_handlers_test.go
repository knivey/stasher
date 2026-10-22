package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestSharedListThumbKey(t *testing.T) {
	s, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "share-thumb-a@test.dev", "longpassword1")
	bob := registerUser(t, ts, "share-thumb-b@test.dev", "longpassword1")

	// alice: a private item with a photo (must stay invisible) and a shared
	// item with a photo (thumb_key must surface in bob's /shared list)
	priv := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "secret"}, alice)
	var privItem struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, priv, &privItem)
	postFile(t, ts, "/api/v1/items/"+fmtInt64(privItem.ID)+"/photos",
		"file", "secret.png", png1x1, alice)

	shared := doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": "public widget", "visibility": "shared"}, alice)
	var sharedItem struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, shared, &sharedItem)
	postFile(t, ts, "/api/v1/items/"+fmtInt64(sharedItem.ID)+"/photos",
		"file", "widget.png", png1x1, alice)

	// uploads derive real thumbs: the expected /shared value is the photo's
	// DERIVED thumb_key, read straight from its row
	rows, err := s.Queries().ListItemPhotos(context.Background(), sharedItem.ID)
	if err != nil || len(rows) != 1 ||
		!rows[0].ThumbKey.Valid || rows[0].ThumbKey.String == rows[0].StorageKey {
		t.Fatalf("shared item photos: %v %+v", err, rows)
	}

	var list struct {
		Data []struct {
			Name     string          `json:"name"`
			ThumbKey json.RawMessage `json:"thumb_key"`
		} `json:"data"`
		Total int64 `json:"total"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/shared", nil, bob), &list)
	if list.Total != 1 || len(list.Data) != 1 || list.Data[0].Name != "public widget" {
		t.Fatalf("shared list should hold only the shared item, got %+v", list)
	}
	if got := string(list.Data[0].ThumbKey); got != jsonString(rows[0].ThumbKey.String) {
		t.Fatalf("shared thumb_key = %s, want %s (derived thumb, not the original %q)",
			got, jsonString(rows[0].ThumbKey.String), rows[0].StorageKey)
	}
}

func TestSharedBrowseAndCopy(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "share-a@test.dev", "longpassword1")
	bob := registerUser(t, ts, "share-b@test.dev", "longpassword1")

	// alice: private item + shared item with a tag and a photo
	priv := doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": "secret"}, alice)
	var privItem struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, priv, &privItem)

	shared := doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": "soldering iron tips", "qty": 8, "tags": []string{"electronics"},
			"visibility": "shared"}, alice)
	var sharedItem struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, shared, &sharedItem)
	photoResp := postFile(t, ts, "/api/v1/items/"+fmtInt64(sharedItem.ID)+"/photos",
		"file", "tip.png", png1x1, alice)
	var photo struct {
		StorageKey string `json:"storage_key"`
	}
	decodeBody(t, photoResp, &photo)

	// bob browses shared: only the shared item, with owner name
	var list struct {
		Data []struct {
			Name      string `json:"name"`
			OwnerName string `json:"owner_name"`
		} `json:"data"`
		Total int64 `json:"total"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/shared", nil, bob), &list)
	if list.Total != 1 || list.Data[0].Name != "soldering iron tips" ||
		list.Data[0].OwnerName != "Test" {
		t.Fatalf("shared list = %+v", list)
	}

	// copying a PRIVATE item of another user -> 404 (Review Focus #5)
	if r := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(privItem.ID)+"/copy", nil, bob); r.StatusCode != http.StatusNotFound {
		t.Fatalf("copy private = %d, want 404", r.StatusCode)
	}
	// nonexistent -> 404
	if r := doJSON(t, ts, "POST", "/api/v1/items/999999/copy", nil, bob); r.StatusCode != http.StatusNotFound {
		t.Fatalf("copy missing = %d, want 404", r.StatusCode)
	}

	// bob copies the shared item
	resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(sharedItem.ID)+"/copy", nil, bob)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("copy = %d", resp.StatusCode)
	}
	var copy struct {
		ID   int64 `json:"id"`
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
		Photos []struct {
			StorageKey string `json:"storage_key"`
		} `json:"photos"`
		LocationID any     `json:"location_id"`
		Visibility string  `json:"visibility"`
		Qty        float64 `json:"qty"`
	}
	decodeBody(t, resp, &copy)
	if copy.ID == sharedItem.ID {
		t.Fatal("copy reuses item id")
	}
	if len(copy.Tags) != 1 || copy.Tags[0].Name != "electronics" {
		t.Fatalf("copy tags = %+v", copy.Tags)
	}
	if len(copy.Photos) != 1 || copy.Photos[0].StorageKey == photo.StorageKey {
		t.Fatalf("copy photos = %+v (orig key %q)", copy.Photos, photo.StorageKey)
	}
	if copy.LocationID != nil {
		t.Fatalf("copy kept foreign location: %v", copy.LocationID)
	}
	if copy.Visibility != "private" || copy.Qty != 8 {
		t.Fatalf("copy = %+v", copy)
	}
	// copied photo file is really there and independent
	req, _ := http.NewRequest("GET", ts.URL+"/files/"+copy.Photos[0].StorageKey, nil)
	req.AddCookie(bob)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("copied photo fetch: %v %d", err, r.StatusCode)
	}
	// bob owns the copy in his list
	if _, total := listItems(t, ts, bob, ""); total != 1 {
		t.Fatal("copy not in bob's items")
	}

	// alice deletes her original; bob's copy survives with its file
	doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(sharedItem.ID), nil, alice)
	req, _ = http.NewRequest("GET", ts.URL+"/files/"+copy.Photos[0].StorageKey, nil)
	req.AddCookie(bob)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("copied photo died with original: %v %d", err, r.StatusCode)
	}
}

func TestCopyOwnItem(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "dup@test.dev", "longpassword1")
	created := doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": "dup me", "tags": []string{"t"}}, cookie)
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, created, &item)
	if r := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(item.ID)+"/copy", nil, cookie); r.StatusCode != http.StatusCreated {
		t.Fatalf("copy own = %d", r.StatusCode)
	}
	if _, total := listItems(t, ts, cookie, ""); total != 2 {
		t.Fatal("duplicate missing")
	}
}

// TestCopyDuplicatesThumb: copying an item duplicates the derived thumb
// FILE under a fresh key (both openable independently, source untouched).
func TestCopyDuplicatesThumb(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "copy-thumb@test.dev", "longpassword1")

	created := doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": "thumb source"}, cookie)
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, created, &item)
	postFile(t, ts, "/api/v1/items/"+fmtInt64(item.ID)+"/photos",
		"file", "src.png", png1x1, cookie)

	src, err := s.Queries().ListItemPhotos(context.Background(), item.ID)
	if err != nil || len(src) != 1 || !src[0].ThumbKey.Valid {
		t.Fatalf("source photos: %v %+v", err, src)
	}
	srcThumb := src[0].ThumbKey.String

	resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(item.ID)+"/copy", nil, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("copy = %d", resp.StatusCode)
	}
	var copy struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &copy)

	copied, err := s.Queries().ListItemPhotos(context.Background(), copy.ID)
	if err != nil || len(copied) != 1 {
		t.Fatalf("copied photos: %v %d", err, len(copied))
	}
	if !copied[0].ThumbKey.Valid {
		t.Fatal("copied photo has no thumb_key")
	}
	newThumb := copied[0].ThumbKey.String
	if newThumb == srcThumb || newThumb == copied[0].StorageKey {
		t.Fatalf("copied thumb_key = %q (source thumb %q, copy storage %q), want a fresh key",
			newThumb, srcThumb, copied[0].StorageKey)
	}
	// both thumb FILES exist independently; the source thumb is intact
	for _, key := range []string{srcThumb, newThumb} {
		f, err := s.st.Open(key)
		if err != nil {
			t.Fatalf("thumb %q not openable: %v", key, err)
		}
		f.Close()
	}
}
