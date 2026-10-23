package api

import (
	"fmt"
	"net/http"
	"testing"
)

// insertLocatedItem inserts an item owned by email parked at locID (raw SQL
// like TestDeleteLocationReparentsAndClearsItems).
func insertLocatedItem(t *testing.T, s *Server, email, name string, locID int64) {
	t.Helper()
	mustExec(t, s, `INSERT INTO items (user_id, name, qty, location_id, visibility, created_at, updated_at)
		SELECT id, ?, 1, ?, 'private', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z' FROM users WHERE email = ?`,
		name, locID, email)
}

func locationExists(t *testing.T, s *Server, id int64) bool {
	t.Helper()
	var n int64
	if err := s.raw.QueryRow(`SELECT COUNT(*) FROM locations WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func itemsAt(t *testing.T, s *Server, locID int64) int64 {
	t.Helper()
	var n int64
	if err := s.raw.QueryRow(`SELECT COUNT(*) FROM items WHERE location_id = ?`, locID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeleteLocationSubtree(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "subtree@test.dev", "longpassword1")
	sib := createLocation(t, ts, cookie, "Sib", nil)
	root := createLocation(t, ts, cookie, "Root", nil)
	a := createLocation(t, ts, cookie, "A", root)
	a1 := createLocation(t, ts, cookie, "A1", a)
	a2 := createLocation(t, ts, cookie, "A2", a)
	insertLocatedItem(t, s, "subtree@test.dev", "in-a1", a1)
	insertLocatedItem(t, s, "subtree@test.dev", "in-a2", a2)

	resp := doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(root)+"?subtree=true", nil, cookie)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("subtree delete = %d, want 204", resp.StatusCode)
	}
	for _, id := range []int64{root, a, a1, a2} {
		if locationExists(t, s, id) {
			t.Fatalf("location %d survived the subtree delete", id)
		}
	}
	// the root-level location OUTSIDE the subtree is untouched
	if !locationExists(t, s, sib) {
		t.Fatal("the top-level sibling was deleted (it is not in the subtree)")
	}
	var parent any
	if err := s.raw.QueryRow(`SELECT parent_id FROM locations WHERE id = ?`, sib).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if parent != nil {
		t.Fatalf("sibling parent_id = %v, want NULL", parent)
	}
	// items ALWAYS survive and become unlocated
	var surviving, unlocated int64
	if err := s.raw.QueryRow(`SELECT COUNT(*) FROM items WHERE name IN ('in-a1','in-a2')`).Scan(&surviving); err != nil {
		t.Fatal(err)
	}
	if err := s.raw.QueryRow(`SELECT COUNT(*) FROM items WHERE name IN ('in-a1','in-a2') AND location_id IS NULL`).Scan(&unlocated); err != nil {
		t.Fatal(err)
	}
	if surviving != 2 || unlocated != 2 {
		t.Fatalf("items surviving=%d unlocated=%d, want 2/2", surviving, unlocated)
	}
}

// TestDeleteLocationDefaultReparentsChildrenUp pins the DEFAULT delete (no
// ?subtree param): exactly ONE row dies, the deleted node's children
// reparent to ITS parent, items of surviving locations keep their location.
func TestDeleteLocationDefaultReparentsChildrenUp(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "defdel@test.dev", "longpassword1")
	sib := createLocation(t, ts, cookie, "Sib", nil)
	root := createLocation(t, ts, cookie, "Root", nil)
	a := createLocation(t, ts, cookie, "A", root)
	a1 := createLocation(t, ts, cookie, "A1", a)
	a2 := createLocation(t, ts, cookie, "A2", a)
	insertLocatedItem(t, s, "defdel@test.dev", "in-a1", a1)

	resp := doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(a), nil, cookie)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("default delete = %d, want 204", resp.StatusCode)
	}
	if locationExists(t, s, a) {
		t.Fatal("A survived its own delete")
	}
	for _, id := range []int64{root, a1, a2, sib} {
		if !locationExists(t, s, id) {
			t.Fatalf("location %d should have survived the default delete", id)
		}
	}
	for _, id := range []int64{a1, a2} {
		var parent int64
		if err := s.raw.QueryRow(`SELECT parent_id FROM locations WHERE id = ?`, id).Scan(&parent); err != nil {
			t.Fatal(err)
		}
		if parent != root {
			t.Fatalf("location %d parent = %d, want root %d (children of the deleted node reparent up)", id, parent, root)
		}
	}
	// the item parked in surviving A1 keeps its location
	if n := itemsAt(t, s, a1); n != 1 {
		t.Fatalf("items still in A1 = %d, want 1", n)
	}
}

// TestDeleteLocationSubtreeFalseMatchesDefault pins ?subtree=false: the
// EXACT legacy behavior, children reparent up.
func TestDeleteLocationSubtreeFalseMatchesDefault(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "falsedel@test.dev", "longpassword1")
	root := createLocation(t, ts, cookie, "Root", nil)
	a := createLocation(t, ts, cookie, "A", root)
	a1 := createLocation(t, ts, cookie, "A1", a)

	resp := doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(a)+"?subtree=false", nil, cookie)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("subtree=false delete = %d, want 204", resp.StatusCode)
	}
	if locationExists(t, s, a) || !locationExists(t, s, a1) || !locationExists(t, s, root) {
		t.Fatal("subtree=false must die alone: only the named row goes")
	}
	var parent int64
	if err := s.raw.QueryRow(`SELECT parent_id FROM locations WHERE id = ?`, a1).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if parent != root {
		t.Fatalf("A1 parent = %d, want root %d", parent, root)
	}
}

// TestDeleteLocationSubtreeRemovesDescendantPhotoFiles: a DESCENDANT's
// picture files must leave the disk too, not just the root row's.
func TestDeleteLocationSubtreeRemovesDescendantPhotoFiles(t *testing.T) {
	s, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "subtreepic@test.dev", "longpassword1")
	root := createLocation(t, ts, owner, "Root", nil)
	child := createLocation(t, ts, owner, "Child", root)

	up := postFile(t, ts, "/api/v1/locations/"+fmtInt64(child)+"/photo",
		"file", "child.jpg", jpegBytes(t, 600, 400), owner)
	if up.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d", up.StatusCode)
	}
	var body struct {
		PhotoKey string `json:"photo_key"`
		ThumbKey string `json:"thumb_key"`
	}
	decodeBody(t, up, &body)

	resp := doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(root)+"?subtree=true", nil, owner)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("subtree delete = %d, want 204", resp.StatusCode)
	}
	if fileOnDisk(t, s, body.PhotoKey) {
		t.Fatal("descendant's photo file still on disk after subtree delete")
	}
	if body.ThumbKey != "" && fileOnDisk(t, s, body.ThumbKey) {
		t.Fatal("descendant's thumb file still on disk after subtree delete")
	}
}

// TestDeleteLocationSubtreeLeaf: a childless location with subtree=true is
// just the single delete (no children to reparent, nothing extra dies).
func TestDeleteLocationSubtreeLeaf(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "leafsub@test.dev", "longpassword1")
	lone := createLocation(t, ts, cookie, "Lone", nil)
	insertLocatedItem(t, s, "leafsub@test.dev", "lone-item", lone)

	resp := doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(lone)+"?subtree=true", nil, cookie)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("leaf subtree delete = %d, want 204", resp.StatusCode)
	}
	if locationExists(t, s, lone) {
		t.Fatal("leaf survived its subtree delete")
	}
	var unlocated int64
	if err := s.raw.QueryRow(`SELECT COUNT(*) FROM items WHERE name='lone-item' AND location_id IS NULL`).Scan(&unlocated); err != nil {
		t.Fatal(err)
	}
	if unlocated != 1 {
		t.Fatalf("unlocated lone-item = %d, want 1", unlocated)
	}
}

func TestDeleteLocationSubtreeRejects(t *testing.T) {
	s, ts := newTestServer(t, nil)
	owner := registerUser(t, ts, "subfor@test.dev", "longpassword1")
	other := registerUser(t, ts, "subfor-other@test.dev", "longpassword1")
	foreign := createLocation(t, ts, other, "Not Yours", nil)
	foreignChild := createLocation(t, ts, other, "Not Yours Jr", foreign)
	mine := createLocation(t, ts, owner, "Mine", nil)

	// foreign subtree -> 404 FIRST, nothing of the other user's is deleted
	if resp := doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(foreign)+"?subtree=true", nil, owner); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign subtree delete = %d, want 404", resp.StatusCode)
	}
	if !locationExists(t, s, foreign) || !locationExists(t, s, foreignChild) {
		t.Fatal("a foreign subtree delete must delete nothing")
	}

	// invalid param value -> 400 validation_error, nothing deleted
	resp := doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(mine)+"?subtree=yes", nil, owner)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad subtree param = %d, want 400", resp.StatusCode)
	}
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, resp, &errBody)
	if errBody.Error.Code != "validation_error" {
		t.Fatalf("error code = %q, want validation_error", errBody.Error.Code)
	}
	if !locationExists(t, s, mine) {
		t.Fatal("a rejected param must delete nothing")
	}
}

// TestDeleteLocationSubtreeDepthCap: a 60-deep chain must not hang or error;
// the documented depth<50 cap bounds the collected subtree (51 rows die:
// depths 0..50) and the 9 locations below the cut survive.
func TestDeleteLocationSubtreeDepthCap(t *testing.T) {
	s, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "deep@test.dev", "longpassword1")
	prev := createLocation(t, ts, cookie, "L0", nil)
	ids := []int64{prev}
	for i := 1; i < 60; i++ {
		prev = createLocation(t, ts, cookie, fmt.Sprintf("L%d", i), prev)
		ids = append(ids, prev)
	}

	resp := doJSON(t, ts, "DELETE", "/api/v1/locations/"+fmtInt64(ids[0])+"?subtree=true", nil, cookie)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("deep subtree delete = %d, want 204", resp.StatusCode)
	}
	for i, id := range ids {
		gone := !locationExists(t, s, id)
		if i < 51 && !gone {
			t.Fatalf("location at depth %d survived the capped delete", i)
		}
		if i >= 51 && gone {
			t.Fatalf("location at depth %d is below the depth cap and must survive", i)
		}
	}
}
