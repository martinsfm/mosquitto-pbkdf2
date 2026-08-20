package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

const (
	sessionCookieName = "gw_session"
	sessionTTL        = 24 * time.Hour
)

// auth is a minimal session store: one shared password (configured in
// gateway.yaml, same spirit as Kepware's project password) gates a
// random session token handed out as an HttpOnly cookie on login. This is
// meant to stop someone else on the factory LAN from casually opening
// the dashboard and repointing a PLC - it is not enterprise auth (no
// per-user accounts, no audit trail) and the connection is plain HTTP, so
// don't rely on it across an untrusted network.
type auth struct {
	password string // empty means auth is disabled entirely

	mu       sync.Mutex
	sessions map[string]time.Time // token -> expiry
}

func newAuth(password string) *auth {
	return &auth{password: password, sessions: map[string]time.Time{}}
}

func (a *auth) enabled() bool {
	return a.password != ""
}

// login checks pw against the configured password using a constant-time
// comparison (so response timing doesn't leak how many characters
// matched) and, on success, mints a new session token good for
// sessionTTL.
func (a *auth) login(pw string) (token string, ok bool) {
	if subtle.ConstantTimeCompare([]byte(pw), []byte(a.password)) != 1 {
		return "", false
	}
	token = randomToken()

	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepLocked()
	a.sessions[token] = time.Now().Add(sessionTTL)
	return token, true
}

// validate reports whether token is a live session, sliding its expiry
// forward on every successful check so an actively-used dashboard tab
// never gets logged out mid-session.
func (a *auth) validate(token string) bool {
	if token == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[token]
	if !ok || time.Now().After(exp) {
		delete(a.sessions, token)
		return false
	}
	a.sessions[token] = time.Now().Add(sessionTTL)
	return true
}

func (a *auth) logout(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
}

// sweepLocked drops expired sessions. Called with mu held; the session
// table for a local single-operator tool never grows large enough to
// need anything fancier than "clean up opportunistically on login".
func (a *auth) sweepLocked() {
	now := time.Now()
	for tok, exp := range a.sessions {
		if now.After(exp) {
			delete(a.sessions, tok)
		}
	}
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read failing means the OS RNG is broken - nothing
		// downstream of this call can be trusted either way.
		panic("webui: crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// requireAuth wraps a handler so it 401s (as JSON, since every protected
// route is either the REST API or the SSE stream) unless the request
// carries a valid session cookie. A no-op passthrough when no password is
// configured.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	if !s.auth.enabled() {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil || !s.auth.validate(c.Value) {
			writeError(w, http.StatusUnauthorized, errNotAuthenticated)
			return
		}
		next(w, r)
	}
}

var errNotAuthenticated = httpError("faça login novamente")

type httpError string

func (e httpError) Error() string { return string(e) }
