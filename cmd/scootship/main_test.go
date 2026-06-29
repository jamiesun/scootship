package main

import (
	"strings"
	"testing"

	"github.com/jamiesun/scootship/internal/config"
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
