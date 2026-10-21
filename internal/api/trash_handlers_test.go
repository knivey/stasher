package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"stasher/internal/config"
)

// trashEntry is the decoded shape of one GET /trash row.
type trashEntry struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Qty        float64 `json:"qty"`
	PhotoCount int64   `json:"photo_count"`
	ThumbKey   *string `json:"thumb_key"`
	TrashedAt  string  `json:"trashed_at"`
	PurgesAt   *string `json:"purges_at"`
}

func listTrash(t *testing.T, ts *httptest.Server, cookie *http.Cookie) (entries []trashEntry, total int64) {
	t.Helper()
	resp := doJSON(t, ts, "GET", "/api/v1/trash", nil, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /trash = %d", resp.StatusCode)
	}
	var body struct {
		Data  []trashEntry `json:"data"`
		Total int64        `json:"total"`
	}
	decodeBody(t, resp, &body)
	return body.Data, body.Total
}

// fileGone asserts the storage key no longer serves (HTTP 404) and the
// file is physically gone from disk.
func fileGone(t *testing.T, s *Server, ts *httptest.Server, key string, cookie *http.Cookie) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+"/files/"+key, nil)
	req.AddCookie(cookie)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != http.StatusNotFound {
		t.Fatalf("file %q: %v %d, want 404", key, err, r.StatusCode)
	}
	if f, err := s.st.Open(key); err == nil {
		f.Close()
		t.Fatalf("file %q still on disk after purge", key)
	}
}

// createItemWithFiles makes an item with tags, one photo, and one document,
// returning the item id and both storage keys.
func createItemWithFiles(t *testing.T, ts *httptest.Server, cookie *http.Cookie, name string) (itemID int64, photoKey, docKey string) {
	t.Helper()
	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{
		"name": name, "qty": 10, "unit": "pcs", "tags": []string{"hardware"},
	}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create item = %d", resp.StatusCode)
	}
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &item)

	resp = postFile(t, ts, "/api/v1/items/"+fmtInt64(item.ID)+"/photos",
		"file", name+".png", png1x1, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("photo upload = %d", resp.StatusCode)
	}
	var photo struct {
		StorageKey string `json:"storage_key"`
	}
	decodeBody(t, resp, &photo)

	resp = postFile(t, ts, "/api/v1/items/"+fmtInt64(item.ID)+"/documents",
		"file", name+".pdf", []byte("%PDF-1.4 fake pdf bytes"), cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("doc upload = %d", resp.StatusCode)
	}
	var doc struct {
		StorageKey string `json:"storage_key"`
	}
	decodeBody(t, resp, &doc)
	return item.ID, photo.StorageKey, doc.StorageKey
}

