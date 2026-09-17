package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"log-download-portal/internal/config"
)

type SessionManager struct {
	cfg      config.AuthConfig
	mu       sync.Mutex
	sessions map[string]session
	failures map[string]failureWindow
}

type session struct {
	Username  string
	ExpiresAt time.Time
}

type failureWindow struct {
	Count     int
	StartedAt time.Time
}

func NewSessionManager(cfg config.AuthConfig) *SessionManager {
	return &SessionManager{
		cfg:      cfg,
		sessions: map[string]session{},
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
	id := randomHex(32)
	expires := time.Now().Add(m.cfg.SessionTTL.Duration)

	m.mu.Lock()
	m.sessions[id] = session{Username: username, ExpiresAt: expires}
	m.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     m.cfg.CookieName,
		Value:    m.sign(id),
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(m.cfg.SessionTTL.Duration.Seconds()),
		HttpOnly: true,
		Secure:   m.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *SessionManager) Clear(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(m.cfg.CookieName); err == nil {
		if id, ok := m.verify(cookie.Value); ok {
			m.mu.Lock()
			delete(m.sessions, id)
			m.mu.Unlock()
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     m.cfg.CookieName,
		Value:    "",
		Path:     "/",
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
	id, ok := m.verify(cookie.Value)
	if !ok {
		return "", false
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || now.After(s.ExpiresAt) {
		delete(m.sessions, id)
		return "", false
	}
	return s.Username, true
}

func (m *SessionManager) sign(id string) string {
	mac := hmac.New(sha256.New, m.cfg.SessionKey)
	_, _ = mac.Write([]byte(id))
	return id + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *SessionManager) verify(value string) (string, bool) {
	id, sig, ok := strings.Cut(value, ".")
	if !ok || id == "" || sig == "" {
		return "", false
	}
	mac := hmac.New(sha256.New, m.cfg.SessionKey)
	_, _ = mac.Write([]byte(id))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", false
	}
	return id, true
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

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrRateLimited        = errors.New("too many login failures")
)
