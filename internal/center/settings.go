package center

import (
	"fmt"
	"net/http"

	"github.com/jamiesun/scootship/internal/version"
)

type settingsRow struct {
	Key   string
	Value string
	Warn  bool
}

type settingsPage struct {
	basePage
	Operators int
	Tokens    int
	Nodes     int
	Center    []settingsRow
	Security  []settingsRow
}

// handleSettings renders a read-only settings hub: links to the operator,
// account, and token management pages plus the center's effective runtime
// configuration. Secrets (passwords, bearer tokens) are never shown.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	tls := "disabled"
	if s.cfg.TLSEnabled() {
		tls = "enabled"
	} else if s.cfg.BehindTLSProxy {
		tls = "terminated by proxy"
	}
	page := settingsPage{
		basePage:  basePage{Title: "Settings", Version: version.Version, User: user, Active: "settings"},
		Operators: len(s.operators.List()),
		Tokens:    len(s.tokens.Snapshots()),
		Nodes:     len(s.store.Nodes()),
		Center: []settingsRow{
			{Key: "version", Value: version.Version},
			{Key: "listen address", Value: s.cfg.Addr},
			{Key: "tls", Value: tls, Warn: tls == "disabled"},
			{Key: "data dir", Value: s.cfg.DataDir},
			{Key: "stale after", Value: fmt.Sprintf("%ds", s.cfg.StaleSeconds)},
			{Key: "max telemetry body", Value: humanBytes(s.cfg.MaxTelemetryByte)},
			{Key: "dev mode", Value: onOff(s.cfg.Dev), Warn: s.cfg.Dev},
		},
		Security: []settingsRow{
			{Key: "login max fails", Value: fmt.Sprintf("%d", s.cfg.LoginMaxFails)},
			{Key: "login window", Value: s.cfg.LoginWindow.String()},
			{Key: "login lockout", Value: s.cfg.LoginLockout.String()},
		},
	}
	s.render(w, "settings", page)
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
