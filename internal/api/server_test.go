package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"stasher/internal/auth"
	"stasher/internal/db"
)

func TestHealthz(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp, _ := http.Get(ts.URL + "/healthz")
	if resp.StatusCode != 200 {
		t.Fatalf("healthz = %d", resp.StatusCode)
	}
}

func TestRequireAuthAndMe(t *testing.T) {
	s, ts := newTestServer(t, nil)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)
	u, err := s.q.CreateUser(ctx, createUserParams("me@test.dev", "display", now))
	if err != nil {
		t.Fatal(err)
	}
	raw, hash := auth.NewSessionToken()
	if _, err := s.q.CreateSession(ctx, sessionParams(hash, u.ID, now)); err != nil {
		t.Fatal(err)
	}

	// no cookie -> 401
	resp, _ := http.Get(ts.URL + "/api/v1/auth/me")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me without cookie = %d, want 401", resp.StatusCode)
	}

	// bad cookie -> 401
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "forged"})
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 401 {
		t.Fatalf("me with forged cookie = %d, want 401", resp.StatusCode)
	}

	// good cookie -> 200 with profile
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: raw})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var me struct {
		Email    string `json:"email"`
		IsAdmin  bool   `json:"is_admin"`
		Disabled bool   `json:"-"`
	}
	decodeBody(t, resp, &me)
	if me.Email != "me@test.dev" || me.IsAdmin {
		t.Fatalf("me = %+v", me)
	}
}

func TestRequireAuthDisabledUser(t *testing.T) {
	s, ts := newTestServer(t, nil)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)
	u, err := s.q.CreateUser(ctx, createUserParams("off@test.dev", "display", now))
	if err != nil {
		t.Fatal(err)
	}
	raw, hash := auth.NewSessionToken()
	if _, err := s.q.CreateSession(ctx, sessionParams(hash, u.ID, now)); err != nil {
		t.Fatal(err)
	}
	if err := s.q.SetUserDisabled(ctx, db.SetUserDisabledParams{Disabled: 1, UpdatedAt: now, ID: u.ID}); err != nil {
		t.Fatal(err)
	}

	// valid session, but the account got disabled afterwards -> 401
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: raw})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me with disabled account = %d, want 401", resp.StatusCode)
	}
}
