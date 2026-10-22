package api

import (
	"net/http"
	"testing"
)

func TestProjectLifecycle(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "proj@test.dev", "longpassword1")

	resp := doJSON(t, ts, "POST", "/api/v1/projects",
		map[string]any{"name": "Home NAS", "notes": "bulk storage"}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project = %d", resp.StatusCode)
	}
	var proj struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	decodeBody(t, resp, &proj)
	if proj.Status != "planned" {
		t.Fatalf("default status = %q", proj.Status)
	}

	// invalid status rejected
	if r := doJSON(t, ts, "POST", "/api/v1/projects",
		map[string]any{"name": "bad", "status": "wip"}, cookie); r.StatusCode != 400 {
		t.Fatalf("bad status = %d", r.StatusCode)
	}

	// add an item with qty_used
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "8TB HDD"}, cookie)
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &created)
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(proj.ID)+"/items",
		map[string]any{"item_id": created.ID, "qty_used": 4}, cookie); r.StatusCode != 201 {
		t.Fatalf("add project item = %d", r.StatusCode)
	}

	// project detail lists it
	var detail struct {
		Items []struct {
			ItemID   int64   `json:"item_id"`
			ItemName string  `json:"item_name"`
			QtyUsed  float64 `json:"qty_used"`
			ItemQty  float64 `json:"item_qty"`
		} `json:"items"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects/"+fmtInt64(proj.ID), nil, cookie), &detail)
	if len(detail.Items) != 1 || detail.Items[0].ItemName != "8TB HDD" || detail.Items[0].QtyUsed != 4 {
		t.Fatalf("project items = %+v", detail.Items)
	}

	// item detail shows the project
	var itemDetail struct {
		Projects []struct {
			ID      int64   `json:"id"`
			Name    string  `json:"name"`
			QtyUsed float64 `json:"qty_used"`
		} `json:"projects"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(created.ID), nil, cookie), &itemDetail)
	if len(itemDetail.Projects) != 1 || itemDetail.Projects[0].Name != "Home NAS" ||
		itemDetail.Projects[0].QtyUsed != 4 {
		t.Fatalf("item projects = %+v", itemDetail.Projects)
	}

	// update qty_used
	if r := doJSON(t, ts, "PATCH", "/api/v1/projects/"+fmtInt64(proj.ID)+"/items/"+fmtInt64(created.ID),
		map[string]any{"qty_used": 6}, cookie); r.StatusCode != 204 {
		t.Fatalf("patch project item = %d", r.StatusCode)
	}
	// status update
	if r := doJSON(t, ts, "PATCH", "/api/v1/projects/"+fmtInt64(proj.ID),
		map[string]any{"status": "active"}, cookie); r.StatusCode != 200 {
		t.Fatalf("patch project = %d", r.StatusCode)
	}

	// list
	var list struct {
		Total int64 `json:"total"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects", nil, cookie), &list)
	if list.Total != 1 {
		t.Fatalf("projects total = %d", list.Total)
	}

	// deleting the project keeps the item, severs the link
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+fmtInt64(proj.ID), nil, cookie); r.StatusCode != 204 {
		t.Fatalf("delete project = %d", r.StatusCode)
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(created.ID), nil, cookie), &itemDetail)
	if len(itemDetail.Projects) != 0 {
		t.Fatalf("item still linked after project delete: %+v", itemDetail.Projects)
	}
	if r := doJSON(t, ts, "GET", "/api/v1/items/"+fmtInt64(created.ID), nil, cookie); r.StatusCode != 200 {
		t.Fatal("item died with project")
	}
}

func TestProjectIsolationAndForeignItem(t *testing.T) {
	_, ts := newTestServer(t, nil)
	a := registerUser(t, ts, "proj-a@test.dev", "longpassword1")
	b := registerUser(t, ts, "proj-b@test.dev", "longpassword1")
	projA := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "A proj"}, a)
	var pa struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, projA, &pa)
	itemB := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "b item"}, b)
	var ib struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, itemB, &ib)

	// a cannot add b's item to a's project
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(pa.ID)+"/items",
		map[string]any{"item_id": ib.ID}, a); r.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign item add = %d, want 404", r.StatusCode)
	}
	// b cannot see a's project
	if r := doJSON(t, ts, "GET", "/api/v1/projects/"+fmtInt64(pa.ID), nil, b); r.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign project = %d, want 404", r.StatusCode)
	}
}

func TestProjectItemDeleteAbsent(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "pdel@test.dev", "longpassword1")
	proj := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "P"}, cookie)
	var p struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, proj, &p)
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "i"}, cookie)
	var it struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &it)
	if r := doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(p.ID)+"/items",
		map[string]any{"item_id": it.ID}, cookie); r.StatusCode != http.StatusCreated {
		t.Fatalf("add project item = %d", r.StatusCode)
	}
	// first delete succeeds
	if r := doJSON(t, ts, "DELETE", "/api/v1/projects/"+fmtInt64(p.ID)+"/items/"+fmtInt64(it.ID),
		nil, cookie); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete project item = %d, want 204", r.StatusCode)
	}
	// second delete: row is absent -> 404 (was 204)
	resp := doJSON(t, ts, "DELETE", "/api/v1/projects/"+fmtInt64(p.ID)+"/items/"+fmtInt64(it.ID),
		nil, cookie)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete absent project item = %d, want 404", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, resp, &body)
	if body.Error.Code != "not_found" {
		t.Fatalf("absent delete code = %q, want not_found", body.Error.Code)
	}
}

func TestProjectItemQtyUsedValidation(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "pqty@test.dev", "longpassword1")
	proj := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "P"}, cookie)
	var p struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, proj, &p)
	item := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": "i"}, cookie)
	var it struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, item, &it)

	add := func(qty float64) *http.Response {
		return doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(p.ID)+"/items",
			map[string]any{"item_id": it.ID, "qty_used": qty}, cookie)
	}
	// add: out-of-range rejected (matches items.qty 0..1e12)
	for _, bad := range []float64{-5, -0.01, 1e13} {
		if r := add(bad); r.StatusCode != http.StatusBadRequest {
			t.Fatalf("add qty_used %v = %d, want 400", bad, r.StatusCode)
		}
	}
	// add: boundaries accepted
	for _, ok := range []float64{0, 1e12} {
		if r := add(ok); r.StatusCode != http.StatusCreated {
			t.Fatalf("add qty_used %v = %d, want 201", ok, r.StatusCode)
		}
	}

	patch := func(qty float64) *http.Response {
		return doJSON(t, ts, "PATCH", "/api/v1/projects/"+fmtInt64(p.ID)+"/items/"+fmtInt64(it.ID),
			map[string]any{"qty_used": qty}, cookie)
	}
	for _, bad := range []float64{-5, 1e13} {
		if r := patch(bad); r.StatusCode != http.StatusBadRequest {
			t.Fatalf("patch qty_used %v = %d, want 400", bad, r.StatusCode)
		}
	}
	if r := patch(1e12); r.StatusCode != http.StatusNoContent {
		t.Fatalf("patch qty_used 1e12 = %d, want 204", r.StatusCode)
	}
}
