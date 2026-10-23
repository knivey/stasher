package api

import (
	"database/sql"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"

	"stasher/internal/auth"
	"stasher/internal/config"
	dbpkg "stasher/internal/db"
	"stasher/internal/fetcher"
	"stasher/internal/files"
)

// Server holds application dependencies for all handlers.
type Server struct {
	cfg         *config.Config
	q           *dbpkg.Queries
	st          *files.Storage
	raw         *sql.DB // for transactions and test queries; same pool as q
	spaFallback http.HandlerFunc

	loginIP    *auth.Limiter // 10 attempts/min per IP
	loginAcct  *auth.Limiter // 5 attempts/min per account
	registerIP *auth.Limiter // 10 registrations/min per IP
	searchRate *auth.Limiter // 1 document search / 3s per user

	fetchDirect  *fetcher.DirectEngine // plain HTTP downloads; also verifies search candidates
	fetchArchive fetcher.Engine        // chromium page->PDF (nil = archive disabled)
	searcher     fetcher.Searcher      // Brave web search (nil = search disabled)

	jobs     map[string]*jobState // in-memory fetch jobs (never persisted)
	jobsMu   sync.Mutex
	fetchSem chan struct{} // cap 1: at most one running fetch server-wide
}

// New wires the server. fetchDirect/fetchArchive may be nil in principle
// (the fetch endpoint then answers 503) but main always wires both;
// searcher is nil unless brave_api_key is configured (search → 400
// search_disabled).
func New(cfg *config.Config, raw *sql.DB, st *files.Storage, fetchDirect *fetcher.DirectEngine, fetchArchive fetcher.Engine, searcher fetcher.Searcher) *Server {
	return &Server{
		cfg: cfg, q: dbpkg.New(raw), st: st, raw: raw,
		loginIP:      auth.NewLimiter(10, 10),
		loginAcct:    auth.NewLimiter(5, 5),
		registerIP:   auth.NewLimiter(10, 10),
		searchRate:   auth.NewLimiter(20, 1), // 1 search / 3s per user, no burst
		fetchDirect:  fetchDirect,
		fetchArchive: fetchArchive,
		searcher:     searcher,
		jobs:         map[string]*jobState{},
		fetchSem:     make(chan struct{}, 1),
	}
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

			r.Get("/locations", s.handleListLocations)
			r.Post("/locations", s.handleCreateLocation)
			r.Post("/locations/bulk", s.handleBulkCreateLocations)
			r.Get("/locations/{id}", s.handleGetLocation)
			r.Patch("/locations/{id}", s.handleUpdateLocation)
			r.Delete("/locations/{id}", s.handleDeleteLocation)
			r.Post("/locations/{id}/copy-layout", s.handleCopyLocationLayout)
			r.Post("/locations/{id}/photo", s.handleUploadLocationPhoto)
			r.Delete("/locations/{id}/photo", s.handleDeleteLocationPhoto)

			r.Get("/tags", s.handleListTags)
			r.Post("/tags", s.handleCreateTag)
			r.Patch("/tags/{id}", s.handleUpdateTag)
			r.Delete("/tags/{id}", s.handleDeleteTag)

			r.Post("/items", s.handleCreateItem)
			r.Post("/items/bulk-move", s.handleBulkMoveItems)
			r.Get("/items", s.handleListItems)
			r.Get("/items/{id}", s.handleGetItem)
			r.Patch("/items/{id}", s.handleUpdateItem)
			r.Delete("/items/{id}", s.handleDeleteItem)
			r.Post("/items/{id}/fetch", s.handleItemFetch)
			r.Post("/items/{id}/search-documents", s.handleItemSearchDocuments)
			r.Get("/jobs/{id}", s.handleGetJob)
			r.Post("/items/{id}/photos", s.handleUploadPhoto)
			r.Patch("/items/{id}/photos/{photoId}", s.handlePatchPhoto)
			r.Delete("/items/{id}/photos/{photoId}", s.handleDeletePhoto)
			r.Post("/items/{id}/photos/{photoId}/main", s.handleMakePhotoMain)
			r.Post("/items/{id}/documents", s.handleUploadDocument)
			r.Delete("/items/{id}/documents/{docId}", s.handleDeleteDocument)
			r.Post("/items/{id}/copy", s.handleCopyItem)
			r.Post("/items/{id}/usage", s.handleAddItemUsage)
			r.Get("/items/{id}/usage", s.handleListItemUsage)
			r.Post("/items/{id}/notes", s.handleAddItemNote)
			r.Delete("/items/{id}/notes/{noteId}", s.handleDeleteItemNote)

			r.Get("/shared", s.handleListShared)

			r.Get("/trash", s.handleListTrash)
			r.Delete("/trash", s.handleEmptyTrash)
			r.Delete("/trash/{id}", s.handlePurgeTrashEntry)
			r.Post("/trash/{id}/restore", s.handleRestoreTrashEntry)

			r.Get("/projects", s.handleListProjects)
			r.Post("/projects", s.handleCreateProject)
			r.Get("/projects/{id}", s.handleGetProject)
			r.Patch("/projects/{id}", s.handleUpdateProject)
			r.Delete("/projects/{id}", s.handleDeleteProject)
			r.Post("/projects/{id}/items", s.handleAddProjectItem)
			r.Patch("/projects/{id}/items/{itemId}", s.handlePatchProjectItem)
			r.Delete("/projects/{id}/items/{itemId}", s.handleDeleteProjectItem)

			r.Route("/admin", func(r chi.Router) {
				r.Use(s.requireAdmin)
				r.Get("/users", s.handleAdminListUsers)
				r.Patch("/users/{id}", s.handleAdminPatchUser)
				r.Get("/invites", s.handleAdminListInvites)
				r.Post("/invites", s.handleAdminCreateInvite)
				r.Delete("/invites/{id}", s.handleAdminDeleteInvite)
			})
		})
	})

	// Gated file serving (Task 15): root-level, still authed.
	r.With(s.requireAuth).Get("/files/*", s.handleFileGet)

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
