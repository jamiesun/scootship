package config

import (
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestFromEnvDefaults(t *testing.T) {
	cfg := FromEnv(env(nil))

	if cfg.Addr != ":8080" {
		t.Fatalf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.DataDir != "./data" {
		t.Fatalf("DataDir = %q, want ./data", cfg.DataDir)
	}
	if cfg.AdminUser != "admin" {
		t.Fatalf("AdminUser = %q, want admin", cfg.AdminUser)
	}
	if cfg.Dev {
		t.Fatal("Dev should default false")
	}
	if cfg.StaleSeconds != 90 {
		t.Fatalf("StaleSeconds = %d, want 90", cfg.StaleSeconds)
	}
	if cfg.MaxTelemetryByte != 8*1024*1024 {
		t.Fatalf("MaxTelemetryByte = %d, want 8MiB", cfg.MaxTelemetryByte)
	}
	if cfg.AuditRetentionEvents != 1000 {
		t.Fatalf("AuditRetentionEvents = %d, want 1000", cfg.AuditRetentionEvents)
	}
	if cfg.LoginMaxFails != 5 || cfg.LoginWindow != 15*time.Minute || cfg.LoginLockout != 15*time.Minute {
		t.Fatalf("unexpected login guard defaults: %+v", cfg)
	}
	if cfg.TLSEnabled() {
		t.Fatal("TLS should be disabled without both cert and key")
	}
	if cfg.BehindTLSProxy {
		t.Fatal("BehindTLSProxy should default false")
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Fatalf("TrustedProxies = %v, want empty", cfg.TrustedProxies)
	}
}

func TestFromEnvOverrides(t *testing.T) {
	cfg := FromEnv(env(map[string]string{
		"SCOOTSHIP_ADDR":                   "127.0.0.1:9090",
		"SCOOTSHIP_TLS_CERT":               "cert.pem",
		"SCOOTSHIP_TLS_KEY":                "key.pem",
		"SCOOTSHIP_BEHIND_TLS_PROXY":       "1",
		"SCOOTSHIP_DATA_DIR":               "/var/lib/scootship",
		"SCOOTSHIP_ADMIN_USER":             "operator",
		"SCOOTSHIP_ADMIN_PASSWORD":         "secret",
		"SCOOTSHIP_NODE_TOKENS_FILE":       "/run/scootship/tokens.json",
		"SCOOTSHIP_NODE_TOKENS":            "n-1=t-1",
		"SCOOTSHIP_DEV":                    "yes",
		"SCOOTSHIP_STALE_SECONDS":          "45",
		"SCOOTSHIP_MAX_TELEMETRY_BYTES":    "1024",
		"SCOOTSHIP_AUDIT_RETENTION_EVENTS": "250",
		"SCOOTSHIP_LOGIN_MAX_FAILS":        "7",
		"SCOOTSHIP_LOGIN_WINDOW_SECONDS":   "60",
		"SCOOTSHIP_LOGIN_LOCKOUT_SECONDS":  "120",
		"SCOOTSHIP_TRUSTED_PROXIES":        "10.0.0.0/8, 192.168.1.10, bad-entry",
	}))

	if cfg.Addr != "127.0.0.1:9090" || cfg.DataDir != "/var/lib/scootship" {
		t.Fatalf("basic overrides not applied: %+v", cfg)
	}
	if !cfg.TLSEnabled() {
		t.Fatal("TLS should be enabled when both cert and key are set")
	}
	if !cfg.BehindTLSProxy {
		t.Fatal("BehindTLSProxy should be enabled")
	}
	if cfg.AdminUser != "operator" || cfg.AdminPassword != "secret" {
		t.Fatalf("admin overrides not applied: %+v", cfg)
	}
	if cfg.NodeTokensFile != "/run/scootship/tokens.json" || cfg.NodeTokensInline != "n-1=t-1" {
		t.Fatalf("node token overrides not applied: %+v", cfg)
	}
	if !cfg.Dev || cfg.StaleSeconds != 45 || cfg.MaxTelemetryByte != 1024 || cfg.AuditRetentionEvents != 250 {
		t.Fatalf("runtime overrides not applied: %+v", cfg)
	}
	if cfg.LoginMaxFails != 7 || cfg.LoginWindow != time.Minute || cfg.LoginLockout != 2*time.Minute {
		t.Fatalf("login guard overrides not applied: %+v", cfg)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Fatalf("TrustedProxies len = %d, want 2 (%v)", len(cfg.TrustedProxies), cfg.TrustedProxies)
	}
	if got := cfg.TrustedProxies[0].String(); got != "10.0.0.0/8" {
		t.Fatalf("first proxy = %q, want 10.0.0.0/8", got)
	}
	if got := cfg.TrustedProxies[1].String(); got != "192.168.1.10/32" {
		t.Fatalf("second proxy = %q, want 192.168.1.10/32", got)
	}
}

func TestFromEnvFallsBackOnInvalidIntegers(t *testing.T) {
	cfg := FromEnv(env(map[string]string{
		"SCOOTSHIP_STALE_SECONDS":          "not-a-number",
		"SCOOTSHIP_MAX_TELEMETRY_BYTES":    "bad",
		"SCOOTSHIP_AUDIT_RETENTION_EVENTS": "0",
		"SCOOTSHIP_LOGIN_MAX_FAILS":        "bad",
		"SCOOTSHIP_LOGIN_WINDOW_SECONDS":   "bad",
		"SCOOTSHIP_LOGIN_LOCKOUT_SECONDS":  "bad",
	}))

	if cfg.StaleSeconds != 90 || cfg.MaxTelemetryByte != 8*1024*1024 || cfg.AuditRetentionEvents != 1000 || cfg.LoginMaxFails != 5 {
		t.Fatalf("integer defaults not preserved: %+v", cfg)
	}
	if cfg.LoginWindow != 15*time.Minute || cfg.LoginLockout != 15*time.Minute {
		t.Fatalf("duration defaults not preserved: %+v", cfg)
	}
}
