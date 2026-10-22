package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- naming rules (mirrored by web/src/utils/locationNames.ts) ----

func TestSpreadsheetLetters(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{1, "A"}, {2, "B"}, {25, "Y"}, {26, "Z"},
		{27, "AA"}, {52, "AZ"}, {53, "BA"}, {702, "ZZ"}, {703, "AAA"},
	} {
		if got := spreadsheetLetters(tc.in, false); got != tc.want {
			t.Errorf("upper(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, tc := range []struct {
		in   int
		want string
	}{
		{1, "a"}, {26, "z"}, {27, "aa"}, {28, "ab"}, {52, "az"}, {53, "ba"},
	} {
		if got := spreadsheetLetters(tc.in, true); got != tc.want {
			t.Errorf("lower(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLocationNodeName(t *testing.T) {
	for _, tc := range []struct {
		name   string
		idx    []int
		naming string
		single bool
		want   string
	}{
		{"1d compact", []int{1}, "compact", true, "1"},
		{"1d compact n", []int{12}, "compact", true, "12"},
		{"1d verbose", []int{3}, "verbose", true, "Slot 3"},
		{"2d compact", []int{3, 2}, "compact", false, "C2"},
		{"2d compact past Z", []int{27, 1}, "compact", false, "AA1"},
		{"2d verbose", []int{3, 2}, "verbose", false, "Row 3 Col 2"},
		{"3d compact", []int{1, 2, 3}, "compact", false, "A2c"},
		{"3d compact lower past z", []int{1, 1, 27}, "compact", false, "A1aa"},
		{"3d verbose", []int{3, 2, 1}, "verbose", false, "Row 3 Col 2 Bin 1"},
		{"nested intermediate compact", []int{2}, "compact", false, "B"},
		{"nested intermediate verbose", []int{2}, "verbose", false, "Row 2"},
	} {
		if got := locationNodeName(tc.idx, tc.naming, tc.single); got != tc.want {
			t.Errorf("%s: locationNodeName(%v, %q, %v) = %q, want %q",
				tc.name, tc.idx, tc.naming, tc.single, got, tc.want)
		}
	}
}

// generateBulkLocations rendered as "name|parent-path" so both the names
// and the parent wiring are pinned in one sequence.
func renderNodes(nodes []bulkLocationNode) string {
	parts := make([]string, len(nodes))
	for i, n := range nodes {
		parts[i] = n.Name + "|" + n.Parent
	}
	return strings.Join(parts, "; ")
}

func TestGenerateBulkLocations(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dims      []int
		structure string
		naming    string
		prefix    string
		want      string
	}{
		{
			"flat 2d compact", []int{2, 3}, "flat", "compact", "",
			"A1|; A2|; A3|; B1|; B2|; B3|",
		},
		{
			"flat 2d compact prefixed", []int{2, 2}, "flat", "compact", "Bin",
			"Bin A1|; Bin A2|; Bin B1|; Bin B2|",
		},
		{
			"flat 3d compact", []int{2, 2, 2}, "flat", "compact", "",
			"A1a|; A1b|; A2a|; A2b|; B1a|; B1b|; B2a|; B2b|",
		},
		{
			"flat 1d compact", []int{3}, "flat", "compact", "",
			"1|; 2|; 3|",
		},
		{
			"flat 1d verbose", []int{3}, "flat", "verbose", "",
			"Slot 1|; Slot 2|; Slot 3|",
		},
		{
			"nested 2d compact", []int{2, 3}, "nested", "compact", "",
			"A|; A1|1; A2|1; A3|1; B|; B1|2; B2|2; B3|2",
		},
		{
			// prefix applies to LEAF names only - intermediates stay bare
			"nested 2d compact prefixed", []int{2, 2}, "nested", "compact", "Bin",
			"A|; Bin A1|1; Bin A2|1; B|; Bin B1|2; Bin B2|2",
		},
		{
			"nested 2d verbose prefixed", []int{2, 2}, "nested", "verbose", "Shelf",
			"Row 1|; Shelf Row 1 Col 1|1; Shelf Row 1 Col 2|1; Row 2|; Shelf Row 2 Col 1|2; Shelf Row 2 Col 2|2",
		},
		{
			"nested 3d compact", []int{2, 2, 2}, "nested", "compact", "",
			"A|; A1|1; A1a|1.1; A1b|1.1; A2|1; A2a|1.2; A2b|1.2; B|; B1|2; B1a|2.1; B1b|2.1; B2|2; B2a|2.2; B2b|2.2",
		},
	} {
		got := renderNodes(generateBulkLocations(tc.dims, tc.structure, tc.naming, tc.prefix))
		if got != tc.want {
			t.Errorf("%s: generate =\n  %s\nwant\n  %s", tc.name, got, tc.want)
		}
	}
}

// ---- HTTP helpers ----

type bulkLocRow struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ParentID *int64 `json:"parent_id"`
	Notes    string `json:"notes"`
}

func listLocRows(t *testing.T, ts *httptest.Server, cookie *http.Cookie) []bulkLocRow {
	t.Helper()
	var body struct {
		Data []bulkLocRow `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/locations", nil, cookie), &body)
	return body.Data
}

func childRows(locs []bulkLocRow, parent int64) []bulkLocRow {
	var out []bulkLocRow
	for _, l := range locs {
		if l.ParentID != nil && *l.ParentID == parent {
			out = append(out, l)
		}
	}
	return out // ListLocations is ORDER BY name already
}

func rowNames(rows []bulkLocRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}

func subtreeIDs(locs []bulkLocRow, root int64) map[int64]bool {
	ids := map[int64]bool{root: true}
	for changed := true; changed; {
		changed = false
		for _, l := range locs {
			if l.ParentID != nil && ids[*l.ParentID] && !ids[l.ID] {
				ids[l.ID] = true
				changed = true
			}
		}
	}
	return ids
}

func namesEq(t *testing.T, ctx string, got, want []string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s: names = %v, want %v", ctx, got, want)
	}
}

// ---- POST /locations/bulk ----

func TestBulkCreateLocationsFlat(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "bulk-flat@test.dev", "longpassword1")
	garage := createLocation(t, ts, cookie, "Garage", nil)

	resp := doJSON(t, ts, "POST", "/api/v1/locations/bulk", map[string]any{
		"name": "Shelves", "parent_id": garage, "dims": []int{2, 3},
		"structure": "flat", "naming": "compact", "prefix": "Bin",
	}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bulk flat = %d, want 201", resp.StatusCode)
	}
	var body struct {
		ID              int64  `json:"id"`
		Name            string `json:"name"`
		ParentID        *int64 `json:"parent_id"`
		ChildrenCreated int    `json:"children_created"`
	}
	decodeBody(t, resp, &body)
	if body.Name != "Shelves" || body.ParentID == nil || *body.ParentID != garage {
		t.Fatalf("root = %+v", body)
	}
	if body.ChildrenCreated != 6 {
		t.Fatalf("children_created = %d, want 6", body.ChildrenCreated)
	}

	locs := listLocRows(t, ts, cookie)
	namesEq(t, "flat children", rowNames(childRows(locs, body.ID)),
		[]string{"Bin A1", "Bin A2", "Bin A3", "Bin B1", "Bin B2", "Bin B3"})
}

func TestBulkCreateLocationsFlat3D(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "bulk-3d@test.dev", "longpassword1")

	resp := doJSON(t, ts, "POST", "/api/v1/locations/bulk", map[string]any{
		"name": "Wall", "dims": []int{2, 2, 2},
		"structure": "flat", "naming": "compact",
	}, cookie)
	var body struct {
		ID              int64 `json:"id"`
		ChildrenCreated int   `json:"children_created"`
	}
	decodeBody(t, resp, &body)
	if resp.StatusCode != http.StatusCreated || body.ChildrenCreated != 8 {
		t.Fatalf("bulk 3d = %d children=%d, want 201/8", resp.StatusCode, body.ChildrenCreated)
	}
	namesEq(t, "3d flat children", rowNames(childRows(listLocRows(t, ts, cookie), body.ID)),
		[]string{"A1a", "A1b", "A2a", "A2b", "B1a", "B1b", "B2a", "B2b"})
}

func TestBulkCreateLocations1D(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "bulk-1d@test.dev", "longpassword1")

	resp := doJSON(t, ts, "POST", "/api/v1/locations/bulk", map[string]any{
		"name": "Rail", "dims": []int{3}, "structure": "flat", "naming": "compact",
	}, cookie)
	var body struct {
		ID              int64 `json:"id"`
		ChildrenCreated int   `json:"children_created"`
	}
	decodeBody(t, resp, &body)
	if body.ChildrenCreated != 3 {
		t.Fatalf("children_created = %d, want 3", body.ChildrenCreated)
	}
	namesEq(t, "1d children", rowNames(childRows(listLocRows(t, ts, cookie), body.ID)),
		[]string{"1", "2", "3"})
}

func TestBulkCreateLocationsNested(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "bulk-nest@test.dev", "longpassword1")

	resp := doJSON(t, ts, "POST", "/api/v1/locations/bulk", map[string]any{
		"name": "Cabinet", "dims": []int{2, 3},
		"structure": "nested", "naming": "verbose",
	}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bulk nested = %d, want 201", resp.StatusCode)
	}
	var body struct {
		ID              int64 `json:"id"`
		ChildrenCreated int   `json:"children_created"`
	}
	decodeBody(t, resp, &body)
	if body.ChildrenCreated != 8 { // 2 rows + 6 cells
		t.Fatalf("children_created = %d, want 8", body.ChildrenCreated)
	}

	locs := listLocRows(t, ts, cookie)
	namesEq(t, "nested level 0", rowNames(childRows(locs, body.ID)),
		[]string{"Row 1", "Row 2"})
	var row1 int64
	for _, l := range locs {
		if l.Name == "Row 1" {
			row1 = l.ID
		}
	}
	namesEq(t, "nested level 1", rowNames(childRows(locs, row1)),
		[]string{"Row 1 Col 1", "Row 1 Col 2", "Row 1 Col 3"})
}

func TestBulkCreateLocationsValidation(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "bulk-val@test.dev", "longpassword1")
	valid := func() map[string]any {
		return map[string]any{
			"name": "Grid", "dims": []int{2, 2},
			"structure": "flat", "naming": "compact",
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		msg    string
	}{
		{"empty name", func(b map[string]any) { b["name"] = "" }, "name"},
		{"whitespace name", func(b map[string]any) { b["name"] = " \t " }, "name"},
		{"absent dims", func(b map[string]any) { delete(b, "dims") }, "dims"},
		{"empty dims", func(b map[string]any) { b["dims"] = []int{} }, "dims"},
		{"four dims", func(b map[string]any) { b["dims"] = []int{1, 2, 3, 4} }, "dims"},
		{"dim zero", func(b map[string]any) { b["dims"] = []int{0, 2} }, "1 and 100"},
		{"dim 101", func(b map[string]any) { b["dims"] = []int{101} }, "1 and 100"},
		{"product 1001", func(b map[string]any) { b["dims"] = []int{11, 91} }, "1000"},
		{"bad structure", func(b map[string]any) { b["structure"] = "matrix" }, "structure"},
		{"bad naming", func(b map[string]any) { b["naming"] = "numbers" }, "naming"},
		{"prefix 51", func(b map[string]any) { b["prefix"] = strings.Repeat("x", 51) }, "prefix"},
	} {
		body := valid()
		tc.mutate(body)
		resp := doJSON(t, ts, "POST", "/api/v1/locations/bulk", body, cookie)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", tc.name, resp.StatusCode)
			continue
		}
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		decodeBody(t, resp, &e)
		if e.Error.Code != "validation_error" {
			t.Errorf("%s: code = %q, want validation_error", tc.name, e.Error.Code)
		}
		if !strings.Contains(e.Error.Message, tc.msg) {
			t.Errorf("%s: message = %q, want it to contain %q", tc.name, e.Error.Message, tc.msg)
		}
	}
	// validation precedes any write: nothing was created
	if locs := listLocRows(t, ts, cookie); len(locs) != 0 {
		t.Fatalf("invalid requests created %d locations", len(locs))
	}
}

func TestBulkCreateLocationsParentNotFound(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "bulk-alice@test.dev", "longpassword1")
	bob := registerUser(t, ts, "bulk-bob@test.dev", "longpassword1")
	foreign := createLocation(t, ts, alice, "Alice's shelf", nil)

	resp := doJSON(t, ts, "POST", "/api/v1/locations/bulk", map[string]any{
		"name": "Bob grid", "parent_id": foreign, "dims": []int{2},
		"structure": "flat", "naming": "compact",
	}, bob)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign parent = %d, want 404", resp.StatusCode)
	}
	if locs := listLocRows(t, ts, bob); len(locs) != 0 {
		t.Fatalf("nothing should be created for bob, got %d", len(locs))
	}
}

func TestBulkCreateLocationsDuplicateName(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "bulk-dup@test.dev", "longpassword1")
	createLocation(t, ts, cookie, "Shelf", nil)
	resp := doJSON(t, ts, "POST", "/api/v1/locations/bulk", map[string]any{
		"name": "Shelf", "dims": []int{2}, "structure": "flat", "naming": "compact",
	}, cookie)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate root name = %d, want 409", resp.StatusCode)
	}
}

// ---- POST /locations/{id}/copy-layout ----

func TestCopyLocationLayout(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "copy@test.dev", "longpassword1")

	cabinet := createLocation(t, ts, cookie, "Cabinet", nil)
	d1 := createLocation(t, ts, cookie, "Drawer 1", cabinet)
	createLocation(t, ts, cookie, "Drawer 2", cabinet)
	createLocation(t, ts, cookie, "Bin A", d1)
	createLocation(t, ts, cookie, "Bin B", d1)
	var binC int64
	{
		for _, l := range listLocRows(t, ts, cookie) {
			if l.Name == "Drawer 2" {
				binC = createLocation(t, ts, cookie, "Bin C", l.ID)
			}
		}
	}
	// an item living in the source drawer must NOT ride along
	mustExec(t, s, `INSERT INTO items (user_id, name, qty, location_id, visibility, created_at, updated_at)
		SELECT id, 'widget', 1, ?, 'private', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z' FROM users LIMIT 1`, d1)

	resp := doJSON(t, ts, "POST", "/api/v1/locations/"+fmtInt64(cabinet)+"/copy-layout",
		map[string]any{"name": "Cabinet copy"}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("copy = %d, want 201", resp.StatusCode)
	}
	var body struct {
		ID              int64  `json:"id"`
		Name            string `json:"name"`
		ParentID        *int64 `json:"parent_id"`
		Notes           string `json:"notes"`
		ChildrenCreated int    `json:"children_created"`
	}
	decodeBody(t, resp, &body)
	if body.Name != "Cabinet copy" || body.ParentID != nil || body.ChildrenCreated != 5 {
		t.Fatalf("copy root = %+v", body)
	}
	if body.Notes != "" { // names + shape only
		t.Fatalf("copy notes = %q, want empty", body.Notes)
	}

	locs := listLocRows(t, ts, cookie)
	drawers := childRows(locs, body.ID)
	namesEq(t, "copy drawers", rowNames(drawers), []string{"Drawer 1", "Drawer 2"})
	var newD1 int64
	for _, d := range drawers {
		if d.Name == "Drawer 1" {
			newD1 = d.ID
		}
	}
	namesEq(t, "copy drawer 1 bins", rowNames(childRows(locs, newD1)), []string{"Bin A", "Bin B"})
	namesEq(t, "copy drawer 2 bins", rowNames(childRows(locs, drawers[1].ID)), []string{"Bin C"})

	// items are never copied (and notes are empty on every copy)
	sub := subtreeIDs(locs, body.ID)
	ids := make([]string, 0, len(sub))
	for id := range sub {
		ids = append(ids, fmtInt64(id))
	}
	var n int64
	mustQueryRow(t, s, `SELECT COUNT(*) FROM items WHERE location_id IN (`+strings.Join(ids, ",")+`)`).Scan(&n)
	if n != 0 {
		t.Fatalf("copy has %d items, want 0", n)
	}
	for _, l := range locs {
		if sub[l.ID] && l.Notes != "" {
			t.Fatalf("copied location %q has notes %q", l.Name, l.Notes)
		}
	}

	// copying into the source's own subtree is rejected (child, self)
	for _, parent := range []int64{binC, cabinet} {
		resp := doJSON(t, ts, "POST", "/api/v1/locations/"+fmtInt64(cabinet)+"/copy-layout",
			map[string]any{"name": "X", "parent_id": parent}, cookie)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("copy into own subtree (parent %d) = %d, want 400", parent, resp.StatusCode)
		}
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		decodeBody(t, resp, &e)
		if e.Error.Code != "validation_error" || e.Error.Message != "cannot copy a location into its own subtree" {
			t.Fatalf("subtree copy error = %+v", e.Error)
		}
	}

	// empty name is a plain validation error; parent_id 0 = root
	if resp := doJSON(t, ts, "POST", "/api/v1/locations/"+fmtInt64(cabinet)+"/copy-layout",
		map[string]any{"name": "  "}, cookie); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty name copy = %d, want 400", resp.StatusCode)
	}
	resp = doJSON(t, ts, "POST", "/api/v1/locations/"+fmtInt64(cabinet)+"/copy-layout",
		map[string]any{"name": "Cabinet copy 2", "parent_id": 0}, cookie)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("root-level copy = %d, want 201", resp.StatusCode)
	}
	var second struct {
		ParentID        *int64 `json:"parent_id"`
		ChildrenCreated int    `json:"children_created"`
	}
	decodeBody(t, resp, &second)
	if second.ParentID != nil || second.ChildrenCreated != 5 {
		t.Fatalf("root-level copy = %+v", second)
	}
}

