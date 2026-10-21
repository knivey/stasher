package api

import (
	"net/http"
	"testing"
)

func TestProfileUpdate(t *testing.T) {
	_, ts := newTestServer(t, nil)
	cookie := registerUser(t, ts, "prof@test.dev", "longpassword1")
	resp := doJSON(t, ts, "PATCH", "/api/v1/auth/me",
		map[string]any{"display_name": "NewName", "default_item_visibility": "shared"}, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch me = %d", resp.StatusCode)
	}
	var me struct {
		DisplayName           string `json:"display_name"`
		DefaultItemVisibility string `json:"default_item_visibility"`
	}
	decodeBody(t, resp, &me)
	if me.DisplayName != "NewName" || me.DefaultItemVisibility != "shared" {
		t.Fatalf("me = %+v", me)
	}
}

func TestPasswordChangeRevokesOthers(t *testing.T) {
	_, ts := newTestServer(t, nil)
	registerUser(t, ts, "pw@test.dev", "longpassword1")
	other := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "pw@test.dev", "password": "longpassword1"}, nil)
	otherCookie := sessionCookieFrom(other)

	// current session cookie
	loginResp := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "pw@test.dev", "password": "longpassword1"}, nil)
	current := sessionCookieFrom(loginResp)

	resp := doJSON(t, ts, "POST", "/api/v1/auth/password",
		map[string]any{"current_password": "longpassword1", "new_password": "newlongpassword", "revoke_other_sessions": true}, current)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("password change = %d", resp.StatusCode)
	}
	// other session dead, current alive
	if r := doJSON(t, ts, "GET", "/api/v1/auth/me", nil, otherCookie); r.StatusCode != 401 {
		t.Fatalf("other session = %d, want 401", r.StatusCode)
	}
	if r := doJSON(t, ts, "GET", "/api/v1/auth/me", nil, current); r.StatusCode != 200 {
		t.Fatalf("current session = %d, want 200", r.StatusCode)
	}
	// old password no longer works
	if r := doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "pw@test.dev", "password": "longpassword1"}, nil); r.StatusCode != 401 {
		t.Fatalf("old password login = %d, want 401", r.StatusCode)
	}
	// wrong current password rejected
	if r := doJSON(t, ts, "POST", "/api/v1/auth/password",
		map[string]any{"current_password": "nope", "new_password": "anotherlongpass"}, current); r.StatusCode != 400 {
		t.Fatalf("wrong current pw = %d, want 400", r.StatusCode)
	}
}

func TestDeviceListAndRevoke(t *testing.T) {
	_, ts := newTestServer(t, nil)
	phone := registerUser(t, ts, "dev@test.dev", "longpassword1")
	desktop := sessionCookieFrom(doJSON(t, ts, "POST", "/api/v1/auth/login",
		map[string]any{"email": "dev@test.dev", "password": "longpassword1"}, nil))

	// list from phone: two sessions, phone's is current
	resp := doJSON(t, ts, "GET", "/api/v1/auth/sessions", nil, phone)
	var list struct {
		Data []struct {
			ID      int64 `json:"id"`
			Current bool  `json:"current"`
		} `json:"data"`
	}
	decodeBody(t, resp, &list)
	if len(list.Data) != 2 {
		t.Fatalf("sessions = %d, want 2", len(list.Data))
	}
	currents := 0
	for _, s := range list.Data {
		if s.Current {
			currents++
		}
	}
	if currents != 1 {
		t.Fatalf("current flags = %d, want 1", currents)
	}

	// revoke desktop (its id is the non-current one)
	var desktopID int64
	for _, s := range list.Data {
		if !s.Current {
			desktopID = s.ID
		}
	}
	if r := doJSON(t, ts, "DELETE", "/api/v1/auth/sessions/"+fmtInt64(desktopID), nil, phone); r.StatusCode != 204 {
		t.Fatalf("revoke = %d", r.StatusCode)
	}
	if r := doJSON(t, ts, "GET", "/api/v1/auth/me", nil, desktop); r.StatusCode != 401 {
		t.Fatalf("revoked session = %d, want 401", r.StatusCode)
	}

	// revoke-all except current kills the rest (none left) and keeps phone
	if r := doJSON(t, ts, "POST", "/api/v1/auth/sessions/revoke-all",
		map[string]any{"except_current": true}, phone); r.StatusCode != 204 {
		t.Fatalf("revoke-all = %d", r.StatusCode)
	}
	if r := doJSON(t, ts, "GET", "/api/v1/auth/me", nil, phone); r.StatusCode != 200 {
		t.Fatalf("phone after revoke-all = %d, want 200", r.StatusCode)
	}
}