// TestTrashFullCycle covers the whole flow: delete snapshots everything
// (tags, photo, document, project link, ledger), the item disappears from
// /items, restore brings it back under a NEW id with every relation intact
// (files still serve), and the trash ends up empty.
func TestTrashFullCycle(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "trash-full@test.dev", "longpassword1")

	itemID, photoKey, docKey := createItemWithFiles(t, ts, cookie, "widget")

	// one project link and one usage entry on top of the baseline ledger
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "bench"}, cookie)
	var project struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &project)
	if resp = doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(project.ID)+"/items",
		map[string]any{"item_id": itemID, "qty_used": 2, "notes": "screws"}, cookie); resp.StatusCode != http.StatusCreated {
		t.Fatalf("project link = %d", resp.StatusCode)
	}
	if resp = doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(itemID)+"/usage",
		map[string]any{"kind": "used", "delta": -3, "project_id": project.ID}, cookie); resp.StatusCode != http.StatusCreated {
		t.Fatalf("usage = %d", resp.StatusCode)
	}

	// DELETE /items/{id} -> 204, item gone from /items, trash shows it
	if resp = doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(itemID), nil, cookie); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete item = %d", resp.StatusCode)
	}
	if resp = doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(itemID), nil, cookie); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get trashed item = %d, want 404", resp.StatusCode)
	}
	if _, total := listItems(t, ts, cookie, ""); total != 0 {
		t.Fatalf("items total = %d, want 0", total)
	}

	entries, total := listTrash(t, ts, cookie)
	if total != 1 || len(entries) != 1 {
		t.Fatalf("trash = %d entries (total %d), want 1", len(entries), total)
	}
	e := entries[0]
	if e.Name != "widget" || e.Qty != 7 {
		t.Fatalf("trash entry = %+v, want widget qty 7", e)
	}
	if e.PhotoCount != 1 || e.ThumbKey == nil || *e.ThumbKey != photoKey {
		t.Fatalf("trash entry thumb = %+v, want photo key %q", e, photoKey)
	}
	if e.TrashedAt == "" {
		t.Fatal("trashed_at empty")
	}
	if e.PurgesAt != nil {
		t.Fatalf("purges_at = %v, want null (retention 0 in test config)", *e.PurgesAt)
	}

	// files survive the item delete ON DISK (their rows are gone with the
	// item, so /files 404s while trashed; the keys live in the snapshot)
	for _, key := range []string{photoKey, docKey} {
		if f, err := s.st.Open(key); err != nil {
			f.Close()
			t.Fatalf("file %q vanished with the item: %v", key, err)
		} else {
			f.Close()
		}
	}

	// restore -> 201, new id, everything back
	resp = doJSON(t, ts, "POST", "/api/v1/trash/"+fmtInt64(e.ID)+"/restore", nil, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("restore = %d", resp.StatusCode)
	}
	var restored struct {
		ID         int64   `json:"id"`
		LocationID *int64  `json:"location_id"`
		Qty        float64 `json:"qty"`
		Tags       []struct {
			Name string `json:"name"`
		} `json:"tags"`
		Photos []struct {
			StorageKey string `json:"storage_key"`
		} `json:"photos"`
		Documents []struct {
			StorageKey string `json:"storage_key"`
			Filename   string `json:"filename"`
		} `json:"documents"`
		Projects []struct {
			ID      int64    `json:"id"`
			QtyUsed *float64 `json:"qty_used"`
		} `json:"projects"`
	}
	decodeBody(t, resp, &restored)
	if restored.ID == itemID {
		t.Fatal("restore reused the old item id")
	}
	if restored.Qty != 7 || len(restored.Tags) != 1 || restored.Tags[0].Name != "hardware" {
		t.Fatalf("restored item = %+v", restored)
	}
	if len(restored.Photos) != 1 || restored.Photos[0].StorageKey != photoKey {
		t.Fatalf("restored photos = %+v, want key %q", restored.Photos, photoKey)
	}
	if len(restored.Documents) != 1 || restored.Documents[0].StorageKey != docKey || restored.Documents[0].Filename != "widget.pdf" {
		t.Fatalf("restored documents = %+v", restored.Documents)
	}
	if len(restored.Projects) != 1 || restored.Projects[0].ID != project.ID || restored.Projects[0].QtyUsed == nil || *restored.Projects[0].QtyUsed != 2 {
		t.Fatalf("restored projects = %+v", restored.Projects)
	}
	// restored photo still serves
	req, _ := http.NewRequest("GET", ts.URL+"/files/"+photoKey, nil)
	req.AddCookie(cookie)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("restored photo file: %v %d", err, r.StatusCode)
	}
	// ledger history survived: baseline (initial, qty 10) + used (-3, qty 7)
	resp = doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(restored.ID)+"/usage?page_size=100", nil, cookie)
	var ledger struct {
		Data []struct {
			Delta        float64 `json:"delta"`
			ResultingQty float64 `json:"resulting_qty"`
			Kind         string  `json:"kind"`
			ProjectID    *int64  `json:"project_id"`
		} `json:"data"`
	}
	decodeBody(t, resp, &ledger)
	if len(ledger.Data) != 2 {
		t.Fatalf("restored ledger = %d entries, want 2", len(ledger.Data))
	}
	// API lists DESC: newest first (used), then baseline
	if ledger.Data[0].Kind != "used" || ledger.Data[0].Delta != -3 || ledger.Data[0].ResultingQty != 7 ||
		ledger.Data[0].ProjectID == nil || *ledger.Data[0].ProjectID != project.ID {
		t.Fatalf("ledger[0] = %+v, want used -3 -> 7 with project", ledger.Data[0])
	}
	if ledger.Data[1].Kind != "adjustment" || ledger.Data[1].Delta != 10 || ledger.Data[1].ResultingQty != 10 {
		t.Fatalf("ledger[1] = %+v, want baseline 10 -> 10", ledger.Data[1])
	}

	// trash is now empty and the item is back in /items
	if _, total = listTrash(t, ts, cookie); total != 0 {
		t.Fatalf("trash total after restore = %d, want 0", total)
	}
	if _, total = listItems(t, ts, cookie, ""); total != 1 {
		t.Fatalf("items total after restore = %d, want 1", total)
	}
}

