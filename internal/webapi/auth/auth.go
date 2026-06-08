// Package auth handles session-based login for the dashboard API.
//
// Ported from web_api/core/auth.py. Replaces Django's session middleware with
// signed cookies (gorilla/securecookie equivalent built on stdlib HMAC).
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const cookieName = "kodiak_session"

// CtxKey is used to fish the authenticated user ID out of the request context.
type ctxKey struct{}

// Session holds the per-user session data.
type Session struct {
	UserID uuid.UUID
	IssuedAt time.Time
}

// Manager signs and verifies session cookies.
type Manager struct {
	secret []byte
}

// New constructs a Manager. The secret must be at least 32 bytes.
func New(secret string) *Manager {
	return &Manager{secret: []byte(secret)}
}

// LoginRequired wraps a handler so it only runs when a valid session is present.
func (m *Manager) LoginRequired(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := m.Decode(r)
		if err != nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, sess)
		h(w, r.WithContext(ctx))
	}
}

// FromContext fetches the session attached to the request. Returns ok=false
// when the handler wasn't wrapped in LoginRequired.
func FromContext(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(ctxKey{}).(Session)
	return s, ok
}

// Login writes a signed cookie carrying the user ID.
func (m *Manager) Login(w http.ResponseWriter, userID uuid.UUID) {
	value := m.sign(userID.String() + "|" + time.Now().UTC().Format(time.RFC3339))
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(30 * 24 * time.Hour),
	})
}

// Logout clears the session cookie.
func (m *Manager) Logout(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:    cookieName,
		Value:   "",
		Path:    "/",
		Expires: time.Unix(0, 0),
		MaxAge:  -1,
	})
}

// Decode reads + verifies the cookie.
func (m *Manager) Decode(r *http.Request) (Session, error) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return Session{}, err
	}
	payload, err := m.verify(c.Value)
	if err != nil {
		return Session{}, err
	}
	parts := strings.SplitN(payload, "|", 2)
	if len(parts) != 2 {
		return Session{}, errors.New("malformed session")
	}
	uid, err := uuid.Parse(parts[0])
	if err != nil {
		return Session{}, err
	}
	t, _ := time.Parse(time.RFC3339, parts[1])
	return Session{UserID: uid, IssuedAt: t}, nil
}

func (m *Manager) sign(payload string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sig
}

func (m *Manager) verify(raw string) (string, error) {
	parts := strings.SplitN(raw, ".", 2)
	if len(parts) != 2 {
		return "", errors.New("malformed cookie")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	expected := m.sign(string(payload))
	if !hmac.Equal([]byte(expected), []byte(raw)) {
		return "", errors.New("bad signature")
	}
	return string(payload), nil
}
