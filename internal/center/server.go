// Package center implements the scoot-edge management center: the server side of
// the EDGE.md contract plus an embedded admin dashboard.
//
// The center is the only inbound trusted surface in the fleet (the edge opens no
// listener and only dials out), so every node endpoint is authenticated by a
// per-node bearer token and every dashboard route by a login session. Telemetry
// is append-only and is never reflected back to a node's local state.
package center

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/jamiesun/scootship/internal/config"
	"github.com/jamiesun/scootship/internal/loginguard"
	"github.com/jamiesun/scootship/internal/operators"
	"github.com/jamiesun/scootship/internal/store"
	"github.com/jamiesun/scootship/internal/tokens"
	"github.com/jamiesun/scootship/internal/version"
	"github.com/jamiesun/scootship/internal/web"
)

// Server is the management center.
type Server struct {
	cfg        config.Config
	store      store.Store
	tokens     *tokens.Registry
	operators  *operators.Store
	sessions   *sessionStore
	loginGuard *loginguard.Guard
	tmpl       *template.Template
	log        *slog.Logger
	now        func() time.Time
}

// sessionTTL is how long a dashboard login stays valid.
const (
	// sessionTTL is the standard dashboard login duration.
	sessionTTL = 12 * time.Hour
	// rememberSessionTTL is used only when an operator explicitly selects
	// "remember this device". This remembers the login session, never the password.
	rememberSessionTTL = 30 * 24 * time.Hour
)

// New builds a center server. The dashboard templates are parsed once from the
// embedded filesystem.
func New(cfg config.Config, st store.Store, tk *tokens.Registry, ops *operators.Store, logger *slog.Logger) (*Server, error) {
	tmpl, err := web.Templates(template.FuncMap{
		"trunc":      trunc,
		"initial":    initial,
		"t":          tr,
		"tf":         trf,
		"langURL":    langURL,
		"pathEscape": url.PathEscape,
	})
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:       cfg,
		store:     st,
		tokens:    tk,
		operators: ops,
		sessions:  newSessionStore(sessionTTL),
		loginGuard: loginguard.New(loginguard.Options{
			MaxFails: cfg.LoginMaxFails,
			Window:   cfg.LoginWindow,
			Lockout:  cfg.LoginLockout,
		}),
		tmpl: tmpl,
		log:  logger,
		now:  time.Now,
	}, nil
}

// Handler wires the routes. Node-facing routes use bearer auth; dashboard routes
// use login-session auth; health and static assets are open.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Edge-facing (per-node bearer token).
	mux.Handle("POST /telemetry", s.requireNode(http.HandlerFunc(s.handleTelemetry)))
	mux.Handle("GET /jobs/lease", s.requireNode(http.HandlerFunc(s.handleLease)))

	// Open liveness + embedded static assets (CSS/JS only) + login.
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(web.Static())))
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)

	// Dashboard + JSON API (login-session auth).
	mux.Handle("GET /{$}", s.requireAdmin(http.HandlerFunc(s.handleFleet)))
	mux.Handle("GET /nodes/{id}", s.requireAdmin(http.HandlerFunc(s.handleNode)))
	mux.Handle("GET /tokens", s.requireAdmin(http.HandlerFunc(s.handleTokens)))
	mux.Handle("POST /tokens", s.requireAdmin(http.HandlerFunc(s.handleTokenCreate)))
	mux.Handle("POST /tokens/{id}/rotate", s.requireAdmin(http.HandlerFunc(s.handleTokenRotate)))
	mux.Handle("POST /tokens/{id}/revoke", s.requireAdmin(http.HandlerFunc(s.handleTokenRevoke)))
	mux.Handle("GET /settings", s.requireAdmin(http.HandlerFunc(s.handleSettings)))
	mux.Handle("GET /account", s.requireAdmin(http.HandlerFunc(s.handleAccount)))
	mux.Handle("POST /account", s.requireAdmin(http.HandlerFunc(s.handleAccountUpdate)))
	mux.Handle("POST /account/password", s.requireAdmin(http.HandlerFunc(s.handleAccountPassword)))
	mux.Handle("GET /operators", s.requireAdmin(http.HandlerFunc(s.handleOperators)))
	mux.Handle("GET /operators/new", s.requireAdmin(http.HandlerFunc(s.handleOperatorCreatePage)))
	mux.Handle("POST /operators", s.requireAdmin(http.HandlerFunc(s.handleOperatorCreate)))
	mux.Handle("GET /operators/{username}", s.requireAdmin(http.HandlerFunc(s.handleOperatorEdit)))
	mux.Handle("POST /operators/{username}", s.requireAdmin(http.HandlerFunc(s.handleOperatorUpdate)))
	mux.Handle("GET /api/fleet", s.requireAdmin(http.HandlerFunc(s.handleAPIFleet)))
	mux.Handle("GET /api/nodes/{id}", s.requireAdmin(http.HandlerFunc(s.handleAPINode)))
	mux.Handle("GET /api/tokens", s.requireAdmin(http.HandlerFunc(s.handleAPITokens)))
	mux.Handle("GET /api/operators", s.requireAdmin(http.HandlerFunc(s.handleAPIOperators)))

	return s.securityHeaders(s.recoverPanic(s.logRequests(languageMiddleware(mux))))
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if s.cfg.TLSEnabled() {
			errCh <- srv.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey)
		} else {
			errCh <- srv.ListenAndServe()
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "scootship",
		"version": version.Version,
	})
}

// --- middleware ---

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// Never log the Authorization header or query secrets.
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"dur_ms", time.Since(start).Milliseconds(),
		)
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic recovered", "path", r.URL.Path, "panic", v)
				writeJSONError(w, http.StatusInternalServerError, "internal", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeaders applies defensive response headers to every response. The CSP
// is strict because the dashboard ships only self-hosted assets with no inline
// scripts or styles.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; img-src 'self' data:; style-src 'self'; " +
		"script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}
