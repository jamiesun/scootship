// Command scootship is the scoot-edge management center: it ingests append-only
// fleet telemetry over the EDGE.md v1 contract and serves an embedded admin
// dashboard, all from a single binary.
//
// Subcommands:
//
//	scootship serve        run the center (env-configured)
//	scootship mock-edge    run a simulated node against a center (dev/test)
//	scootship version      print the version
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jamiesun/scootship/internal/center"
	"github.com/jamiesun/scootship/internal/config"
	"github.com/jamiesun/scootship/internal/mockedge"
	"github.com/jamiesun/scootship/internal/operators"
	"github.com/jamiesun/scootship/internal/store"
	"github.com/jamiesun/scootship/internal/tokens"
	"github.com/jamiesun/scootship/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	switch os.Args[1] {
	case "serve":
		if err := runServe(logger); err != nil {
			logger.Error("serve exited", "err", err)
			os.Exit(1)
		}
	case "mock-edge":
		if err := runMockEdge(logger, os.Args[2:]); err != nil {
			logger.Error("mock-edge exited", "err", err)
			os.Exit(1)
		}
	case "version", "-v", "--version":
		fmt.Println("scootship", version.Version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `scootship `+version.Version+` — scoot-edge management center

Usage:
  scootship serve        run the center (configured via SCOOTSHIP_* env vars)
  scootship mock-edge    simulate a scoot-edge node dialing a center
  scootship version      print version

serve environment:
  SCOOTSHIP_ADDR                 listen address (default ":8080")
  SCOOTSHIP_TLS_CERT             PEM cert path (HTTPS direct mode)
  SCOOTSHIP_TLS_KEY              PEM key path
  SCOOTSHIP_BEHIND_TLS_PROXY     =1 allows plain HTTP listener only behind trusted TLS termination
  SCOOTSHIP_DATA_DIR             append-only store dir (default "./data")
  SCOOTSHIP_ADMIN_USER           first operator username when store is empty (default "admin")
  SCOOTSHIP_ADMIN_PASSWORD       first operator password (required for bootstrap unless SCOOTSHIP_DEV=1)
  SCOOTSHIP_NODE_TOKENS_FILE     private JSON file of node_id -> token
  SCOOTSHIP_NODE_TOKENS          inline "node=token,node2=token2"
  SCOOTSHIP_DEV                  =1 seeds a demo node token and a default admin/admin login
  SCOOTSHIP_STALE_SECONDS        node goes stale after N seconds (default 90)
  SCOOTSHIP_LOGIN_MAX_FAILS      failed logins per source IP before lockout (default 5)
  SCOOTSHIP_LOGIN_WINDOW_SECONDS sliding window for counting failures (default 900)
  SCOOTSHIP_LOGIN_LOCKOUT_SECONDS lockout duration once tripped (default 900)
  SCOOTSHIP_TRUSTED_PROXIES      CIDRs/IPs whose X-Forwarded-For is trusted (default none)

mock-edge flags:
  -center URL   center base URL (default "http://localhost:8080")
  -node ID      node id (default "n-dev")
  -token TOKEN  per-node bearer token (default "dev-token")
  -interval D   heartbeat interval (default 10s)
  -ship-audit   also ship synthetic audit batches
`)
}

