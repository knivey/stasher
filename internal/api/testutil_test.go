package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"stasher/internal/auth"
	"stasher/internal/config"
	"stasher/internal/db"
	"stasher/internal/fetcher"
	"stasher/internal/files"
)

// TestMain silences chi's request logging so test output stays clean.
// (chi v5 has no DefaultLogWriter; DefaultLogger is the equivalent knob.)
func TestMain(m *testing.M) {
	middleware.DefaultLogger = func(next http.Handler) http.Handler { return next }
	os.Exit(m.Run())
}

func timeNow(t *testing.T) string {
	t.Helper()
	return time.Now().UTC().Format(time.RFC3339)
}

// Queries exposes the query layer to tests (same package, but call sites
// read like the public surface instead of poking unexported fields).
func (s *Server) Queries() *db.Queries { return s.q }

// Storage exposes the file storage to tests.
func (s *Server) Storage() *files.Storage { return s.st }

// lastUserID returns the highest user id (test convenience).
func (s *Server) lastUserID(t *testing.T) int64 {
	t.Helper()
	var id int64
	if err := s.raw.QueryRow("SELECT MAX(id) FROM users").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// stubChromePath resolves the fetcher package's stub-chrome fixture from
// this file's location (tests may run from any working directory).
func stubChromePath() string {
	_, thisFile, _, _ := runtime.Caller(0) // .../internal/api/testutil_test.go
	return filepath.Join(filepath.Dir(thisFile), "..", "fetcher", "testdata", "stub-chrome.sh")
}

// newTestServer returns a Server backed by a fresh temp SQLite DB and a test
// HTTP server running the real router. mutate (optional) adjusts config before
// construction (e.g. switch Registration to "invite"). The fetch engines use
// the loopback policy + the stub chrome so tests can fetch real httptest URLs.
func newTestServer(t *testing.T, mutate func(*config.Config)) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatal(err)
	}
	st, err := files.New(filepath.Join(dir, "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		ListenAddr: "", BaseURL: tsBaseURL, DataDir: dir,
		Registration: "open", LogLevel: "warn",
	}
	if mutate != nil {
		mutate(cfg)
	}
	s := New(cfg, sqlDB, st,
		fetcher.NewDirectEngine(fetcher.LoopbackPolicy()),
		fetcher.NewArchiveEngine(fetcher.LoopbackPolicy(), stubChromePath()),
		nil) // searcher: tests install a fake on s.searcher when needed
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)
	return s, ts
}

const tsBaseURL = "http://test.invalid"

// doJSON performs a request with an optional JSON body and optional cookie.
func doJSON(t *testing.T, ts *httptest.Server, method, path string, body any, cookie *http.Cookie) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// registerUser creates a user via POST /api/v1/auth/register and returns the cookie.
func registerUser(t *testing.T, ts *httptest.Server, email, password string) *http.Cookie {
	t.Helper()
	resp := doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": email, "password": password, "display_name": "Test"}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register = %d", resp.StatusCode)
	}
	return sessionCookieFrom(resp)
}

func fmtInt64(i int64) string { return strconv.FormatInt(i, 10) }

// jsonString returns s as a JSON-encoded string literal, for asserting raw
// decoded fields (absent key -> "" vs explicit null -> "null" vs value).
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func sessionCookieFrom(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	return nil
}

func decodeBody(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func createUserParams(email, name, now string) db.CreateUserParams {
	return db.CreateUserParams{
		Email: email, PasswordHash: "x", DisplayName: name,
		DefaultItemVisibility: "private", CreatedAt: now, UpdatedAt: now,
	}
}

func sessionParams(hash string, userID int64, now string) db.CreateSessionParams {
	return db.CreateSessionParams{
		TokenHash: hash, UserID: userID, UserAgent: "test", LastIp: "127.0.0.1",
		CreatedAt: now, LastSeenAt: now,
	}
}

// mustExec runs a raw SQL statement (test convenience for tables without APIs yet).
func mustExec(t *testing.T, s *Server, query string, args ...any) {
	t.Helper()
	if _, err := s.raw.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// mustQueryRow runs a raw SQL query returning one row.
func mustQueryRow(t *testing.T, s *Server, query string) *sql.Row {
	t.Helper()
	return s.raw.QueryRow(query)
}
