package api

import (
	"crypto/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "stasher/internal/db"
)

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.q.ListUsers(r.Context())
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{
			"id": u.ID, "email": u.Email, "display_name": u.DisplayName,
			"default_item_visibility": u.DefaultItemVisibility,
			"is_admin":                u.IsAdmin, "disabled": u.Disabled,
			"created_at": u.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: int64(len(out))})
}

type adminUserPatch struct {
	Disabled *bool `json:"disabled"`
	IsAdmin  *bool `json:"is_admin"`
}

func (s *Server) handleAdminPatchUser(w http.ResponseWriter, r *http.Request) {
	me, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid user id")
		return
	}
	var req adminUserPatch
	if !decodeJSON(w, r, &req) {
		return
	}
	if id == me.ID && req.Disabled != nil && *req.Disabled {
		writeErr(w, http.StatusBadRequest, "cannot_disable_self", "you cannot disable your own account")
		return
	}
	target, err := s.q.GetUserByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if req.Disabled != nil {
		if err := s.q.SetUserDisabled(r.Context(), dbpkg.SetUserDisabledParams{
			Disabled: b2i(*req.Disabled), UpdatedAt: now, ID: id,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
		if *req.Disabled {
			if err := s.q.DeleteAllUserSessions(r.Context(), id); err != nil {
				s.internalErr(w, err)
				return
			}
		}
	}
	if req.IsAdmin != nil {
		if err := s.q.SetUserAdmin(r.Context(), dbpkg.SetUserAdminParams{
			IsAdmin: b2i(*req.IsAdmin), UpdatedAt: now, ID: id,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
	}
	target, _ = s.q.GetUserByID(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": target.ID, "email": target.Email, "display_name": target.DisplayName,
		"is_admin": target.IsAdmin, "disabled": target.Disabled,
	})
}

// b2i converts a bool to the SQLite 0/1 integer convention.
func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func newInviteCode() string {
	const alphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ" // no I L O U 0 1
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func (s *Server) handleAdminCreateInvite(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	inv, err := s.q.CreateInvite(r.Context(), dbpkg.CreateInviteParams{
		Code: newInviteCode(), CreatedBy: u.ID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": inv.ID, "code": inv.Code, "created_at": inv.CreatedAt})
}

func (s *Server) handleAdminListInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := s.q.ListInvites(r.Context())
	if err != nil {
		s.internalErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, i := range rows {
		usedBy := ""
		if i.UsedBy.Valid {
			usedBy = i.UsedByEmail.String
		}
		var usedAt any
		if i.UsedAt.Valid {
			usedAt = i.UsedAt.String
		}
		out = append(out, map[string]any{
			"id": i.ID, "code": i.Code, "created_at": i.CreatedAt,
			"used_by_email": usedBy, "used_at": usedAt,
		})
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: int64(len(out))})
}

func (s *Server) handleAdminDeleteInvite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid invite id")
		return
	}
	if err := s.q.DeleteInvite(r.Context(), id); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
