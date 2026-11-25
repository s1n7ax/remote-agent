package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// Agents share the host network, so they can reach the API like the user
// can (directly or through the node's own tailnet address). Every API call
// therefore needs either the session cookie a browser gets by pairing with
// the login token, or that token as a bearer (host scripts). Agents never see
// the token: it lives in the data dir, outside every sandbox.

const sessionCookie = "ra_session"

// Auth checks API credentials.
type Auth struct {
	// Token is the login token; empty disables authentication (tests only).
	Token string
	// Secret signs session cookies; rotating it signs every browser out.
	Secret []byte
}

func (a *Auth) session() string {
	m := hmac.New(sha256.New, a.Secret)
	m.Write([]byte("session"))
	return hex.EncodeToString(m.Sum(nil))
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// ok reports whether r carries a valid session cookie or bearer token.
func (a *Auth) ok(r *http.Request) bool {
	if a == nil || a.Token == "" {
		return true
	}
	if bearer, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); found && equal(bearer, a.Token) {
		return true
	}
	c, err := r.Cookie(sessionCookie)
	return err == nil && equal(c.Value, a.session())
}

// public lists what needs no credentials: liveness, the idle probe the
// deploy timer polls, login itself, and the static SPA.
func public(r *http.Request) bool {
	switch {
	case r.Method == http.MethodGet && (r.URL.Path == "/healthz" || r.URL.Path == "/api/idle"):
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/api/login":
		return true
	}
	return !strings.HasPrefix(r.URL.Path, "/api/")
}

func (a *Auth) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !public(r) && !a.ok(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in with the login token"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if s.Auth == nil || s.Auth.Token == "" {
		return ok(w, map[string]bool{"ok": true})
	}
	if !equal(strings.TrimSpace(in.Token), s.Auth.Token) {
		time.Sleep(500 * time.Millisecond) // slow down guessing
		return httpError{http.StatusUnauthorized, "wrong login token"}
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: s.Auth.session(), Path: "/", MaxAge: 400 * 24 * 3600,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, _ *http.Request) error {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	return ok(w, map[string]bool{"ok": true})
}
