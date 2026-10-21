package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"stasher/internal/auth"
	dbpkg "stasher/internal/db"
)

type profileReq struct {
	DisplayName           *string `json:"display_name"`
	DefaultItemVisibility *string `json:"default_item_visibility"`
}

func (s *Server) handleProfileUpdate(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req profileReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DisplayName != nil {
		if len(*req.DisplayName) < 1 || len(*req.DisplayName) > 100 {
			writeErr(w, http.StatusBadRequest, "validation_error", "display name must be 1-100 chars")
			return
		}
		u.DisplayName = *req.DisplayName
	}
	if req.DefaultItemVisibility != nil {
		if *req.DefaultItemVisibility != "private" && *req.DefaultItemVisibility != "shared" {
			writeErr(w, http.StatusBadRequest, "validation_error", "visibility must be private or shared")
			return
		}
		u.DefaultItemVisibility = *req.DefaultItemVisibility
	}
	if err := s.q.UpdateUserProfile(r.Context(), dbpkg.UpdateUserProfileParams{
		DisplayName: u.DisplayName, DefaultItemVisibility: u.DefaultItemVisibility,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339), ID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, userJSONFrom(u))
}

type passwordReq struct {
	CurrentPassword     string `json:"current_password"`
	NewPassword         string `json:"new_password"`
	RevokeOtherSessions bool   `json:"revoke_other_sessions"`
}

func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req passwordReq
	if !decodeJSON(w, r, &req) {
		return
	}
	full, err := s.q.GetUserByID(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if !auth.VerifyPassword(full.PasswordHash, req.CurrentPassword) {
		writeErr(w, http.StatusBadRequest, "invalid_current_password", "current password is incorrect")
		return
	}
	if len(req.NewPassword) < 10 {
		writeErr(w, http.StatusBadRequest, "validation_error", "new password must be at least 10 chars")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	if err := s.q.UpdateUserPassword(r.Context(), dbpkg.UpdateUserPasswordParams{
		PasswordHash: hash, UpdatedAt: time.Now().UTC().Format(time.RFC3339), ID: u.ID,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	if req.RevokeOtherSessions {
		sid, _ := currentSessionID(r.Context())
		if err := s.q.DeleteOtherUserSessions(r.Context(), dbpkg.DeleteOtherUserSessionsParams{
			UserID: u.ID, ID: sid,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	sid, _ := currentSessionID(r.Context())
	rows, err := s.q.ListUserSessions(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	curSid := sid
	out := make([]map[string]any, 0, len(rows))
	for _, sess := range rows {
		out = append(out, map[string]any{
			"id": sess.ID, "user_agent": sess.UserAgent, "last_ip": sess.LastIp,
			"created_at": sess.CreatedAt, "last_seen_at": sess.LastSeenAt,
			"current": sess.ID == curSid,
		})
	}
	writeJSON(w, http.StatusOK, listEnvelope{Data: out, Total: int64(len(out))})
}

func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "invalid session id")
		return
	}
	if _, err := s.q.GetSessionByID(r.Context(), dbpkg.GetSessionByIDParams{ID: id, UserID: u.ID}); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	if err := s.q.DeleteSession(r.Context(), dbpkg.DeleteSessionParams{ID: id, UserID: u.ID}); err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type revokeAllReq struct {
	ExceptCurrent bool `json:"except_current"`
}

func (s *Server) handleRevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	var req revokeAllReq
	if !decodeJSON(w, r, &req) {
		return
	}
	var err error
	if req.ExceptCurrent {
		sid, _ := currentSessionID(r.Context())
		err = s.q.DeleteOtherUserSessions(r.Context(), dbpkg.DeleteOtherUserSessionsParams{UserID: u.ID, ID: sid})
	} else {
		err = s.q.DeleteAllUserSessions(r.Context(), u.ID)
		http.SetCookie(w, auth.ClearSessionCookie())
	}
	if err != nil {
		s.internalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
