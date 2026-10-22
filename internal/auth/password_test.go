package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"testing"

	"golang.org/x/crypto/argon2"
)

// craftHash builds a GENUINE argon2id hash (real KDF output) with the given
// encoded parameters, so a verifier without parameter bounds will happily
// compute the KDF and accept it.
func craftHash(t *testing.T, m, ti, p uint32) string {
	t.Helper()
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	key := argon2.IDKey([]byte("x"), salt, ti, m, uint8(p), argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		m, ti, p,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
}

// TestVerifyPasswordParamBounds pins the stored-hash guard: encoded m/t/p
// outside the accepted envelope must be rejected BEFORE the KDF runs.
// Out-of-bounds cases use real (cheap) hashes so a verifier without the
// guard fails this test by wrongly accepting them; the in-bounds minimum
// must still verify.
func TestVerifyPasswordParamBounds(t *testing.T) {
	if VerifyPassword(craftHash(t, 4096, 1, 1), "x") { // m below 8 MiB floor
		t.Error("m=4096 (below floor) verified; bounds check missing")
	}
	if VerifyPassword(craftHash(t, argonMemory, 65, 1), "x") { // t above 64
		t.Error("t=65 verified; bounds check missing")
	}
	if VerifyPassword(craftHash(t, argonMemory, 1, 17), "x") { // p above 16
		t.Error("p=17 verified; bounds check missing")
	}
	if VerifyPassword(craftHash(t, (1<<20)+1024, 1, 1), "x") { // just above the 1 GiB m ceiling
		t.Error("m=1<<20+1024 (above cap) verified; bounds check missing")
	}
	// genuine hash with the cheapest in-bounds params: must verify
	if h := craftHash(t, 8*1024, 1, 1); !VerifyPassword(h, "x") {
		t.Error("in-bounds m=8192,t=1,p=1 failed to verify")
	}
}

// TestVerifyPasswordHugeMRejectedWithoutKDF: hashes whose encoded m claims
// a huge work factor must be rejected on parameters alone — verifying them
// would mean running the KDF (multi-GiB allocation). Do NOT run this test
// against an unguarded VerifyPassword: evaluating the hash pre-fix IS the
// out-of-memory the guard exists to prevent (the uint32-overflow case is
// safe either way; the in-range-but-over-cap case crashes the binary).
func TestVerifyPasswordHugeMRejectedWithoutKDF(t *testing.T) {
	for _, h := range []string{
		// m overflows uint32 outright (rejected at parse even pre-guard)
		"$argon2id$v=19$m=4294967296,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		// m fits uint32 but exceeds the 1<<20 KiB (1 GiB) cap (~256 GiB claim)
		"$argon2id$v=19$m=268435456,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		if VerifyPassword(h, "x") {
			t.Errorf("huge-m hash %.40q... verified", h)
		}
	}
}

func TestHashVerifyRoundtrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse battery staple") {
		t.Fatal("correct password failed to verify")
	}
	if VerifyPassword(h, "wrong password") {
		t.Fatal("wrong password verified")
	}
}

func TestHashFormat(t *testing.T) {
	h, _ := HashPassword("x")
	want := "$argon2id$v=19$m=65536,t=3,p=4$"
	if len(h) < len(want) || h[:len(want)] != want {
		t.Fatalf("hash prefix = %q, want %q...", h, want)
	}
}

func TestVerifyMalformedHash(t *testing.T) {
	for _, h := range []string{
		"", "plaintext", "$argon2id$v=19$", "$argon2id$v=19$m=65536,t=3,p=4$notb64$notb64",
		"$argon2id$v=19$m=65536,t=0,p=4$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=65536,t=3,p=0$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=65536,t=3,p=4$AAAAAAAAAAAAAAAAAAAAAA$",
	} {
		if VerifyPassword(h, "x") {
			t.Errorf("malformed hash %q verified", h)
		}
	}
}
