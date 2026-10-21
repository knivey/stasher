package auth

import "testing"

func TestLimiterAllowsUpToBurst(t *testing.T) {
	l := NewLimiter(60, 3) // 1/sec, burst 3
	for i := 0; i < 3; i++ {
		if !l.Allow("k") {
			t.Fatalf("attempt %d denied within burst", i+1)
		}
	}
	if l.Allow("k") {
		t.Fatal("4th immediate attempt allowed, want denied")
	}
	if !l.Allow("other") {
		t.Fatal("different key denied") // keys are independent
	}
}
