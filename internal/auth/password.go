package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// Bounds on parameters accepted from a STORED hash string (KiB / rounds /
// threads). A tampered or corrupted hash must be rejected before
// argon2.IDKey runs — the KDF allocates m KiB, so an unbounded m is a
// memory-DoS (a hash claiming m=1<<27 would allocate ~256 GiB). The
// envelope brackets every hash this package generates (m=64MiB, t=3, p=4)
// with generous headroom for future tunings.
const (
	argonMinMemoryKiB = 8 * 1024
	argonMaxMemoryKiB = 1 << 26
	argonMaxTime      = 64
	argonMaxThreads   = 16
)

// HashPassword returns an encoded Argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the encoded hash.
func VerifyPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != 19 {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	if m < argonMinMemoryKiB || m > argonMaxMemoryKiB ||
		t < 1 || t > argonMaxTime ||
		p < 1 || p > argonMaxThreads {
		return false // out-of-bounds params: reject BEFORE the KDF (m allocates)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	if len(want) == 0 {
		return false // argon2.IDKey panics with a zero-length key
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