func TestCopyLocationLayoutForeignSource(t *testing.T) {
	_, ts := newTestServer(t, nil)
	alice := registerUser(t, ts, "copy-alice@test.dev", "longpassword1")
	bob := registerUser(t, ts, "copy-bob@test.dev", "longpassword1")
	foreign := createLocation(t, ts, alice, "Alice's cabinet", nil)

	resp := doJSON(t, ts, "POST", "/api/v1/locations/"+fmtInt64(foreign)+"/copy-layout",
		map[string]any{"name": "Bob copy"}, bob)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign source = %d, want 404", resp.StatusCode)
	}
}

func TestCopyLocationLayoutTooLarge(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "copy-big@test.dev", "longpassword1")

	// bulk-build 1000 children + root = 1001 nodes
	resp := doJSON(t, ts, "POST", "/api/v1/locations/bulk", map[string]any{
		"name": "Big", "dims": []int{10, 100}, "structure": "flat", "naming": "compact",
	}, cookie)
	var big struct {
		ID              int64 `json:"id"`
		ChildrenCreated int   `json:"children_created"`
	}
	decodeBody(t, resp, &big)
	if resp.StatusCode != http.StatusCreated || big.ChildrenCreated != 1000 {
		t.Fatalf("setup bulk = %d children=%d", resp.StatusCode, big.ChildrenCreated)
	}

	resp = doJSON(t, ts, "POST", "/api/v1/locations/"+fmtInt64(big.ID)+"/copy-layout",
		map[string]any{"name": "Big copy"}, cookie)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized copy = %d, want 400", resp.StatusCode)
	}
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeBody(t, resp, &e)
	if e.Error.Code != "validation_error" || !strings.Contains(e.Error.Message, "1000") {
		t.Fatalf("oversized copy error = %+v", e.Error)
	}
}
