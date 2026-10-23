package fetcher

import (
	"net"
	"testing"
)

func TestPublicPolicyBlocksPrivateRanges(t *testing.T) {
	p := PublicPolicy()
	blocked := []string{
		"127.0.0.1", "127.8.8.8", "10.0.0.1", "172.16.0.1", "172.31.255.255",
		"192.168.1.1", "169.254.1.1", "0.0.0.0",
		"::1", "fe80::1", "fc00::1", "fd12::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "::",
		// multicast (brief's policy checks IsMulticast and friends)
		"224.0.0.1", "ff0e::1",
		// 2026-10-23 widening: 0.0.0.0/8 ("this network"), 240.0.0.0/4
		// (reserved, includes the 255.255.255.255 broadcast address), and
		// deprecated IPv4-compatible IPv6 ::/96 (the v4 bytes sit in the
		// low 32 bits, NOT the ::ffff: mapped range To4() unwraps).
		"0.0.0.1", "0.255.255.255",
		"240.0.0.0", "240.0.0.1", "250.1.2.3", "255.255.255.255",
		"::10.0.0.1", "::127.0.0.1", "::192.168.1.1", "::7f00:1",
	}
	for _, s := range blocked {
		if p.Allowed(net.ParseIP(s)) {
			t.Errorf("PublicPolicy.Allowed(%s) = true, want false", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111"}
	for _, s := range allowed {
		if !p.Allowed(net.ParseIP(s)) {
			t.Errorf("PublicPolicy.Allowed(%s) = false, want true", s)
		}
	}
}

// LoopbackPolicy stays allow-everything: the fetch handler tests depend on
// httptest loopback targets passing it, and the widened PublicPolicy ranges
// must not leak into it.
func TestLoopbackPolicyStillAllowsEverything(t *testing.T) {
	p := LoopbackPolicy()
	for _, s := range []string{
		"127.0.0.1", "::1", "0.0.0.1", "240.0.0.1", "255.255.255.255",
		"::10.0.0.1", "10.0.0.1", "8.8.8.8",
	} {
		if !p.Allowed(net.ParseIP(s)) {
			t.Errorf("LoopbackPolicy.Allowed(%s) = false, want true", s)
		}
	}
}