// TestTrashRestoreVanishedDeps: a restored item drops its location if the
// location was deleted while trashed, and skips links to vanished projects
// while keeping links to surviving ones.
func TestTrashRestoreVanishedDeps(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "trash-vanish@test.dev", "longpassword1")

	resp := doJSON(t, ts, "POST", "/api/v1/locations", map[string]any{"name": "garage"}, cookie)
	var loc struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &loc)

	var projIDs [2]int64
	for i, name := range []string{"kept", "gone"} {
		resp = doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": name}, cookie)
		var p struct {
			ID int64 `json:"id"`
		}
		decodeBody(t, resp, &p)
		projIDs[i] = p.ID
	}

	resp = doJSON(t, ts, "POST", "/api/v1/items", map[string]any{
		"name": "dependent", "location_id": loc.ID,
	}, cookie)
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &item)
	for _, pid := range projIDs {
		if resp = doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(pid)+"/items",
			map[string]any{"item_id": item.ID}, cookie); resp.StatusCode != http.StatusCreated {
			t.Fatalf("project link = %d", resp.StatusCode)
		}
	}

	doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(item.ID), nil, cookie)
	entries, _ := listTrash(t, ts, cookie)
	if len(entries) != 1 {
		t.Fatalf("trash entries = %d, want 1", len(entries))
	}
	// the location and the second project vanish while trashed
	doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(loc.ID), nil, cookie)
	doJSON(t, ts, "DELETE", "/api/v1/projects/"+fmtInt64(projIDs[1]), nil, cookie)

	resp = doJSON(t, ts, "POST", "/api/v1/trash/"+fmtInt64(entries[0].ID)+"/restore", nil, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("restore = %d", resp.StatusCode)
	}
	var restored struct {
		LocationID *int64 `json:"location_id"`
		Projects   []struct {
			ID int64 `json:"id"`
		} `json:"projects"`
	}
	decodeBody(t, resp, &restored)
	if restored.LocationID != nil {
		t.Fatalf("restored location_id = %v, want null", *restored.LocationID)
	}
	if len(restored.Projects) != 1 || restored.Projects[0].ID != projIDs[0] {
		t.Fatalf("restored projects = %+v, want only %d", restored.Projects, projIDs[0])
	}
}

// TestTrashPurgeAndEmpty: purging one entry deletes its files from disk;
// emptying trash clears every owned entry the same way.
func TestTrashPurgeAndEmpty(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "trash-purge@test.dev", "longpassword1")

	id1, photo1, doc1 := createItemWithFiles(t, ts, cookie, "one")
	id2, photo2, _ := createItemWithFiles(t, ts, cookie, "two")
	doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(id1), nil, cookie)
	doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(id2), nil, cookie)
	entries, total := listTrash(t, ts, cookie)
	if total != 2 || len(entries) != 2 {
		t.Fatalf("trash = %d/%d, want 2/2", len(entries), total)
	}

	// purge one (the entry for "one", whichever id that is)
	var oneID int64
	for _, e := range entries {
		if e.Name == "one" {
			oneID = e.ID
		}
	}
	if resp := doJSON(t, ts, "DELETE", "/api/v1/trash/"+fmtInt64(oneID), nil, cookie); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("purge one = %d", resp.StatusCode)
	}
	fileGone(t, s, ts, photo1, cookie)
	fileGone(t, s, ts, doc1, cookie)
	if _, total = listTrash(t, ts, cookie); total != 1 {
		t.Fatalf("trash total after purge = %d, want 1", total)
	}

	// empty all
	if resp := doJSON(t, ts, "DELETE", "/api/v1/trash", nil, cookie); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("empty trash = %d", resp.StatusCode)
	}
	fileGone(t, s, ts, photo2, cookie)
	if _, total = listTrash(t, ts, cookie); total != 0 {
		t.Fatalf("trash total after empty = %d, want 0", total)
	}
	// purging an already-purged entry -> 404
	if resp := doJSON(t, ts, "DELETE", "/api/v1/trash/"+fmtInt64(oneID), nil, cookie); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("re-purge = %d, want 404", resp.StatusCode)
	}
}

