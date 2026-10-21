package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"stasher/internal/config"
	"stasher/internal/db"
	"stasher/internal/files"
)

// newTestServer returns a Server backed by a fresh temp SQLite DB and a test
// HTTP server running the real router. mutate (optional) adjusts config before
// construction (e.g. switch Registration to "invite").
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
	s := New(cfg, sqlDB, st)
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

func sessionCookieFrom(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == "stasher_session" {
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
