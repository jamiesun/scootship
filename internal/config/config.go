// Package config loads the center's runtime configuration from the environment.
//
// Secrets (node tokens, dashboard password) come from the environment or a
// private file, never from committed config and never compiled into the binary.
package config

import (
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the resolved center configuration.
type Config struct {
	Addr             string // listen address, e.g. ":8080"
	TLSCert          string // PEM cert path; with TLSKey enables HTTPS
	TLSKey           string // PEM key path
	BehindTLSProxy   bool   // explicitly allow plain HTTP listener behind trusted TLS termination
	DataDir          string // append-only store directory
	AdminUser        string // first dashboard operator username when the operator store is empty
	AdminPassword    string // first dashboard operator password when the operator store is empty
	NodeTokensFile   string // JSON node_id->token file
	NodeTokensInline string // "node=token,node2=token2"
	Dev              bool   // dev conveniences (seed a demo node token + default login)
	StaleSeconds     int    // a node is "stale" after this many seconds of silence
	MaxTelemetryByte int64  // cap on a single /telemetry request body

	// Login brute-force protection (per source IP).
	LoginMaxFails int
	LoginWindow   time.Duration
	LoginLockout  time.Duration

	// TrustedProxies are reverse proxies whose X-Forwarded-For may be believed
	// when attributing the real client IP. Empty means trust no proxy and use the
	// raw connection address (the safe default).
	TrustedProxies []netip.Prefix
}

// FromEnv resolves configuration from SCOOTSHIP_* environment variables.
func FromEnv(getenv func(string) string) Config {
	return Config{
		Addr:             def(getenv("SCOOTSHIP_ADDR"), ":8080"),
		TLSCert:          getenv("SCOOTSHIP_TLS_CERT"),
		TLSKey:           getenv("SCOOTSHIP_TLS_KEY"),
		BehindTLSProxy:   truthy(getenv("SCOOTSHIP_BEHIND_TLS_PROXY")),
		DataDir:          def(getenv("SCOOTSHIP_DATA_DIR"), "./data"),
		AdminUser:        def(getenv("SCOOTSHIP_ADMIN_USER"), "admin"),
		AdminPassword:    getenv("SCOOTSHIP_ADMIN_PASSWORD"),
		NodeTokensFile:   getenv("SCOOTSHIP_NODE_TOKENS_FILE"),
		NodeTokensInline: getenv("SCOOTSHIP_NODE_TOKENS"),
		Dev:              truthy(getenv("SCOOTSHIP_DEV")),
		StaleSeconds:     intDef(getenv("SCOOTSHIP_STALE_SECONDS"), 90),
		MaxTelemetryByte: int64(intDef(getenv("SCOOTSHIP_MAX_TELEMETRY_BYTES"), 8*1024*1024)),
		LoginMaxFails:    intDef(getenv("SCOOTSHIP_LOGIN_MAX_FAILS"), 5),
		LoginWindow:      time.Duration(intDef(getenv("SCOOTSHIP_LOGIN_WINDOW_SECONDS"), 900)) * time.Second,
		LoginLockout:     time.Duration(intDef(getenv("SCOOTSHIP_LOGIN_LOCKOUT_SECONDS"), 900)) * time.Second,
		TrustedProxies:   parseProxies(getenv("SCOOTSHIP_TRUSTED_PROXIES")),
	}
}

// TLSEnabled reports whether both a cert and key were configured.
func (c Config) TLSEnabled() bool { return c.TLSCert != "" && c.TLSKey != "" }

// parseProxies parses a comma-separated list of CIDRs or bare IPs. Invalid
// entries are skipped.
func parseProxies(s string) []netip.Prefix {
	var out []netip.Prefix
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if p, err := netip.ParsePrefix(item); err == nil {
			out = append(out, p)
			continue
		}
		if a, err := netip.ParseAddr(item); err == nil {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return out
}

func def(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func intDef(v string, fallback int) int {
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func truthy(v string) bool {
	switch v {
	case "1", "true", "TRUE", "yes", "on":
		return true
	default:
		return false
	}
}

// Getenv is os.Getenv, exposed so callers can pass it to FromEnv.
func Getenv(key string) string { return os.Getenv(key) }
