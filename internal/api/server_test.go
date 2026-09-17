package api

import (
	"net"
	"net/http/httptest"
	"testing"
)

func TestClientIPUsesForwardedHeaderOnlyFromTrustedProxy(t *testing.T) {
	_, trusted, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{trustedProxies: []*net.IPNet{trusted}}

	trustedRequest := httptest.NewRequest("GET", "http://portal.example", nil)
	trustedRequest.RemoteAddr = "10.1.2.3:1234"
	trustedRequest.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := server.clientIP(trustedRequest); got != "203.0.113.9" {
		t.Fatalf("trusted proxy client IP = %q", got)
	}

	directRequest := httptest.NewRequest("GET", "http://portal.example", nil)
	directRequest.RemoteAddr = "192.0.2.10:1234"
	directRequest.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := server.clientIP(directRequest); got != "192.0.2.10" {
		t.Fatalf("direct client IP = %q", got)
	}
}