func runServe(logger *slog.Logger) error {
	cfg := config.FromEnv(config.Getenv)

	// Dev convenience: enable the login with a default password so the dashboard
	// is reachable locally without provisioning secrets. Be loud about it.
	if cfg.AdminPassword == "" && cfg.Dev {
		cfg.AdminPassword = "admin"
		logger.Warn("SCOOTSHIP_DEV: dashboard login enabled with default admin/admin (insecure; dev only)")
	}
	if err := validateServeConfig(cfg); err != nil {
		return err
	}

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	registry, err := resolveTokens(cfg, logger)
	if err != nil {
		return fmt.Errorf("load node tokens: %w", err)
	}
	ops, err := operators.Open(cfg.DataDir, cfg.AdminUser, cfg.AdminPassword, time.Now())
	if err != nil {
		return fmt.Errorf("open operators: %w", err)
	}

	srv, err := center.New(cfg, st, registry, ops, logger)
	if err != nil {
		return fmt.Errorf("build server: %w", err)
	}

	// Startup posture: be loud about insecure-but-convenient dev settings so an
	// operator never mistakes a dev center for a hardened one.
	logger.Info("scootship center starting",
		"addr", cfg.Addr,
		"transport", transportMode(cfg),
		"tls", cfg.TLSEnabled(),
		"data_dir", cfg.DataDir,
		"node_tokens", registry.Count(),
		"operators", ops.Count(),
		"login_max_fails", cfg.LoginMaxFails,
		"login_lockout_s", int(cfg.LoginLockout.Seconds()),
		"trusted_proxies", len(cfg.TrustedProxies),
	)
	if !cfg.TLSEnabled() {
		if cfg.Dev {
			logger.Warn("serving plain HTTP because SCOOTSHIP_DEV=1 (insecure; local use only)")
		} else {
			logger.Warn("serving plain HTTP behind trusted TLS termination; ensure the listener is not directly exposed", "trusted_proxies", len(cfg.TrustedProxies))
		}
	}
	if !ops.Configured() {
		logger.Warn("dashboard is locked: set SCOOTSHIP_ADMIN_PASSWORD to bootstrap the first operator (or SCOOTSHIP_DEV=1 for local use)")
	}
	if registry.Empty() {
		logger.Warn("no node tokens configured: no edge can authenticate until you set SCOOTSHIP_NODE_TOKENS(_FILE)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return srv.Run(ctx)
}

func validateServeConfig(cfg config.Config) error {
	if (cfg.TLSCert == "") != (cfg.TLSKey == "") {
		return fmt.Errorf("SCOOTSHIP_TLS_CERT and SCOOTSHIP_TLS_KEY must be set together")
	}
	if cfg.TLSEnabled() || cfg.Dev || cfg.BehindTLSProxy {
		return nil
	}
	return fmt.Errorf("plain HTTP is disabled outside dev mode: set SCOOTSHIP_TLS_CERT/KEY for direct HTTPS, set SCOOTSHIP_BEHIND_TLS_PROXY=1 when a trusted reverse proxy terminates TLS, or set SCOOTSHIP_DEV=1 for local development")
}

func transportMode(cfg config.Config) string {
	switch {
	case cfg.TLSEnabled():
		return "https"
	case cfg.Dev:
		return "http-dev"
	case cfg.BehindTLSProxy:
		return "http-behind-tls-proxy"
	default:
		return "invalid-plain-http"
	}
}

func resolveTokens(cfg config.Config, logger *slog.Logger) (*tokens.Registry, error) {
	var entries []tokens.Entry
	if cfg.NodeTokensFile != "" {
		fileEntries, err := tokens.LoadFileEntries(cfg.NodeTokensFile)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	entries = append(entries, tokens.ParseInlineEntries(cfg.NodeTokensInline)...)
	if len(entries) == 0 && cfg.Dev {
		entries = append(entries, tokens.Entry{NodeID: "n-dev", Token: "dev-token", Source: tokens.SourceDev})
		logger.Warn("SCOOTSHIP_DEV: seeded demo node token n-dev=dev-token (insecure; dev only)")
	}
	return tokens.NewEntries(entries), nil
}

func runMockEdge(logger *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("mock-edge", flag.ContinueOnError)
	center := fs.String("center", "http://localhost:8080", "center base URL")
	node := fs.String("node", "n-dev", "node id")
	token := fs.String("token", "dev-token", "per-node bearer token")
	interval := fs.Duration("interval", 10*time.Second, "heartbeat interval")
	shipAudit := fs.Bool("ship-audit", false, "also ship synthetic audit batches")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return mockedge.Run(ctx, mockedge.Options{
		Center:    *center,
		NodeID:    *node,
		Token:     *token,
		Interval:  *interval,
		ShipAudit: *shipAudit,
		Logger:    logger,
	})
}
