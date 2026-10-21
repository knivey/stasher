package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// createQtyItem creates an owned item with the given qty and returns its id.
func createQtyItem(t *testing.T, ts *httptest.Server, cookie *http.Cookie, name string, qty float64) int64 {
	t.Helper()
	resp := doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": name, "qty": qty}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create item %q = %d", name, resp.StatusCode)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &created)
	return created.ID
}

// usageEntry mirrors one item_ledger row as returned by the usage endpoints.
type usageEntry struct {
	ID           int64   `json:"id"`
	Delta        float64 `json:"delta"`
	ResultingQty float64 `json:"resulting_qty"`
	Kind         string  `json:"kind"`
	ProjectID    *int64  `json:"project_id"`
	Note         string  `json:"note"`
	CreatedAt    string  `json:"created_at"`
}

func listUsage(t *testing.T, ts *httptest.Server, cookie *http.Cookie, itemID int64) (entries []usageEntry, total int64) {
	t.Helper()
	resp := doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(itemID)+"/usage", nil, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list usage = %d", resp.StatusCode)
	}
	var body struct {
		Data  []usageEntry `json:"data"`
		Total int64        `json:"total"`
	}
	decodeBody(t, resp, &body)
	return body.Data, body.Total
}

func TestItemUsageLifecycle(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "usage-life@test.dev", "longpassword1")
	id := createQtyItem(t, ts, cookie, "resistors", 10)

	post := func(body map[string]any) *http.Response {
		return doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/usage", body, cookie)
	}
	mustPost := func(body map[string]any) usageEntry {
		t.Helper()
		resp := post(body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("usage %v = %d, want 201", body, resp.StatusCode)
		}
		var e usageEntry
		decodeBody(t, resp, &e)
		return e
	}
	detail := func() (qty float64, disposed bool) {
		t.Helper()
		var d struct {
			Qty      float64 `json:"qty"`
			Disposed bool    `json:"disposed"`
		}
		decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(id), nil, cookie), &d)
		return d.Qty, d.Disposed
	}

	// used -3 -> 7
	e := mustPost(map[string]any{"kind": "used", "delta": -3, "note": "bench build"})
	if e.Delta != -3 || e.ResultingQty != 7 || e.Kind != "used" || e.Note != "bench build" {
		t.Fatalf("used entry = %+v", e)
	}
	if q, dis := detail(); q != 7 || dis {
		t.Fatalf("after used: qty=%v disposed=%v", q, dis)
	}
	// sold -2 -> 5
	if e := mustPost(map[string]any{"kind": "sold", "delta": -2}); e.ResultingQty != 5 {
		t.Fatalf("sold entry = %+v", e)
	}
	// restock +5 -> 10
	if e := mustPost(map[string]any{"kind": "restock", "delta": 5}); e.ResultingQty != 10 {
		t.Fatalf("restock entry = %+v", e)
	}
	// disposed: delta forced to -current qty, resulting 0, flag set
	e = mustPost(map[string]any{"kind": "disposed"})
	if e.Delta != -10 || e.ResultingQty != 0 || e.Kind != "disposed" {
		t.Fatalf("disposed entry = %+v", e)
	}
	if q, dis := detail(); q != 0 || !dis {
		t.Fatalf("after dispose: qty=%v disposed=%v", q, dis)
	}

	// validation: bad kind, missing delta, zero delta, below zero
	if resp := post(map[string]any{"kind": "evaporated", "delta": -1}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad kind = %d, want 400", resp.StatusCode)
	}
	if resp := post(map[string]any{"kind": "used"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing delta = %d, want 400", resp.StatusCode)
	}
	if resp := post(map[string]any{"kind": "used", "delta": 0}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("zero delta = %d, want 400", resp.StatusCode)
	}
	if resp := post(map[string]any{"kind": "used", "delta": -0.5}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("below zero = %d, want 400", resp.StatusCode)
	}

	// full ledger, newest first, including the create-time baseline
	entries, total := listUsage(t, ts, cookie, id)
	if total != 5 || len(entries) != 5 {
		t.Fatalf("usage total = %d len = %d, want 5/5", total, len(entries))
	}
	want := []struct {
		kind             string
		delta, resulting float64
	}{
		{"disposed", -10, 0},
		{"restock", 5, 10},
		{"sold", -2, 5},
		{"used", -3, 7},
		{"adjustment", 10, 10},
	}
	for i, w := range want {
		got := entries[i]
		if got.Kind != w.kind || got.Delta != w.delta || got.ResultingQty != w.resulting {
			t.Fatalf("entry[%d] = %+v, want %+v", i, got, w)
		}
	}
	if entries[4].Note != "initial" {
		t.Fatalf("baseline note = %q, want initial", entries[4].Note)
	}
}

