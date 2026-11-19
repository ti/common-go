package ip

import (
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

// TrustedProxies decides which peers may speak for a client through
// X-Forwarded-For. A header written by anyone else is ignored: a client that
// connects directly cannot pick the address it is seen as, which matters as
// soon as the address is used as a rate-limit or audit key.
//
// The zero value and a nil *TrustedProxies trust nobody: the client address
// is the connection's peer. Trust has to be configured, because a wrong guess
// lets any caller choose the address it is seen as (see
// [ParseTrustedProxies]).
type TrustedProxies struct {
	prefixes []netip.Prefix
	private  bool
	none     bool
}

// cgnat is the RFC 6598 shared address space. Kubernetes clusters (EKS
// secondary CIDRs in particular) use it for pod addresses, so an in-cluster
// proxy can have such an address.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// ParseTrustedProxies builds a TrustedProxies from a list of CIDRs
// ("10.0.0.0/8"), single addresses ("192.0.2.7") and the keywords
//
//	private   loopback, RFC 1918, RFC 6598 (100.64.0.0/10), link-local and
//	          IPv6 unique-local addresses: what a proxy inside the cluster or
//	          VPC has. Only correct when every private peer really is a proxy
//	          that sets the header: behind an L4 load balancer or kube-proxy
//	          SNAT the peer is private too, and the header is the client's own
//	none      trust nobody: always use the connection's peer address
//
// An empty list means "none".
func ParseTrustedProxies(list []string) (*TrustedProxies, error) {
	t := &TrustedProxies{}
	if len(list) == 0 {
		t.none = true
		return t, nil
	}
	for _, s := range list {
		s = strings.TrimSpace(s)
		switch strings.ToLower(s) {
		case "":
			continue
		case "private":
			t.private = true
			continue
		case "none":
			t.none = true
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			t.prefixes = append(t.prefixes, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q is not a CIDR, an address, \"private\" or \"none\"", s)
		}
		a = a.Unmap()
		t.prefixes = append(t.prefixes, netip.PrefixFrom(a, a.BitLen()))
	}
	if t.none && (t.private || len(t.prefixes) > 0) {
		return nil, fmt.Errorf("trusted proxies: \"none\" cannot be combined with other entries")
	}
	return t, nil
}

// Contains reports whether addr is a trusted proxy.
func (t *TrustedProxies) Contains(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() {
		return false
	}
	if t == nil || t.none {
		return false
	}
	if t.private && isPrivate(addr) {
		return true
	}
	for _, p := range t.prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func isPrivate(addr netip.Addr) bool {
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || cgnat.Contains(addr)
}

// ClientIP returns the client address for a connection from remoteAddr
// ("host:port" or a bare host) that carried the given X-Forwarded-For header
// values.
//
// When the peer is not a trusted proxy the header is ignored and the peer
// address is returned. Otherwise the list is read from the right (each proxy
// appends the address it received the request from): trusted proxies are
// skipped and the first address that is not one is the client. A client can
// prepend anything it likes to the header, but it cannot change that entry.
// When every entry is trusted (a call from inside the cluster) the leftmost
// one is returned.
func (t *TrustedProxies) ClientIP(remoteAddr string, forwardedFor []string) string {
	peer, ok := parseHost(remoteAddr)
	if !ok {
		return remoteAddr
	}
	if !t.Contains(peer) {
		return peer.String()
	}
	client := peer
	for _, line := range slices.Backward(forwardedFor) {
		for _, entry := range slices.Backward(strings.Split(line, ",")) {
			hop, ok := parseHost(strings.TrimSpace(entry))
			if !ok {
				// Not an address: nothing left of it can be trusted either.
				return client.String()
			}
			client = hop
			if !t.Contains(hop) {
				return client.String()
			}
		}
	}
	return client.String()
}

// FromRequest returns the client address of an HTTP request, see
// [TrustedProxies.ClientIP].
func (t *TrustedProxies) FromRequest(r *http.Request) string {
	return t.ClientIP(r.RemoteAddr, r.Header.Values("X-Forwarded-For"))
}

// parseHost parses "host", "host:port" and "[v6]:port".
func parseHost(s string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap(), true
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap().WithZone(""), true
}

// String describes the trust model, for a startup log line.
func (t *TrustedProxies) String() string {
	if t == nil || t.none || (!t.private && len(t.prefixes) == 0) {
		return "none (X-Forwarded-For is ignored, the client IP is the connection's peer)"
	}
	var parts []string
	if t.private {
		parts = append(parts, "private networks")
	}
	for _, p := range t.prefixes {
		parts = append(parts, p.String())
	}
	return strings.Join(parts, ", ") + " (X-Forwarded-For from these peers is believed)"
}
