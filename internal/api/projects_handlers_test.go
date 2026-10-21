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
