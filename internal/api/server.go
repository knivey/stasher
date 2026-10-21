package api

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"

	"stasher/internal/config"
	dbpkg "stasher/internal/db"
	"stasher/internal/files"
)

// Server holds application dependencies for all handlers.
type Server struct {
	cfg         *config.Config
	q           *dbpkg.Queries
	st          *files.Storage
	raw         *sql.DB // for transactions and test queries; same pool as q
	spaFallback http.HandlerFunc
}

// New wires the server. This signature is final for the whole plan.
func New(cfg *config.Config, raw *sql.DB, st *files.Storage) *Server {
	return &Server{cfg: cfg, q: dbpkg.New(raw), st: st, raw: raw}
}

// SetSPAFallback installs the handler used for unmatched non-API routes
// (main passes the embedded SPA index handler in Task 18).
func (s *Server) SetSPAFallback(h http.HandlerFunc) { s.spaFallback = h }

// Router builds the complete application router: /healthz, /api/v1/*, /files/*.
func (s *Server) Router() chi.Router {
	r := chi.NewRouter()
	r.Use(s.logging)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	r.Route("/api/v1", func(r chi.Router) {
		// Public auth endpoints (added in Task 7): register, login.
		r.Post("/auth/register", s.handleRegister)
		r.Post("/auth/login", s.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/auth/me", s.handleMe)
			r.Post("/auth/logout", s.handleLogout)
			r.Patch("/auth/me", s.handleProfileUpdate)
			r.Post("/auth/password", s.handlePasswordChange)
			r.Get("/auth/sessions", s.handleListSessions)
			r.Delete("/auth/sessions/{id}", s.handleRevokeSession)
			r.Post("/auth/sessions/revoke-all", s.handleRevokeAllSessions)
		})
	})

	// /files/* route added in Task 15.

	if s.spaFallback != nil {
		r.NotFound(s.spaFallback)
	}
	return r
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r.Context())
	writeJSON(w, http.StatusOK, userJSONFrom(u))
}

func userJSONFrom(u dbpkg.User) map[string]any {
	return map[string]any{
		"id": u.ID, "email": u.Email, "display_name": u.DisplayName,
		"default_item_visibility": u.DefaultItemVisibility,
		"is_admin":                u.IsAdmin != 0,
	}
}
