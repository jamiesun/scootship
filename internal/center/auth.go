package center

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jamiesun/scootship/internal/version"
)

type ctxKey int

const nodeIDKey ctxKey = iota

// --- node auth (edge-facing, bearer token) ---

// requireNode authenticates an edge by per-node bearer token and pins the
// authenticated node_id into the request context. A token may only ever speak
// for its own node.
func (s *Server) requireNode(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearerToken(r)
		if tok == "" {
			writeJSONError(w, http.StatusUnauthorized, "no_token", "missing Authorization: Bearer <token>")
			return
		}
		node, ok := s.tokens.NodeFor(tok)
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "bad_token", "unrecognized node token")
			return
		}
		ctx := context.WithValue(r.Context(), nodeIDKey, node)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

func nodeFromCtx(r *http.Request) string {
	if v, ok := r.Context().Value(nodeIDKey).(string); ok {
		return v
	}
	return ""
}

// --- dashboard auth (operator-facing, login session) ---

// requireAdmin gates the dashboard behind a login session. Unauthenticated HTML
// requests are redirected to /login; unauthenticated API requests get 401 JSON.
// With no password configured the dashboard is locked (the center is never
// accidentally world-readable); dev mode seeds a default password instead.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AdminPassword == "" {
			writeJSONError(w, http.StatusServiceUnavailable, "dashboard_locked",
				"dashboard auth is not configured: set SCOOTSHIP_ADMIN_PASSWORD (or SCOOTSHIP_DEV=1 for local use)")
			return
		}
		if _, ok := s.currentUser(r); !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized", "login required")
				return
			}
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) currentUser(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	return s.sessions.validate(c.Value)
}

// loginPage is the login view model.
type loginPage struct {
	Title      string
	Version    string
	Next       string
	Error      string
	Configured bool
	DevHint    bool
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentUser(r); ok {
		http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	s.renderLogin(w, safeNext(r.URL.Query().Get("next")), "")
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdminPassword == "" {
		s.renderLogin(w, "/", "Dashboard auth is not configured on the server.")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderLogin(w, "/", "Could not read the form.")
		return
	}
	ip := s.clientIP(r)
	now := s.now()
	user := r.PostFormValue("user")
	pass := r.PostFormValue("password")
	next := safeNext(r.PostFormValue("next"))

	// Brute-force throttle: refuse a locked-out source IP before touching
	// credentials, so a guesser cannot keep trying during the lockout.
	if d := s.loginGuard.Check(ip, now); !d.Allowed {
		s.log.Warn("dashboard login throttled", "ip", ip, "retry_after_s", int(d.RetryAfter.Seconds()))
		s.tooManyLogins(w, next, d.RetryAfter)
		return
	}

	// Constant-time compare; never log the password.
	if !constantEq(user, s.cfg.AdminUser) || !constantEq(pass, s.cfg.AdminPassword) {
		d := s.loginGuard.Fail(ip, now)
		if !d.Allowed {
			s.log.Warn("dashboard login locked out", "ip", ip, "retry_after_s", int(d.RetryAfter.Seconds()))
			s.tooManyLogins(w, next, d.RetryAfter)
			return
		}
		// Generic message: do not confirm whether the username exists.
		s.log.Warn("failed dashboard login", "ip", ip, "remaining", d.Remaining)
		s.renderLoginStatus(w, http.StatusUnauthorized, next, "Invalid username or password.")
		return
	}

	s.loginGuard.Reset(ip)
	token, exp, err := s.sessions.create(user)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "session_error", "could not create session")
		return
	}
	s.setSessionCookie(w, token, exp)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// tooManyLogins renders the login page with 429 and a Retry-After hint, without
// leaking whether the credentials were otherwise valid.
func (s *Server) tooManyLogins(w http.ResponseWriter, next string, retryAfter time.Duration) {
	secs := int(retryAfter.Seconds())
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	s.renderLoginStatus(w, http.StatusTooManyRequests, next,
		"Too many failed attempts. Please wait and try again.")
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.destroy(c.Value)
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) renderLogin(w http.ResponseWriter, next, errMsg string) {
	s.renderLoginStatus(w, http.StatusOK, next, errMsg)
}

// renderLoginStatus renders the login page with an explicit status code so a
// rejected (401) or throttled (429) attempt carries the right semantics.
func (s *Server) renderLoginStatus(w http.ResponseWriter, status int, next, errMsg string) {
	s.renderStatus(w, status, "login", loginPage{
		Title:      "Sign in",
		Version:    version.Version,
		Next:       next,
		Error:      errMsg,
		Configured: s.cfg.AdminPassword != "",
		DevHint:    s.cfg.Dev,
	})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.TLSEnabled(),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.TLSEnabled(),
	})
}

// safeNext keeps post-login redirects local-only (no open redirect).
func safeNext(p string) string {
	if p == "" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return "/"
	}
	return p
}

func constantEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// --- shared JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"ok": false, "code": code, "error": msg})
}
