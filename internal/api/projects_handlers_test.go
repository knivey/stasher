package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// mkItem creates an item with the user's default visibility.
// mkItemS creates one explicitly shared. Neither sets extra fields.
func mkItem(t *testing.T, ts *httptest.Server, cookie *http.Cookie, name string) int64 {
	resp := doJSON(t, ts, "POST", "/api/v1/items", map[string]any{"name": name}, cookie)
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &item)
	return item.ID
}

func mkItemS(t *testing.T, ts *httptest.Server, cookie *http.Cookie, name string) int64 {
	resp := doJSON(t, ts, "POST", "/api/v1/items",
		map[string]any{"name": name, "visibility": "shared"}, cookie)
	var item struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &item)
	return item.ID
}

func TestProjectVisibility(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "projvis@test.dev", "longpassword1")

	// create: defaults to private
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "P1"}, cookie)
	var created struct {
		Visibility string `json:"visibility"`
	}
	decodeBody(t, resp, &created)
	if created.Visibility != "private" {
		t.Fatalf("default visibility = %q, want private", created.Visibility)
	}

	// create: explicit shared
	resp = doJSON(t, ts, "POST", "/api/v1/projects",
		map[string]any{"name": "P2", "visibility": "shared"}, cookie)
	var created2 struct {
		ID         int64  `json:"id"`
		Visibility string `json:"visibility"`
	}
	decodeBody(t, resp, &created2)
	if created2.Visibility != "shared" {
		t.Fatalf("visibility = %q, want shared", created2.Visibility)
	}

	// PATCH: flip + invalid value + absent-keeps
	resp = doJSON(t, ts, "PATCH", "/api/v1/projects/"+fmtInt64(created2.ID),
		map[string]any{"visibility": "private"}, cookie)
	var patched struct {
		Visibility string `json:"visibility"`
	}
	decodeBody(t, resp, &patched)
	if patched.Visibility != "private" {
		t.Fatalf("patched visibility = %q, want private", patched.Visibility)
	}
	resp = doJSON(t, ts, "PATCH", "/api/v1/projects/"+fmtInt64(created2.ID),
		map[string]any{"visibility": "public"}, cookie)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid visibility status = %d, want 400", resp.StatusCode)
	}
	resp = doJSON(t, ts, "PATCH", "/api/v1/projects/"+fmtInt64(created2.ID),
		map[string]any{"status": "active"}, cookie) // visibility absent → stays
	var after struct {
		Visibility string `json:"visibility"`
	}
	decodeBody(t, resp, &after)
	if after.Visibility != "private" {
		t.Fatalf("absent visibility changed: %q", after.Visibility)
	}

	// list carries visibility
	var list struct {
		Data []struct {
			Visibility string `json:"visibility"`
		} `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/projects", nil, cookie), &list)
	if len(list.Data) != 2 {
		t.Fatalf("list len = %d, want 2", len(list.Data))
	}
}

func TestProjectShareeAccessAndBOMMasking(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pom@test.dev", "longpassword1")
	sharee := registerUser(t, ts, "psh@test.dev", "longpassword1")

	// owner: two items (one private, one shared), a project with both, shared
	itPriv := mkItem(t, ts, owner, "Priv part")
	itSh := mkItemS(t, ts, owner, "Shared part") // helper sets visibility shared
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Bench PSU", "visibility": "shared"}, owner)
	var proj struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &proj)
	doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(proj.ID)+"/items", map[string]any{"item_id": itPriv, "qty_used": 2}, owner)
	doJSON(t, ts, "POST", "/api/v1/projects/"+fmtInt64(proj.ID)+"/items", map[string]any{"item_id": itSh, "qty_used": 1, "notes": "secret link note"}, owner)

	// sharee GET: 200, masked rows, owner_view false
	resp = doJSON(t, ts, "GET", "/api/v1/projects/"+fmtInt64(proj.ID), nil, sharee)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sharee GET = %d", resp.StatusCode)
	}
	var detail map[string]any
	decodeBody(t, resp, &detail)
	if detail["owner_view"] != false {
		t.Fatalf("owner_view = %v", detail["owner_view"])
	}
	rows := detail["items"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	for _, r := range rows {
		m := r.(map[string]any)
		for _, banned := range []string{"item_id", "item_qty", "item_unit", "notes", "added_at"} {
			if _, has := m[banned]; has {
				t.Fatalf("sharee row leaks %q: %v", banned, m)
			}
		}
		if m["item_name"] == "" {
			t.Fatalf("row missing item_name: %v", m)
		}
	}

	// owner GET: full rows with shared flags, owner_view true
	resp = doJSON(t, ts, "GET", "/api/v1/projects/"+fmtInt64(proj.ID), nil, owner)
	decodeBody(t, resp, &detail)
	if detail["owner_view"] != true {
		t.Fatalf("owner owner_view = %v", detail["owner_view"])
	}
	for _, r := range detail["items"].([]any) {
		m := r.(map[string]any)
		if _, has := m["item_id"]; !has {
			t.Fatalf("owner row missing item_id: %v", m)
		}
	}
}

func TestProjectShareePrivate404(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pom2@test.dev", "longpassword1")
	sharee := registerUser(t, ts, "psh2@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Secret"}, owner)
	var proj struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, resp, &proj)
	if r := doJSON(t, ts, "GET", "/api/v1/projects/"+fmtInt64(proj.ID), nil, sharee); r.StatusCode != http.StatusNotFound {
		t.Fatalf("sharee GET private = %d, want 404", r.StatusCode)
	}
}

func TestListSharedProjects(t *testing.T) {
	_, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "pom3@test.dev", "longpassword1")
	other := registerUser(t, ts, "psh3@test.dev", "longpassword1")
	doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Public", "visibility": "shared"}, owner)
	doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Hidden"}, owner)
	doJSON(t, ts, "POST", "/api/v1/projects", map[string]any{"name": "Mine shared", "visibility": "shared"}, other)

	var list struct {
		Data []struct {
			Name      string `json:"name"`
			OwnerName string `json:"owner_name"`
		} `json:"data"`
		Total int64 `json:"total"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/shared/projects", nil, other), &list)
	if len(list.Data) != 1 || list.Data[0].Name != "Public" || list.Data[0].OwnerName == "" {
		t.Fatalf("shared projects = %+v, want only Public with owner name", list.Data)
	}
	// owner's own view: also only others' projects — his own shared
	// "Public" must never appear (but other's "Mine shared" must)
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/shared/projects", nil, owner), &list)
	if len(list.Data) != 1 || list.Data[0].Name != "Mine shared" {
		t.Fatalf("owner's shared list = %+v, want only other's 'Mine shared'", list.Data)
	}
}

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
