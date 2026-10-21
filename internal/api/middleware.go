package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"stasher/internal/auth"
	dbpkg "stasher/internal/db"
)

type ctxKey int

const (
	userKey ctxKey = iota + 1
	sessionIDKey
)

func currentUser(ctx context.Context) (dbpkg.User, bool) {
	u, ok := ctx.Value(userKey).(dbpkg.User)
	return u, ok
}

func currentSessionID(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(sessionIDKey).(int64)
	return id, ok
}

// requireAuth loads the session cookie, validates it server-side, and stores
// the user + session id in the request context.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(auth.CookieName)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		row, err := s.q.GetSessionWithUser(r.Context(), auth.HashToken(c.Value))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid session")
			return
		}
		if row.Disabled != 0 {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "account disabled")
			return
		}
		if err := s.q.TouchSession(r.Context(), dbpkg.TouchSessionParams{
			LastSeenAt: time.Now().UTC().Format(time.RFC3339), ID: row.SessionID,
		}); err != nil {
			slog.Warn("touch session failed", "err", err)
		}
		u := dbpkg.User{
			ID: row.UserID, Email: row.Email, DisplayName: row.DisplayName,
			DefaultItemVisibility: row.DefaultItemVisibility,
			IsAdmin:               row.IsAdmin, Disabled: row.Disabled,
		}
		ctx := context.WithValue(r.Context(), userKey, u)
		ctx = context.WithValue(ctx, sessionIDKey, row.SessionID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAdmin requires an authenticated admin user.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := currentUser(r.Context())
		if !ok || u.IsAdmin == 0 {
			writeErr(w, http.StatusForbidden, "forbidden", "admin access required")
			return
		}
		next.ServeHTTP(w, r.WithContext(r.Context()))
	})
}

func (s *Server) logging(next http.Handler) http.Handler {
	return middleware.Logger(next)
}