// TestTrashAutoPurge: GET /trash sweeps entries whose purges_at is in the
// past (deleting their files); entries with purges_at ” survive forever.
func TestTrashAutoPurge(t *testing.T) {
	s, ts := newTestServer(t, func(c *config.Config) { c.TrashRetentionDays = 30 })
	cookie := registerUser(t, ts, "trash-auto@test.dev", "longpassword1")

	expiredID, photoE, docE := createItemWithFiles(t, ts, cookie, "expired")
	keptID, photoK, _ := createItemWithFiles(t, ts, cookie, "kept")
	doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(expiredID), nil, cookie)
	doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(keptID), nil, cookie)

	// with retention 30 the fresh entries carry a future purges_at...
	entries, _ := listTrash(t, ts, cookie)
	if len(entries) != 2 {
		t.Fatalf("trash entries = %d, want 2", len(entries))
	}
	for _, e := range entries {
		if e.PurgesAt == nil || *e.PurgesAt == "" {
			t.Fatalf("purges_at = %v, want future timestamp", e.PurgesAt)
		}
	}

	// ...force one entry over the horizon
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	mustExec(t, s, `UPDATE trash SET purges_at = ? WHERE id = (
		SELECT id FROM trash WHERE snapshot LIKE '%"expired"%' LIMIT 1)`, past)
	// and pin one entry to never-purge
	mustExec(t, s, `UPDATE trash SET purges_at = '' WHERE id = (
		SELECT id FROM trash WHERE snapshot LIKE '%"kept"%' LIMIT 1)`)

	entries, total := listTrash(t, ts, cookie) // sweeps on read
	if total != 1 || len(entries) != 1 || entries[0].Name != "kept" {
		t.Fatalf("trash after sweep = %+v (total %d), want only kept", entries, total)
	}
	fileGone(t, s, ts, photoE, cookie)
	fileGone(t, s, ts, docE, cookie)
	// the never-purge entry's file is untouched
	if _, err := s.st.Open(photoK); err != nil {
		t.Fatalf("kept file vanished: %v", err)
	}
	// purges_at '' renders as JSON null
	if entries[0].PurgesAt != nil {
		t.Fatalf("purges_at = %v, want null", *entries[0].PurgesAt)
	}
}

// TestTrashIsolation: trash entries are per-user - foreign restore and
// purge 404, and the list only shows one's own entries.
func TestTrashIsolation(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "alice-trash@test.dev", "longpassword1")
	bob := registerUser(t, ts, "bob-trash@test.dev", "longpassword1")

	aliceItem, _, _ := createItemWithFiles(t, ts, alice, "alice thing")
	bobItem, _, _ := createItemWithFiles(t, ts, bob, "bob thing")
	doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(aliceItem), nil, alice)
	doJSON(t, ts, "DELETE", "/api/v1/items/"+fmtInt64(bobItem), nil, bob)

	aliceEntries, _ := listTrash(t, ts, alice)
	bobEntries, _ := listTrash(t, ts, bob)
	if len(aliceEntries) != 1 || aliceEntries[0].Name != "alice thing" {
		t.Fatalf("alice trash = %+v", aliceEntries)
	}
	if len(bobEntries) != 1 || bobEntries[0].Name != "bob thing" {
		t.Fatalf("bob trash = %+v, want only his own", bobEntries)
	}

	foreign := aliceEntries[0].ID
	if resp := doJSON(t, ts, "POST", "/api/v1/trash/"+fmtInt64(foreign)+"/restore", nil, bob); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign restore = %d, want 404", resp.StatusCode)
	}
	if resp := doJSON(t, ts, "DELETE", "/api/v1/trash/"+fmtInt64(foreign), nil, bob); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign purge = %d, want 404", resp.StatusCode)
	}
	// alice's entry is untouched by bob's attempts
	if _, total := listTrash(t, ts, alice); total != 1 {
		t.Fatalf("alice trash total = %d, want 1", total)
	}
	// bob emptying his trash must not touch alice's
	doJSON(t, ts, "DELETE", "/api/v1/trash", nil, bob)
	if _, total := listTrash(t, ts, alice); total != 1 {
		t.Fatalf("alice trash total after bob empty = %d, want 1", total)
	}
}
