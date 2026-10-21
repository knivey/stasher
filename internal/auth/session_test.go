package auth

import (
	"net/http"
	"testing"
)

func TestNewSessionTokenRoundtrip(t *testing.T) {
	raw, hash := NewSessionToken()
	if raw == "" || hash == "" || raw == hash {
		t.Fatalf("raw=%q hash=%q", raw, hash)
	}
	if HashToken(raw) != hash {
		t.Fatal("HashToken(raw) != hash")
	}
	raw2, _ := NewSessionToken()
	if raw2 == raw {
		t.Fatal("tokens are not unique")
	}
}

func TestSessionCookieFields(t *testing.T) {
	c := SessionCookie("abc")
	if c.Name != CookieName || c.Value != "abc" || c.Path != "/" ||
		!c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 315360000 {
		t.Fatalf("cookie = %+v", c)
	}
	d := ClearSessionCookie()
	if d.MaxAge != -1 || d.Value != "" {
		t.Fatalf("clear cookie = %+v", d)
	}
}
