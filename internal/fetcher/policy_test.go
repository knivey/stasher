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
