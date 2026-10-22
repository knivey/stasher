package api

import (
	"net/http"
	"strings"
	"time"

	"stasher/internal/auth"
	dbpkg "stasher/internal/db"
)

type registerReq struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	InviteCode  string `json:"invite_code"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if !decodeJSON(w, r, &req) {
		return
	}
	// Rate-limit before any DB work or argon2 hashing: open registration is
	// unauthenticated, so this is the only guard against grinding and spam.
	if !s.registerIP.Allow("ip:" + clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many attempts; try again later")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if !strings.Contains(req.Email, "@") || len(req.Email) > 254 ||
		len(req.Password) < 10 || req.DisplayName == "" || len(req.DisplayName) > 100 {
		writeErr(w, http.StatusBadRequest, "validation_error",
			"email must be valid, password at least 10 chars, display name 1-100 chars")
		return
	}

	count, err := s.q.CountUsers(r.Context())
	if err != nil {
		s.internalErr(w, err)
		return
	}
	bootstrap := count == 0 // first user: admin, no invite needed
	isAdmin := int64(0)
	if bootstrap {
		isAdmin = 1
	}

	if !bootstrap && s.cfg.Registration == "invite" {
		if req.InviteCode == "" {
			writeErr(w, http.StatusBadRequest, "invite_required", "an invite code is required")
			return
		}
		inv, err := s.q.GetInvite(r.Context(), req.InviteCode)
		if err != nil || inv.UsedBy.Valid {
			writeErr(w, http.StatusBadRequest, "invalid_invite", "invite code is invalid or already used")
			return
		}
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	u, err := s.q.CreateUser(r.Context(), dbpkg.CreateUserParams{
		Email: req.Email, PasswordHash: hash, DisplayName: req.DisplayName,
		DefaultItemVisibility: "private", IsAdmin: isAdmin, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: users.email") {
			writeErr(w, http.StatusConflict, "email_taken", "an account with this email already exists")
			return
		}
		s.internalErr(w, err)
		return
	}
	if !bootstrap && s.cfg.Registration == "invite" {
		if err := s.q.MarkInviteUsed(r.Context(), dbpkg.MarkInviteUsedParams{
			UsedBy: sqlNullInt64(u.ID), UsedAt: sqlNullString(now), Code: req.InviteCode,
		}); err != nil {
			s.internalErr(w, err)
			return
		}
	}
	s.startSession(w, r, u)
	writeJSON(w, http.StatusCreated, userJSONFrom(u))
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// dummyPasswordHash is verified when the email lookup fails so that
// unknown-email and wrong-password attempts burn the same argon2 cost —
// response timing must not reveal whether an account exists. Computed once
// at package init; the password it encodes is never used anywhere.
var dummyPasswordHash = func() string {
	h, err := auth.HashPassword("stasher-timing-equalization-dummy")
	if err != nil {
		panic(err) // argon2 with fixed in-house params cannot fail
	}
	return h
}()

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !s.loginIP.Allow("ip:" + clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many attempts; try again later")
		return
	}
	if req.Email != "" && !s.loginAcct.Allow("acct:"+req.Email) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many attempts; try again later")
		return
	}
	u, err := s.q.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		// Unknown email: still run a full argon2 verify against the dummy
		// hash to equalize timing with the wrong-password path below.
		auth.VerifyPassword(dummyPasswordHash, req.Password)
		writeErr(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if !auth.VerifyPassword(u.PasswordHash, req.Password) {
		// Same error for unknown email and bad password (no account probing).
		writeErr(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if u.Disabled != 0 {
		writeErr(w, http.StatusForbidden, "account_disabled", "this account is disabled")
		return
	}
	s.startSession(w, r, u)
	writeJSON(w, http.StatusOK, userJSONFrom(u))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sid, ok := currentSessionID(r.Context()); ok {
		if u, ok := currentUser(r.Context()); ok {
			if err := s.q.DeleteSession(r.Context(), dbpkg.DeleteSessionParams{ID: sid, UserID: u.ID}); err != nil {
				s.internalErr(w, err)
				return
			}
		}
	}
	http.SetCookie(w, auth.ClearSessionCookie())
	w.WriteHeader(http.StatusNoContent)
}

// startSession creates a session row and sets the cookie.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u dbpkg.User) {
	raw, hash := auth.NewSessionToken()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.q.CreateSession(r.Context(), dbpkg.CreateSessionParams{
		TokenHash: hash, UserID: u.ID,
		UserAgent: r.UserAgent(), LastIp: clientIP(r),
		CreatedAt: now, LastSeenAt: now,
	}); err != nil {
		s.internalErr(w, err)
		return
	}
	http.SetCookie(w, auth.SessionCookie(raw))
}

func clientIP(r *http.Request) string {
	// Caddy (our single trusted proxy hop) appends the real client IP to
	// X-Forwarded-For, so the LAST entry is the client; earlier entries are
	// client-supplied and spoofable.
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		parts := strings.Split(f, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	return r.RemoteAddr
}

func (s *Server) internalErr(w http.ResponseWriter, err error) {
	slogError("internal error", err)
	writeErr(w, http.StatusInternalServerError, "internal", "internal server error")
}
