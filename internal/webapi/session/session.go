// Package session implements a signed-cookie session, compatible in spirit
// with Django's session auth: the cookie stores the user id and an expiry,
// signed with SECRET_KEY.
package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const CookieName = "sessionid"

// maxAge matches Django's default SESSION_COOKIE_AGE (two weeks).
const maxAge = 14 * 24 * time.Hour

type Manager struct {
	secret []byte
	secure bool
}

func New(secret string, secure bool) *Manager {
	return &Manager{secret: []byte(secret), secure: secure}
}

func (m *Manager) sign(payload string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Login sets the session cookie identifying the user.
func (m *Manager) Login(w http.ResponseWriter, userID string) {
	expires := time.Now().Add(maxAge)
	payload := userID + ":" + strconv.FormatInt(expires.Unix(), 10)
	value := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + m.sign(payload)
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// UserID returns the authenticated user id from the request cookie, if the
// signature is valid and the session has not expired.
func (m *Manager) UserID(r *http.Request) (string, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return "", false
	}
	encoded, sig, found := strings.Cut(c.Value, ".")
	if !found {
		return "", false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	payload := string(payloadBytes)
	if !hmac.Equal([]byte(m.sign(payload)), []byte(sig)) {
		return "", false
	}
	idx := strings.LastIndex(payload, ":")
	if idx == -1 {
		return "", false
	}
	userID := payload[:idx]
	expires, err := strconv.ParseInt(payload[idx+1:], 10, 64)
	if err != nil || time.Now().Unix() > expires {
		return "", false
	}
	return userID, true
}

// Logout clears the session cookie (Django's session.flush()).
func (m *Manager) Logout(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}
