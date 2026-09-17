package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"

	"log-download-portal/internal/security"
)

type requestIDKey struct{}

func requestIDFrom(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)
	return value
}

// withRequestID mints a fresh request id for every connection. An external
// X-Request-ID header is honoured only when the immediate peer is a trusted
// proxy, so an end client cannot pin an arbitrary id into the audit log.
func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := newRequestID()
		if s.fromTrustedProxy(r) {
			if external := r.Header.Get("X-Request-ID"); security.ValidateRequestID(external) {
				requestID = external
			}
		}
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID)))
	})
}

func (s *Server) fromTrustedProxy(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return s.isTrustedProxy(ip)
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "request-id-unavailable"
	}
	return hex.EncodeToString(b[:])
}
