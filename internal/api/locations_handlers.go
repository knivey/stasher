package api

import (
	"database/sql"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

func locationJSON(l dbpkg.Location) map[string]any {
	var parent any
	if l.ParentID.Valid {
		parent = l.ParentID.Int64
	}
	return map[string]any{
		"id": l.ID, "name": l.Name, "parent_id": parent, "notes": l.Notes,
		"created_at": l.CreatedAt, "updated_at": l.UpdatedAt,
	}
}

type locationReq struct {
	Name string `json:"name"`
	// Notes is a pointer so PATCH can distinguish absent/null (keep current
	// notes) from an explicit string - even empty - (replace). On create,
	// absent simply means "" (final-review Finding 3).
	Notes    *string `json:"notes"`
	ParentID *int64  `json:"parent_id"`
}

// checkParent verifies parent is owned by u (or nil). Returns param for SQL.
func (s *Server) checkParent(w http.ResponseWriter, r *http.Request, u dbpkg.User, parentID *int64) (sql.NullInt64, bool) {
	if parentID == nil {
		return sql.NullInt64{}, true
	}
	if _, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: *parentID, UserID: u.ID}); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "parent location not found")
		return sql.NullInt64{}, false
	}
	return sqlNullInt64(*parentID), true
}

func (s *Server) handleCreateLocation(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req locationReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || len(req.Name) > 200 {
		writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
		return
	}
	parent, ok := s.checkParent(w, r, u, req.ParentID)
	if !ok {
		return
	}
	// app-logic uniqueness: (user_id, parent, name)
	if existing, err := s.q.GetLocationByName(r.Context(), dbpkg.GetLocationByNameParams{
		UserID: u.ID, Name: req.Name, ParentID: parent,
	}); err == nil && existing.ID != 0 {
		writeErr(w, http.StatusConflict, "location_exists",
			"a location with this name already exists under this parent")
		return
	}
	notes := "" // absent notes on create = empty string
	if req.Notes != nil {
		notes = *req.Notes
	}
	now := time.Now().UTC().Format(time.RFC3339)
	loc, err := s.q.CreateLocation(r.Context(), dbpkg.CreateLocationParams{
		UserID: u.ID, Name: req.Name, ParentID: parent, Notes: notes,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, locationJSON(loc))
}

func (s *Server) handleGetLocation(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid location id")
		return
	}
	loc, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "location not found")
		return
	}
	crumbs, err := s.q.LocationBreadcrumbs(r.Context(), dbpkg.LocationBreadcrumbsParams{
		ID: id, UserID: u.ID, UserID_2: u.ID,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(crumbs))
	for _, c := range crumbs {
		out = append(out, map[string]any{"id": c.ID, "name": c.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"location": locationJSON(loc), "breadcrumbs": out})
}

func (s *Server) handleListLocations(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	locs, err := s.q.ListLocations(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(locs))
	for _, l := range locs {
		out = append(out, locationJSON(l))
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: int64(len(out))})
}

func (s *Server) handleUpdateLocation(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid location id")
		return
	}
	loc, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "location not found")
		return
	}
	var req locationReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != "" { // empty name = keep current
		if len(req.Name) > 200 {
			writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
			return
		}
		loc.Name = req.Name
	}
	if req.Notes != nil { // absent/null keeps current notes; a provided string replaces
		loc.Notes = *req.Notes
	}

	var parent sql.NullInt64
	if req.ParentID != nil { // provided parent is validated + cycle-checked; absent/null keeps the current parent
		if *req.ParentID == id {
			writeErr(w, http.StatusBadRequest, "cycle", "a location cannot be its own parent")
			return
		}
		p, ok := s.checkParent(w, r, u, req.ParentID)
		if !ok {
			return
		}
		// cycle check: new parent must not be a descendant of id
		desc, err := s.q.LocationDescendants(r.Context(), dbpkg.LocationDescendantsParams{
			ID: id, UserID: u.ID, UserID_2: u.ID,
		})
		if err != nil {
			s.internalErr(w, err)
			return
		}
		for _, d := range desc {
			if d == *req.ParentID {
				writeErr(w, http.StatusBadRequest, "cycle", "cannot move a location under its own descendant")
				return
			}
		}
		parent = p
	} else {
		parent = loc.ParentID
	}

	// app-logic uniqueness check against the new (name, parent) combo
	if existing, err := s.q.GetLocationByName(r.Context(), dbpkg.GetLocationByNameParams{
		UserID: u.ID, Name: loc.Name, ParentID: parent,
	}); err == nil && existing.ID != id {
		writeErr(w, http.StatusConflict, "location_exists",
			"a location with this name already exists under this parent")
		return
	}

	if err := s.q.UpdateLocation(r.Context(), dbpkg.UpdateLocationParams{
		Name: loc.Name, Notes: loc.Notes, ParentID: parent,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339), ID: id, UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	loc, _ = s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: id, UserID: u.ID})
	writeJSON(w, http.StatusOK, locationJSON(loc))
}

