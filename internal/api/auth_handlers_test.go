package api

import (
	"context"
	"net/http"
	"testing"

	"stasher/internal/config"
	"stasher/internal/db"
)

func TestRegisterOpenMode(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "First@Test.Dev", "password": "longpassword1", "display_name": "First"}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register = %d, want 201", resp.StatusCode)
	}
	var body struct {
		Email   string `json:"email"`
		IsAdmin bool   `json:"is_admin"`
	}
	decodeBody(t, resp, &body)
	if body.Email != "first@test.dev" {
		t.Errorf("email not lowercased: %q", body.Email)
	}
	if !body.IsAdmin { // first user is admin (bootstrap rule)
		t.Errorf("first user should be admin")
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	_, ts := newTestServer(t, nil)
	registerUser(t, ts, "dup@test.dev", "longpassword1")
	resp := doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "dup@test.dev", "password": "longpassword1", "display_name": "Dup"}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup register = %d, want 409", resp.StatusCode)
	}
}

func TestRegisterInviteMode(t *testing.T) {
	s, ts := newTestServer(t, func(c *config.Config) { c.Registration = "invite" })
	ctx, now := context.Background(), timeNow(t)

	admin, err := s.q.CreateUser(ctx, createUserParams("admin@test.dev", "Admin", now))
	if err != nil {
		t.Fatal(err)
	}
	invite, err := s.q.CreateInvite(ctx, db.CreateInviteParams{Code: "TESTCODE1", CreatedBy: admin.ID, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	_ = invite

	// no code -> 400
	resp := doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "a@test.dev", "password": "longpassword1", "display_name": "A"}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("register without invite = %d, want 400", resp.StatusCode)
	}
	// bad code -> 400
	resp = doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "a@test.dev", "password": "longpassword1", "display_name": "A", "invite_code": "WRONG"}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("register wrong invite = %d, want 400", resp.StatusCode)
	}
	// good code -> 201, non-admin
	resp = doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "a@test.dev", "password": "longpassword1", "display_name": "A", "invite_code": "TESTCODE1"}, nil)
	var body struct {
		IsAdmin bool `json:"is_admin"`
	}
	decodeBody(t, resp, &body)
	if resp.StatusCode != http.StatusCreated || body.IsAdmin {
		t.Fatalf("register with invite = %d admin=%v, want 201 admin=false", resp.StatusCode, body.IsAdmin)
	}
	// code reuse -> 400
	resp = doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "b@test.dev", "password": "longpassword1", "display_name": "B", "invite_code": "TESTCODE1"}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("reuse invite = %d, want 400", resp.StatusCode)
	}

	// BUT: bootstrap rule — on an empty DB the first user needs no code. Covered
	// by TestRegisterOpenMode style: fresh server, invite mode, no users.
	_, ts2 := newTestServer(t, func(c *config.Config) { c.Registration = "invite" })
	resp = doJSON(t, ts2, "POST", "/api/v1/auth/register",
		map[string]any{"email": "first@test.dev", "password": "longpassword1", "display_name": "F"}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap register in invite mode = %d, want 201", resp.StatusCode)
	}
}

func TestRegisterValidation(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cases := []map[string]any{
		{"email": "notanemail", "password": "longpassword1", "display_name": "X"},
		{"email": "x@y.dev", "password": "short", "display_name": "X"},
		{"email": "x@y.dev", "password": "longpassword1", "display_name": ""},
	}
	for _, c := range cases {
		if resp := doJSON(t, ts, "POST", "/api/v1/auth/register", c, nil); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("register %v = %d, want 400", c, resp.StatusCode)
		}
	}
}

func TestLoginLogout(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "login@test.dev", "longpassword1")

	// login with right password
	resp := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "login@test.dev", "password": "longpassword1"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	bad := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "login@test.dev", "password": "wrongpassword"}, nil)
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", bad.StatusCode)
	}

	// logout revokes server-side; replayed cookie must fail (Review Focus #3)
	logout := doJSON(t, ts, "POST", "/api/v1/auth/logout", nil, cookie)
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", logout.StatusCode)
	}
	replay := doJSON(t, ts, "GET", "/api/v1/auth/me", nil, cookie)
	if replay.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed cookie after logout = %d, want 401", replay.StatusCode)
	}
}

func TestLoginRateLimited(t *testing.T) {
	_, ts := newTestServer(t, nil)
	registerUser(t, ts, "rl@test.dev", "longpassword1")
	// account limiter is 5/min: 5 attempts (any result), 6th is 429
	for i := 0; i < 5; i++ {
		doJSON(t, ts, "POST", "/api/v1/auth/login",
			map[string]any{"email": "rl@test.dev", "password": "wrongpassword"}, nil)
	}
	resp := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "rl@test.dev", "password": "wrongpassword"}, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6th attempt = %d, want 429", resp.StatusCode)
	}
}

func TestLoginDisabledUser(t *testing.T) {
	s, ts := newTestServer(t, nil)
	registerUser(t, ts, "dis@test.dev", "longpassword1")
	s.q.SetUserDisabled(context.Background(), db.SetUserDisabledParams{Disabled: 1, UpdatedAt: timeNow(t), ID: s.lastUserID(t)})
	resp := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "dis@test.dev", "password": "longpassword1"}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("disabled login = %d, want 403", resp.StatusCode)
	}
}
