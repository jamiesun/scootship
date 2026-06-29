package center

import (
	"context"
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
		node, ok := s.tokens.NodeForAt(tok, s.now())
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
		if !s.operators.Configured() {
			writeJSONError(w, http.StatusServiceUnavailable, "dashboard_locked",
				"dashboard auth is not configured: set SCOOTSHIP_ADMIN_PASSWORD to bootstrap the first operator (or SCOOTSHIP_DEV=1 for local use)")
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
	Title       string
	Version     string
	Next        string
	Error       string
	Configured  bool
	DevHint     bool
	Lang        string
	CurrentPath string
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentUser(r); ok {
		http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	s.renderLogin(w, r, safeNext(r.URL.Query().Get("next")), "")
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	lang := requestLang(r)
	if !s.operators.Configured() {
		s.renderLogin(w, r, "/", tr(lang, "form.dashboard_locked"))
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderLogin(w, r, "/", tr(lang, "form.read_failed"))
		return
	}
	ip := s.clientIP(r)
	now := s.now()
	user := r.PostFormValue("user")
	pass := r.PostFormValue("password")
	next := safeNext(r.PostFormValue("next"))
	remember := r.PostFormValue("remember") == "1"

	// Brute-force throttle: refuse a locked-out source IP before touching
	// credentials, so a guesser cannot keep trying during the lockout.
	if d := s.loginGuard.Check(ip, now); !d.Allowed {
		s.log.Warn("dashboard login throttled", "ip", ip, "retry_after_s", int(d.RetryAfter.Seconds()))
		s.tooManyLogins(w, r, next, d.RetryAfter)
		return
	}

	operator, ok := s.operators.Authenticate(user, pass, now)
	if !ok {
		d := s.loginGuard.Fail(ip, now)
		if !d.Allowed {
			s.log.Warn("dashboard login locked out", "ip", ip, "retry_after_s", int(d.RetryAfter.Seconds()))
			s.tooManyLogins(w, r, next, d.RetryAfter)
			return
		}
		// Generic message: do not confirm whether the username exists.
		s.log.Warn("failed dashboard login", "ip", ip, "remaining", d.Remaining)
		s.renderLoginStatus(w, r, http.StatusUnauthorized, next, tr(lang, "form.invalid_login"))
		return
	}

	s.loginGuard.Reset(ip)
	ttl := sessionTTL
	if remember {
		ttl = rememberSessionTTL
	}
	token, exp, err := s.sessions.create(operator.Username, ttl)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "session_error", "could not create session")
		return
	}
	s.setSessionCookie(w, token, exp)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// tooManyLogins renders the login page with 429 and a Retry-After hint, without
// leaking whether the credentials were otherwise valid.
func (s *Server) tooManyLogins(w http.ResponseWriter, r *http.Request, next string, retryAfter time.Duration) {
	secs := int(retryAfter.Seconds())
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	s.renderLoginStatus(w, r, http.StatusTooManyRequests, next, tr(requestLang(r), "form.too_many_logins"))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.destroy(c.Value)
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, next, errMsg string) {
	s.renderLoginStatus(w, r, http.StatusOK, next, errMsg)
}

// renderLoginStatus renders the login page with an explicit status code so a
// rejected (401) or throttled (429) attempt carries the right semantics.
func (s *Server) renderLoginStatus(w http.ResponseWriter, r *http.Request, status int, next, errMsg string) {
	lang := "en"
	current := "/login"
	if r != nil {
		lang = requestLang(r)
		current = r.URL.RequestURI()
	}
	s.renderStatus(w, status, "login", loginPage{
		Title:       tr(lang, "page.sign_in"),
		Version:     version.Version,
		Next:        next,
		Error:       errMsg,
		Configured:  s.operators.Configured(),
		DevHint:     s.cfg.Dev,
		Lang:        lang,
		CurrentPath: current,
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

// --- shared JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"ok": false, "code": code, "error": msg})
}
