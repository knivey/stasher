package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
)

// CookieName is the session cookie name.
const CookieName = "stasher_session"

// NewSessionToken returns the raw token (sent to the client in a cookie) and
// its SHA-256 hex hash (the only thing stored in the database).
func NewSessionToken() (raw, hash string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashToken(raw)
}

// HashToken maps a raw token to its stored hash.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// SessionCookie returns the 10-year persistent session cookie.
func SessionCookie(raw string) *http.Cookie {
	return &http.Cookie{
		Name: CookieName, Value: raw, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		MaxAge: 315360000,
	}
}

// ClearSessionCookie expires the session cookie.
func ClearSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name: CookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		MaxAge: -1,
	}
}
