// Package ip provide ip tools
package ip

import (
	"net"
	"net/http"
	"strings"
)

// GetLocalIP get local ip.
func GetLocalIP() net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP
			}
		}
	}
	return nil
}

// GetIPFromHTTPRequest returns the first X-Forwarded-For entry, whoever sent
// it, and writes the header when it is absent.
//
// Deprecated: the first entry is whatever the client chose to send, so this
// value must not reach a log, a stored record or a limiter. Use
// [TrustedProxies.FromRequest], or [ClientIPFromContext] inside a grpcmux
// handler.
func GetIPFromHTTPRequest(r *http.Request) string {
	ip := r.Header.Get("x-forwarded-for")
	if ip != "" {
		return strings.SplitN(ip, ",", 2)[0]
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	r.Header.Set("x-forwarded-for", host)
	return host
}

// GetRemoteIP get ip from http request.
func GetRemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
