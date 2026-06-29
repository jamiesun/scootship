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
	Audit     []settingsRow
	Security  []settingsRow
}

// handleSettings renders a read-only settings hub: links to the operator,
// account, and token management pages plus the center's effective runtime
// configuration. Secrets (passwords, bearer tokens) are never shown.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	tls := tr(lang, "settings.tls_disabled")
	if s.cfg.TLSEnabled() {
		tls = tr(lang, "settings.tls_enabled")
	} else if s.cfg.BehindTLSProxy {
		tls = tr(lang, "settings.tls_proxy")
	}
	page := settingsPage{
		basePage:  s.base(r, user, "settings", "page.settings"),
		Operators: len(s.operators.List()),
		Tokens:    len(s.tokens.Snapshots()),
		Nodes:     len(s.store.Nodes()),
		Center: []settingsRow{
			{Key: tr(lang, "settings.version"), Value: version.Version},
			{Key: tr(lang, "settings.listen_address"), Value: s.cfg.Addr},
			{Key: tr(lang, "settings.tls"), Value: tls, Warn: !s.cfg.TLSEnabled()},
			{Key: tr(lang, "settings.data_dir"), Value: s.cfg.DataDir},
			{Key: tr(lang, "settings.stale_after"), Value: fmt.Sprintf("%ds", s.cfg.StaleSeconds)},
			{Key: tr(lang, "settings.max_telemetry_body"), Value: humanBytes(s.cfg.MaxTelemetryByte)},
			{Key: tr(lang, "settings.dev_mode"), Value: onOff(lang, s.cfg.Dev), Warn: s.cfg.Dev},
		},
		Audit: []settingsRow{
			{Key: tr(lang, "settings.audit_retention_events"), Value: fmt.Sprintf("%d", s.cfg.AuditRetentionEvents)},
			{Key: tr(lang, "settings.audit_durable_store"), Value: tr(lang, "settings.audit_append_only_jsonl")},
			{Key: tr(lang, "settings.audit_gap_behavior"), Value: tr(lang, "settings.audit_gap_explicit")},
		},
		Security: []settingsRow{
			{Key: tr(lang, "settings.login_max_fails"), Value: fmt.Sprintf("%d", s.cfg.LoginMaxFails)},
			{Key: tr(lang, "settings.login_window"), Value: s.cfg.LoginWindow.String()},
			{Key: tr(lang, "settings.login_lockout"), Value: s.cfg.LoginLockout.String()},
		},
	}
	s.render(w, "settings", page)
}

func onOff(lang string, b bool) string {
	if b {
		return tr(lang, "settings.on")
	}
	return tr(lang, "settings.off")
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
