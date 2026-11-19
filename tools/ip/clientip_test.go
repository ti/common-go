package ip

import (
	"net/http"
	"testing"
)

func TestClientIP(t *testing.T) {
	custom, err := ParseTrustedProxies([]string{"203.0.113.0/24", "198.51.100.7"})
	if err != nil {
		t.Fatal(err)
	}
	none, err := ParseTrustedProxies([]string{"none"})
	if err != nil {
		t.Fatal(err)
	}
	private, err := ParseTrustedProxies([]string{"private"})
	if err != nil {
		t.Fatal(err)
	}
	unset, err := ParseTrustedProxies(nil)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		proxies *TrustedProxies
		remote  string
		xff     []string
		want    string
	}{
		{"direct public client, forged header ignored", private, "198.51.100.9:4711", []string{"1.2.3.4"}, "198.51.100.9"},
		{"behind one private proxy", private, "10.0.3.4:55000", []string{"198.51.100.9"}, "198.51.100.9"},
		{"client prepends a forged hop", private, "10.0.3.4:55000", []string{"1.2.3.4, 198.51.100.9, 10.0.0.5"}, "198.51.100.9"},
		{"header split over several lines", private, "10.0.3.4:55000", []string{"1.2.3.4", "198.51.100.9", "100.64.3.1"}, "198.51.100.9"},
		{"in-cluster caller, no header", private, "10.0.3.4:55000", nil, "10.0.3.4"},
		{"every hop trusted: leftmost", private, "10.0.3.4:55000", []string{"10.1.1.1, 10.2.2.2"}, "10.1.1.1"},
		{"garbage stops the walk", private, "10.0.3.4:55000", []string{"unknown, 10.2.2.2"}, "10.2.2.2"},
		{"ipv6 peer and client", private, "[::1]:8080", []string{"2001:db8::7"}, "2001:db8::7"},
		{"ipv4-mapped peer", private, "[::ffff:10.0.0.1]:8080", []string{"198.51.100.9"}, "198.51.100.9"},
		{"custom list: private peer is not trusted", custom, "10.0.3.4:55000", []string{"1.2.3.4"}, "10.0.3.4"},
		{"custom list: listed peer is trusted", custom, "203.0.113.5:443", []string{"1.2.3.4, 198.51.100.7"}, "1.2.3.4"},
		{"none: header never used", none, "10.0.3.4:55000", []string{"198.51.100.9"}, "10.0.3.4"},
		{"unparsable peer returned as is", private, "@", []string{"198.51.100.9"}, "@"},
		// The default trusts nobody: a private or loopback peer cannot pick
		// its address with the header either (L4 load balancer, SNAT).
		{"default: nil ignores the header from a private peer", nil, "10.0.3.4:55000", []string{"198.51.100.9"}, "10.0.3.4"},
		{"default: nil ignores the header from loopback", nil, "127.0.0.1:55000", []string{"198.51.100.9"}, "127.0.0.1"},
		{"default: empty list ignores the header", unset, "10.0.3.4:55000", []string{"198.51.100.9"}, "10.0.3.4"},
		{"default: zero value ignores the header", &TrustedProxies{}, "10.0.3.4:55000", []string{"198.51.100.9"}, "10.0.3.4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.proxies.ClientIP(tt.remote, tt.xff); got != tt.want {
				t.Errorf("ClientIP(%q, %q) = %q, want %q", tt.remote, tt.xff, got, tt.want)
			}
		})
	}
}

func TestFromRequest(t *testing.T) {
	r := &http.Request{RemoteAddr: "10.0.0.1:1234", Header: http.Header{}}
	r.Header.Add("X-Forwarded-For", "198.51.100.9, 10.0.0.2")
	private, _ := ParseTrustedProxies([]string{"private"})
	if got := private.FromRequest(r); got != "198.51.100.9" {
		t.Errorf("FromRequest = %q", got)
	}
	if got := (*TrustedProxies)(nil).FromRequest(r); got != "10.0.0.1" {
		t.Errorf("FromRequest without trust = %q, want the peer", got)
	}
}

func TestParseTrustedProxiesErrors(t *testing.T) {
	for _, list := range [][]string{{"10.0.0.0/33"}, {"proxy.internal"}, {"none", "private"}} {
		if _, err := ParseTrustedProxies(list); err == nil {
			t.Errorf("ParseTrustedProxies(%q): expected an error", list)
		}
	}
}
