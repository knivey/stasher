package auth

import "testing"

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
	for _, h := range []string{"", "plaintext", "$argon2id$v=19$", "$argon2id$v=19$m=65536,t=3,p=4$notb64$notb64"} {
		if VerifyPassword(h, "x") {
			t.Errorf("malformed hash %q verified", h)
		}
	}
}