func (s *Server) handleDeleteLocation(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid location id")
		return
	}
	loc, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "location not found")
		return
	}
	tx, err := s.raw.BeginTx(r.Context(), nil)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	defer tx.Rollback()
	qtx := dbpkg.New(tx)
	now := time.Now().UTC().Format(time.RFC3339)
	if err := qtx.ReparentChildren(r.Context(), dbpkg.ReparentChildrenParams{
		ParentID: loc.ParentID, ParentID_2: sqlNullInt64(id), UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	if err := qtx.ClearItemsLocation(r.Context(), dbpkg.ClearItemsLocationParams{
		UpdatedAt: now, LocationID: sqlNullInt64(id), UserID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	if err := qtx.DeleteLocation(r.Context(), dbpkg.DeleteLocationParams{ID: id, UserID: u.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type bulkLocationReq struct {
	Name      string `json:"name"`
	ParentID  *int64 `json:"parent_id"` // 0 or absent = root
	Dims      []int  `json:"dims"`
	Structure string `json:"structure"`
	Naming    string `json:"naming"`
	Prefix    string `json:"prefix"`
}

// handleBulkCreateLocations creates a root location plus generated
// sublocations in ONE transaction - all or nothing. Every validation runs
// before the first write, so an invalid request leaves nothing behind.
func (s *Server) handleBulkCreateLocations(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req bulkLocationReq
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 200 {
		writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
		return
	}
	if len(req.Dims) < 1 || len(req.Dims) > 3 {
		writeErr(w, http.StatusBadRequest, "validation_error", "dims must contain 1 to 3 entries")
		return
	}
	product := 1
	for _, d := range req.Dims {
		if d < 1 || d > 100 {
			writeErr(w, http.StatusBadRequest, "validation_error", "each dim must be between 1 and 100")
			return
		}
		product *= d
	}
	if product > 1000 {
		writeErr(w, http.StatusBadRequest, "validation_error", "product of dims must be at most 1000")
		return
	}
	if req.Structure != "flat" && req.Structure != "nested" {
		writeErr(w, http.StatusBadRequest, "validation_error", `structure must be "flat" or "nested"`)
		return
	}
	if req.Naming != "compact" && req.Naming != "verbose" {
		writeErr(w, http.StatusBadRequest, "validation_error", `naming must be "compact" or "verbose"`)
		return
	}
	prefix := strings.TrimSpace(req.Prefix)
	if len(prefix) > 50 {
		writeErr(w, http.StatusBadRequest, "validation_error", "prefix must be at most 50 chars")
		return
	}
	parentID := req.ParentID
	if parentID != nil && *parentID == 0 {
		parentID = nil // 0 = root, same as absent
	}
	parent, ok := s.checkParent(w, r, u, parentID)
	if !ok {
		return
	}
	// app-logic uniqueness for the new root (mirrors handleCreateLocation)
	if existing, err := s.q.GetLocationByName(r.Context(), dbpkg.GetLocationByNameParams{
		UserID: u.ID, Name: name, ParentID: parent,
	}); err == nil && existing.ID != 0 {
		writeErr(w, http.StatusConflict, "location_exists",
			"a location with this name already exists under this parent")
		return
	}

	nodes := generateBulkLocations(req.Dims, req.Structure, req.Naming, prefix)
	now := time.Now().UTC().Format(time.RFC3339) // one clock reading for root + children
	tx, err := s.raw.BeginTx(r.Context(), nil)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	defer tx.Rollback()
	qtx := dbpkg.New(tx)
	root, err := qtx.CreateLocation(r.Context(), dbpkg.CreateLocationParams{
		UserID: u.ID, Name: name, ParentID: parent, Notes: "",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	created := map[string]int64{"": root.ID}
	for _, n := range nodes {
		child, err := qtx.CreateLocation(r.Context(), dbpkg.CreateLocationParams{
			UserID: u.ID, Name: n.Name, ParentID: sqlNullInt64(created[n.Parent]),
			Notes: "", CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			s.internalErr(w, err)
			return
		}
		created[n.Path] = child.ID
	}
	if err := tx.Commit(); err != nil {
		s.internalErr(w, err)
		return
	}
	out := locationJSON(root)
	out["children_created"] = len(nodes)
	writeJSON(w, http.StatusCreated, out)
}

type copyLayoutReq struct {
	Name     string `json:"name"`
	ParentID *int64 `json:"parent_id"` // 0 or absent = root
}

// handleCopyLocationLayout duplicates a location's entire subtree (names
// + shape only - items and notes never ride along) under a NEW location
// in one transaction. The copy may not land inside the source's own
// subtree.
func (s *Server) handleCopyLocationLayout(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid location id")
		return
	}
	var req copyLayoutReq
	if !decodeJSON(w, r, &req) {
		return
	}
	src, err := s.q.GetLocation(r.Context(), dbpkg.GetLocationParams{ID: id, UserID: u.ID})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "location not found")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 200 {
		writeErr(w, http.StatusBadRequest, "validation_error", "name must be 1-200 chars")
		return
	}
	parentID := req.ParentID
	if parentID != nil && *parentID == 0 {
		parentID = nil // 0 = root, same as absent
	}
	parent, ok := s.checkParent(w, r, u, parentID)
	if !ok {
		return
	}
	if parent.Valid { // a copy cannot land in the source's own subtree
		desc, err := s.q.LocationDescendants(r.Context(), dbpkg.LocationDescendantsParams{
			ID: id, UserID: u.ID, UserID_2: u.ID,
		})
		if err != nil {
			s.internalErr(w, err)
			return
		}
		for _, d := range desc {
			if d == parent.Int64 {
				writeErr(w, http.StatusBadRequest, "validation_error",
					"cannot copy a location into its own subtree")
				return
			}
		}
	}

	// read the full tree once, then walk the source subtree in name order
	locs, err := s.q.ListLocations(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	sort.Slice(locs, func(i, j int) bool { return locs[i].Name < locs[j].Name })
	childrenOf := make(map[int64][]dbpkg.Location, len(locs))
	for _, l := range locs {
		if l.ParentID.Valid {
			childrenOf[l.ParentID.Int64] = append(childrenOf[l.ParentID.Int64], l)
		}
	}
	type copyNode struct {
		Name      string
		SrcID     int64
		SrcParent int64
	}
	plan := make([]copyNode, 0, len(childrenOf[src.ID]))
	var walk func(parent int64)
	walk = func(parent int64) { // pre-order: parents insert before children
		for _, l := range childrenOf[parent] {
			plan = append(plan, copyNode{Name: l.Name, SrcID: l.ID, SrcParent: parent})
			walk(l.ID)
		}
	}
	walk(src.ID)
	if len(plan)+1 > 1000 { // subtree incl. the new root
		writeErr(w, http.StatusBadRequest, "validation_error",
			"cannot copy a subtree with more than 1000 locations")
		return
	}
	// app-logic uniqueness for the new root (mirrors handleCreateLocation)
	if existing, err := s.q.GetLocationByName(r.Context(), dbpkg.GetLocationByNameParams{
		UserID: u.ID, Name: name, ParentID: parent,
	}); err == nil && existing.ID != 0 {
		writeErr(w, http.StatusConflict, "location_exists",
			"a location with this name already exists under this parent")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.raw.BeginTx(r.Context(), nil)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	defer tx.Rollback()
	qtx := dbpkg.New(tx)
	root, err := qtx.CreateLocation(r.Context(), dbpkg.CreateLocationParams{
		UserID: u.ID, Name: name, ParentID: parent, Notes: "",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	newID := map[int64]int64{src.ID: root.ID}
	for _, n := range plan {
		child, err := qtx.CreateLocation(r.Context(), dbpkg.CreateLocationParams{
			UserID: u.ID, Name: n.Name, ParentID: sqlNullInt64(newID[n.SrcParent]),
			Notes: "", CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			s.internalErr(w, err)
			return
		}
		newID[n.SrcID] = child.ID
	}
	if err := tx.Commit(); err != nil {
		s.internalErr(w, err)
		return
	}
	out := locationJSON(root)
	out["children_created"] = len(plan)
	writeJSON(w, http.StatusCreated, out)
}