func TestItemUsageProjectLink(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "usage-proj-a@test.dev", "longpassword1")
	bob := registerUser(t, ts, "usage-proj-b@test.dev", "longpassword1")

	mkProject := func(cookie *http.Cookie, name string) int64 {
		resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": name}, cookie)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create project = %d", resp.StatusCode)
		}
		var p struct {
			ID int64 `json:"id"`
		}
		decodeBody(t, resp, &p)
		return p.ID
	}
	proj := mkProject(alice, "bench PSU repair")
	foreignProj := mkProject(bob, "bob secret")

	id := createQtyItem(t, ts, alice, "fuse holders", 5)
	resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/usage",
		map[string]any{"kind": "used", "delta": -2, "project_id": proj}, alice)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("usage with project = %d", resp.StatusCode)
	}
	var e usageEntry
	decodeBody(t, resp, &e)
	if e.ProjectID == nil || *e.ProjectID != proj {
		t.Fatalf("entry project_id = %v, want %d", e.ProjectID, proj)
	}

	// foreign project -> 404, and nothing may be written
	if resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/usage",
		map[string]any{"kind": "used", "delta": -1, "project_id": foreignProj}, alice); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign project = %d, want 404", resp.StatusCode)
	}
	_, total := listUsage(t, ts, alice, id)
	if total != 2 { // baseline + the linked entry
		t.Fatalf("entries after rejected project link = %d, want 2", total)
	}
}

func TestItemPatchQtyLogsAdjustment(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "usage-patch@test.dev", "longpassword1")
	id := createQtyItem(t, ts, cookie, "capacitors", 10)

	// qty-changing PATCH auto-logs an adjustment
	resp := doJSON(t, ts, "PATCH", "/api/v1/items/"+fmtInt64(id),
		map[string]any{"qty": 4}, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch qty = %d", resp.StatusCode)
	}
	entries, total := listUsage(t, ts, cookie, id)
	if total != 2 || len(entries) != 2 {
		t.Fatalf("after qty patch: total=%d len=%d, want 2/2", total, len(entries))
	}
	top := entries[0]
	if top.Kind != "adjustment" || top.Delta != -6 || top.ResultingQty != 4 || top.Note != "" {
		t.Fatalf("adjustment entry = %+v", top)
	}

	// name-only PATCH writes no entry
	resp = doJSON(t, ts, "PATCH", "/api/v1/items/"+fmtInt64(id),
		map[string]any{"name": "electrolytics"}, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch name = %d", resp.StatusCode)
	}
	if _, total := listUsage(t, ts, cookie, id); total != 2 {
		t.Fatalf("name-only patch added an entry: total = %d, want 2", total)
	}
}

func TestItemCopyFreshLedger(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "usage-copy@test.dev", "longpassword1")
	id := createQtyItem(t, ts, cookie, "spax bits", 3)

	resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/copy", nil, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("copy = %d", resp.StatusCode)
	}
	var copied struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &copied)
	if copied.ID == id {
		t.Fatal("copy returned the source id")
	}

	entries, total := listUsage(t, ts, cookie, copied.ID)
	if total != 1 || len(entries) != 1 {
		t.Fatalf("copy ledger: total=%d len=%d, want 1/1", total, len(entries))
	}
	e := entries[0]
	if e.Kind != "adjustment" || e.Note != "initial" || e.Delta != 3 || e.ResultingQty != 3 {
		t.Fatalf("copy baseline = %+v", e)
	}
}

func TestItemUsageDisposedFlagAndIsolation(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "usage-iso-a@test.dev", "longpassword1")
	bob := registerUser(t, ts, "usage-iso-b@test.dev", "longpassword1")
	id := createQtyItem(t, ts, alice, "nitrile gloves", 2)

	// list rows carry disposed (false before)
	listDisposed := func() bool {
		var body struct {
			Data []struct {
				ID       int64 `json:"id"`
				Disposed bool  `json:"disposed"`
			} `json:"data"`
		}
		decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items", nil, alice), &body)
		for _, d := range body.Data {
			if d.ID == id {
				return d.Disposed
			}
		}
		t.Fatal("item missing from list")
		return false
	}
	if listDisposed() {
		t.Fatal("fresh item should not be disposed")
	}

	if resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/usage",
		map[string]any{"kind": "disposed"}, alice); resp.StatusCode != http.StatusCreated {
		t.Fatalf("dispose = %d", resp.StatusCode)
	}
	if !listDisposed() {
		t.Fatal("disposed item flag missing from list rows")
	}

	// foreign-item usage endpoints -> 404 (no existence leaks)
	if resp := doJSON(t, ts, "POST", "/api/v1/items/"+fmtInt64(id)+"/usage",
		map[string]any{"kind": "used", "delta": -1}, bob); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign POST usage = %d, want 404", resp.StatusCode)
	}
	if resp := doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(id)+"/usage", nil, bob); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign GET usage = %d, want 404", resp.StatusCode)
	}
}
