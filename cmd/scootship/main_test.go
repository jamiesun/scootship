package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamiesun/scootship/internal/config"
	"github.com/jamiesun/scootship/internal/tokens"
)

func TestValidateServeConfigTransport(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Config
		wantErr string
	}{
		{
			name:    "plain http fails closed by default",
			cfg:     config.Config{},
			wantErr: "plain HTTP is disabled",
		},
		{
			name: "direct https is allowed",
			cfg:  config.Config{TLSCert: "cert.pem", TLSKey: "key.pem"},
		},
		{
			name: "dev http is allowed",
			cfg:  config.Config{Dev: true},
		},
		{
			name: "http behind tls proxy is explicit",
			cfg:  config.Config{BehindTLSProxy: true},
		},
		{
			name:    "partial tls config is rejected",
			cfg:     config.Config{TLSCert: "cert.pem"},
			wantErr: "must be set together",
		},
		{
			name:    "partial tls key is rejected",
			cfg:     config.Config{TLSKey: "key.pem"},
			wantErr: "must be set together",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServeConfig(tt.cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateServeConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateServeConfig() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestTransportModeLabels(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{name: "direct https", cfg: config.Config{TLSCert: "cert.pem", TLSKey: "key.pem"}, want: "https"},
		{name: "dev http", cfg: config.Config{Dev: true}, want: "http-dev"},
		{name: "trusted proxy http", cfg: config.Config{BehindTLSProxy: true}, want: "http-behind-tls-proxy"},
		{name: "invalid plain http", cfg: config.Config{}, want: "invalid-plain-http"},
		{
			name: "direct https wins over other flags",
			cfg:  config.Config{TLSCert: "cert.pem", TLSKey: "key.pem", Dev: true, BehindTLSProxy: true},
			want: "https",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := transportMode(tt.cfg); got != tt.want {
				t.Fatalf("transportMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveTokensRequiresPrivateStaticTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-tokens.json")
	if err := os.WriteFile(path, []byte(`{"n-1":"static-token-0001"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := resolveTokens(config.Config{DataDir: t.TempDir(), NodeTokensFile: path}, discardLogger())
	if err == nil || !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("resolveTokens() err = %v, want insecure permissions", err)
	}
}

func TestResolveTokensAppliesManagedLifecycleOverlay(t *testing.T) {
	dir := t.TempDir()
	staticPath := filepath.Join(dir, "node-tokens.json")
	if err := os.WriteFile(staticPath, []byte(`{"n-1":"static-token-0001","n-2":"static-token-0002"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tokens.NewManagedStore(dir).Save(tokens.ManagedState{
		Tokens:  map[string]string{"n-2": "managed-token-0002"},
		Revoked: []string{"n-1"},
	}); err != nil {
		t.Fatal(err)
	}

	registry, err := resolveTokens(config.Config{DataDir: dir, NodeTokensFile: staticPath}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	if node, ok := registry.NodeFor("static-token-0001"); ok || node != "" {
		t.Fatalf("revoked static token authenticated as %q", node)
	}
	if node, ok := registry.NodeFor("static-token-0002"); ok || node != "" {
		t.Fatalf("overridden static token authenticated as %q", node)
	}
	if node, ok := registry.NodeFor("managed-token-0002"); !ok || node != "n-2" {
		t.Fatalf("managed token = %q %v, want n-2 true", node, ok)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
