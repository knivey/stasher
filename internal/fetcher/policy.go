package fetcher

import (
	"fmt"
	"net"
)

// Policy decides whether a resolved IP may be dialed. Production uses
// PublicPolicy (hard-coded, not configurable); tests may inject looser
// policies so httptest targets work.
type Policy interface {
	Allowed(ip net.IP) bool
}

// The rejected set is hard-coded per spec §2 (Global-Constraint ranges):
// loopback (127/8, ::1), RFC1918 (10/8, 172.16/12, 192.168/16), link-local
// (169.254/16, fe80::/10), ULA (fc00::/7), IPv4-mapped IPv6, unspecified
// (0.0.0.0, ::), and multicast. Widened 2026-10-23 with 0.0.0.0/8 ("this
// network"), 240.0.0.0/4 (reserved, includes the 255.255.255.255 broadcast
// address), and deprecated IPv4-compatible IPv6 ::/96. Spelled out here so
// review sees the exact list; parsed once at init (panic = programmer error
// in these literals).
var (
	privateV4 = mustParseCIDRs(
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16",
		"0.0.0.0/8", "240.0.0.0/4",
	)
	uniqueLocal = mustParseCIDRs("fc00::/7")
	// IPv4-compatible IPv6 (::a.b.c.d) does NOT hit the To4() unwrap below —
	// that only matches the ::ffff:0:0/96 mapped form — so it needs its own
	// block. The range's special cases (::, ::1) are already rejected above.
	v4CompatibleV6 = mustParseCIDRs("::/96")
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(fmt.Sprintf("fetcher: bad hard-coded CIDR %q: %v", c, err))
		}
		out = append(out, n)
	}
	return out
}

type publicPolicy struct{}

// PublicPolicy rejects every non-routable / private range and allows only
// public unicast addresses (v4 + v6).
func PublicPolicy() Policy { return publicPolicy{} }

func (publicPolicy) Allowed(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		// To4 also unwraps IPv4-mapped IPv6, so ::ffff:10.0.0.1 and
		// ::ffff:127.0.0.1 hit these v4 checks (mapped loopback via
		// IsLoopback above).
		for _, n := range privateV4 {
			if n.Contains(ip4) {
				return false
			}
		}
		return true
	}
	// v6: unique local addresses fc00::/7 (fd12::1 included), then the
	// deprecated IPv4-compatible form ::/96.
	for _, n := range uniqueLocal {
		if n.Contains(ip) {
			return false
		}
	}
	for _, n := range v4CompatibleV6 {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

type loopbackPolicy struct{}

// LoopbackPolicy allows everything. TEST-ONLY: lets httptest targets (which
// bind loopback) through the policy. Never used in production wiring.
func LoopbackPolicy() Policy { return loopbackPolicy{} }

func (loopbackPolicy) Allowed(net.IP) bool { return true }
