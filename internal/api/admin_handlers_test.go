package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"stasher/internal/config"
)

func loginUser(t *testing.T, ts *httptest.Server, email, password string) *http.Cookie {
	t.Helper()
	resp := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": email, "password": password}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s = %d", email, resp.StatusCode)
	}
	return sessionCookieFrom(resp)
}

func adminSetup(t *testing.T) (admin, pleb *http.Cookie, ts *httptest.Server, s *Server) {
	s, ts = newTestServer(t, func(c *config.Config) { c.Registration = "invite" })
	// open-mode first user would also be admin; keep invite mode realistic:
	resp := doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "admin@test.dev", "password": "longpassword1", "display_name": "Admin"}, nil)
	if resp.StatusCode != 201 { // bootstrap admin, no invite needed
		t.Fatalf("bootstrap register = %d", resp.StatusCode)
	}
	admin = sessionCookieFrom(resp)

	// admin creates an invite; pleb uses it
	inv := doJSON(t, ts, "POST", "/api/v1/admin/invites", map[string]any{}, admin)
	var invBody struct {
		Code string `json:"code"`
	}
	decodeBody(t, inv, &invBody)
	if len(invBody.Code) != 10 {
		t.Fatalf("invite code = %q", invBody.Code)
	}
	pleb = sessionCookieFrom(doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "pleb@test.dev", "password": "longpassword1", "display_name": "Pleb",
			"invite_code": invBody.Code}, nil))
	return admin, pleb, ts, s
}

func TestAdminRequiresAdmin(t *testing.T) {
	_, pleb, ts, _ := adminSetup(t)
	for _, tc := range []struct{ m, p string }{
		{"GET", "/api/v1/admin/users"},
		{"POST", "/api/v1/admin/invites"},
	} {
		if resp := doJSON(t, ts, tc.m, tc.p, map[string]any{}, pleb); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s %s as pleb = %d, want 403", tc.m, tc.p, resp.StatusCode)
		}
	}
}

func TestAdminDisableUserRevokesSessions(t *testing.T) {
	admin, pleb, ts, _ := adminSetup(t)
	list := doJSON(t, ts, "GET", "/api/v1/admin/users", nil, admin)
	var users struct {
		Data []struct {
			ID    int64  `json:"id"`
			Email string `json:"email"`
		} `json:"data"`
	}
	decodeBody(t, list, &users)
	var plebID int64
	for _, u := range users.Data {
		if u.Email == "pleb@test.dev" {
			plebID = u.ID
		}
	}
	if plebID == 0 {
		t.Fatal("pleb not in user list")
	}
	resp := doJSON(t, ts, "PATCH", "/api/v1/admin/users/"+fmtInt64(plebID),
		map[string]any{"disabled": true}, admin)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable = %d", resp.StatusCode)
	}
	if r := doJSON(t, ts, "GET", "/api/v1/auth/me", nil, pleb); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("disabled user session = %d, want 401", r.StatusCode)
	}
}

func TestAdminCannotDisableSelf(t *testing.T) {
	admin, _, ts, _ := adminSetup(t)
	resp := doJSON(t, ts, "PATCH", "/api/v1/admin/users/1", map[string]any{"disabled": true}, admin)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("self-disable = %d, want 400", resp.StatusCode)
	}
}

func TestInviteLifecycle(t *testing.T) {
	admin, _, ts, _ := adminSetup(t)
	inv := doJSON(t, ts, "POST", "/api/v1/admin/invites", map[string]any{}, admin)
	var created struct {
		ID   int64  `json:"id"`
		Code string `json:"code"`
	}
	decodeBody(t, inv, &created)

	// list shows it unused
	var list struct {
		Data []struct {
			Code        string `json:"code"`
			UsedByEmail string `json:"used_by_email"`
		} `json:"data"`
	}
	decodeBody(t, doJSON(t, ts, "GET", "/api/v1/admin/invites", nil, admin), &list)
	found := false
	for _, i := range list.Data {
		if i.Code == created.Code {
			found = true
			if i.UsedByEmail != "" {
				t.Fatalf("new invite already used by %q", i.UsedByEmail)
			}
		}
	}
	if !found {
		t.Fatal("created invite not listed")
	}
	// delete unused invite
	if r := doJSON(t, ts, "DELETE", "/api/v1/admin/invites/"+fmtInt64(created.ID), nil, admin); r.StatusCode != 204 {
		t.Fatalf("delete invite = %d", r.StatusCode)
	}
	// deleted code no longer registers
	r := doJSON(t, ts, "POST", "/api/v1/auth/register",
		map[string]any{"email": "x@test.dev", "password": "longpassword1", "display_name": "X",
			"invite_code": created.Code}, nil)
	if r.StatusCode != 400 {
		t.Fatalf("register with deleted invite = %d, want 400", r.StatusCode)
	}
}
