package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"log-download-portal/internal/config"
)

// SessionManager issues and verifies stateless HMAC-signed session cookies.
//
// The cookie itself carries the username and expiry (JSON, base64url), sealed
// with an HMAC-SHA256 tag derived from the configured session key. No server
// side session table is kept, so the same cookie is valid across every portal
// replica. The login failure limiter is intentionally kept in-memory per pod:
// it is a best-effort brake against online brute force, and accepting a 2x
// budget across replicas is a fair trade-off for not requiring shared state.
type SessionManager struct {
	cfg      config.AuthConfig
	mu       sync.Mutex
	failures map[string]failureWindow
}

// token is the signed payload embedded in the session cookie.
type token struct {
	Username string `json:"u"`
	Expires  int64  `json:"e"` // unix seconds
}

type failureWindow struct {
	Count     int
	StartedAt time.Time
}

func NewSessionManager(cfg config.AuthConfig) *SessionManager {
	return &SessionManager{
		cfg:      cfg,
		failures: map[string]failureWindow{},
	}
}

func (m *SessionManager) Authenticate(remoteAddr, username, password string) error {
	key := clientKey(remoteAddr)
	if m.isLimited(key) {
		return ErrRateLimited
	}
	if username != m.cfg.Username {
		m.recordFailure(key)
		_ = bcrypt.CompareHashAndPassword([]byte(m.cfg.PasswordHash), []byte(password))
		return ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(m.cfg.PasswordHash), []byte(password)); err != nil {
		m.recordFailure(key)
		return ErrInvalidCredentials
	}
	m.clearFailure(key)
	return nil
}

func (m *SessionManager) Create(w http.ResponseWriter, username string) {
	expires := time.Now().Add(m.cfg.SessionTTL.Duration)
	value, err := m.sign(token{Username: username, Expires: expires.Unix()})
	if err != nil {
		// Marshal of a struct of (string, int64) cannot fail in practice.
		panic(err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     m.cfg.CookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(m.cfg.SessionTTL.Duration.Seconds()),
		HttpOnly: true,
		Secure:   m.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Clear expires the cookie on the client. Because sessions are stateless,
// there is no server-side record to delete; a stolen cookie remains valid
// until its embedded expiry, which is the same trust boundary as before.
func (m *SessionManager) Clear(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.cfg.CookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *SessionManager) User(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(m.cfg.CookieName)
	if err != nil {
		return "", false
	}
	t, ok := m.verify(cookie.Value)
	if !ok {
		return "", false
	}
	if time.Now().Unix() > t.Expires {
		return "", false
	}
	return t.Username, true
}

func (m *SessionManager) sign(t token) (string, error) {
	payload, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, m.cfg.SessionKey)
	_, _ = mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + sig, nil
}

func (m *SessionManager) verify(value string) (token, bool) {
	payload, sig, ok := strings.Cut(value, ".")
	if !ok || payload == "" || sig == "" {
		return token{}, false
	}
	mac := hmac.New(sha256.New, m.cfg.SessionKey)
	_, _ = mac.Write([]byte(payload))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return token{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return token{}, false
	}
	var t token
	if err := json.Unmarshal(raw, &t); err != nil {
		return token{}, false
	}
	return t, true
}

func (m *SessionManager) isLimited(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	window, ok := m.failures[key]
	if !ok {
		return false
	}
	if time.Since(window.StartedAt) > m.cfg.FailureWindow.Duration {
		delete(m.failures, key)
		return false
	}
	return window.Count >= m.cfg.FailureLimit
}

func (m *SessionManager) recordFailure(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	window := m.failures[key]
	if window.StartedAt.IsZero() || time.Since(window.StartedAt) > m.cfg.FailureWindow.Duration {
		window = failureWindow{StartedAt: time.Now()}
	}
	window.Count++
	m.failures[key] = window
}

func (m *SessionManager) clearFailure(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.failures, key)
}

func clientKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrRateLimited        = errors.New("too many login failures")
)
