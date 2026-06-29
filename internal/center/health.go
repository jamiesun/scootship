package center

import (
	"fmt"

	"github.com/jamiesun/scootship/internal/protocol"
	"github.com/jamiesun/scootship/internal/store"
)

type healthSignal struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
}

type healthContext struct {
	ScootBaseline string
	ScootDrift    bool
	EdgeBaseline  string
	EdgeDrift     bool
}

func buildHealthContext(nodes []store.NodeView) healthContext {
	scootBaseline, scootDrift := versionBaseline(nodes, func(n store.NodeView) string { return n.ScootVersion })
	edgeBaseline, edgeDrift := versionBaseline(nodes, func(n store.NodeView) string { return n.EdgeVersion })
	return healthContext{
		ScootBaseline: scootBaseline,
		ScootDrift:    scootDrift,
		EdgeBaseline:  edgeBaseline,
		EdgeDrift:     edgeDrift,
	}
}

func versionBaseline(nodes []store.NodeView, version func(store.NodeView) string) (string, bool) {
	counts := map[string]int{}
	for _, n := range nodes {
		v := version(n)
		if v != "" {
			counts[v]++
		}
	}
	if len(counts) < 2 {
		return "", false
	}
	var best string
	var bestCount int
	for v, n := range counts {
		if n > bestCount || (n == bestCount && (best == "" || v < best)) {
			best = v
			bestCount = n
		}
	}
	return best, true
}

func (s *Server) healthByNode(lang string, nodes []store.NodeView) map[string][]healthSignal {
	ctx := buildHealthContext(nodes)
	out := make(map[string][]healthSignal, len(nodes))
	for _, n := range nodes {
		out[n.NodeID] = s.nodeHealthSignals(lang, n, ctx)
	}
	return out
}

func (s *Server) nodeHealthSignals(lang string, n store.NodeView, ctx healthContext) []healthSignal {
	var out []healthSignal
	if !s.online(n.LastSeenMS) {
		out = append(out, healthSignal{
			Code:     "offline",
			Severity: "danger",
			Title:    tr(lang, "health.offline"),
			Detail:   trf(lang, "health.offline_detail", s.agoLang(lang, n.LastSeenMS), s.cfg.StaleSeconds),
		})
	}
	if ctx.ScootDrift && n.ScootVersion != "" && n.ScootVersion != ctx.ScootBaseline {
		out = append(out, healthSignal{
			Code:     "scoot_version_drift",
			Severity: "warn",
			Title:    tr(lang, "health.scoot_version_drift"),
			Detail:   trf(lang, "health.version_drift_detail", n.ScootVersion, ctx.ScootBaseline),
		})
	}
	if ctx.EdgeDrift && n.EdgeVersion != "" && n.EdgeVersion != ctx.EdgeBaseline {
		out = append(out, healthSignal{
			Code:     "edge_version_drift",
			Severity: "warn",
			Title:    tr(lang, "health.edge_version_drift"),
			Detail:   trf(lang, "health.version_drift_detail", n.EdgeVersion, ctx.EdgeBaseline),
		})
	}
	if n.PolicyCeiling == "unrestricted" {
		out = append(out, healthSignal{
			Code:     "unrestricted_ceiling",
			Severity: "warn",
			Title:    tr(lang, "health.unrestricted_ceiling"),
			Detail:   tr(lang, "health.unrestricted_ceiling_detail"),
		})
	}
	if n.AuditStats.PolicyDeny > 0 {
		out = append(out, healthSignal{
			Code:     "policy_deny",
			Severity: "warn",
			Title:    tr(lang, "health.policy_deny"),
			Detail:   trf(lang, "health.audit_count_detail", n.AuditStats.PolicyDeny),
		})
	}
	if n.AuditStats.SystemError > 0 {
		out = append(out, healthSignal{
			Code:     "system_error",
			Severity: "danger",
			Title:    tr(lang, "health.system_error"),
			Detail:   trf(lang, "health.audit_count_detail", n.AuditStats.SystemError),
		})
	}
	if n.AuditLifecycle.GapCount > 0 {
		out = append(out, healthSignal{
			Code:     "audit_gap",
			Severity: "warn",
			Title:    tr(lang, "health.audit_gap"),
			Detail:   trf(lang, "health.audit_gap_detail", n.AuditLifecycle.GapCount, n.AuditLifecycle.DroppedEvents),
		})
	}
	if n.AuditLifecycle.DuplicateReports > 0 {
		out = append(out, healthSignal{
			Code:     "duplicate_audit_report",
			Severity: "warn",
			Title:    tr(lang, "health.duplicate_audit_report"),
			Detail:   trf(lang, "health.duplicate_audit_report_detail", n.AuditLifecycle.DuplicateReports),
		})
	}
	totalAudit := auditStatsTotal(n.AuditStats)
	if totalAudit > uint64(n.AuditStored) {
		out = append(out, healthSignal{
			Code:     "audit_body_lag",
			Severity: "warn",
			Title:    tr(lang, "health.audit_body_lag"),
			Detail:   trf(lang, "health.audit_body_lag_detail", totalAudit-uint64(n.AuditStored), totalAudit, n.AuditStored),
		})
	}
	return out
}

func strongestHealth(signals []healthSignal) string {
	level := "ok"
	for _, sig := range signals {
		if sig.Severity == "danger" {
			return "danger"
		}
		if sig.Severity == "warn" {
			level = "warn"
		}
	}
	return level
}

func auditStatsTotal(stats protocol.AuditStats) uint64 {
	return stats.Run + stats.Thought + stats.ToolCall + stats.Observation + stats.Final + stats.PolicyDeny + stats.SystemError
}

func signalSummary(signals []healthSignal) string {
	if len(signals) == 0 {
		return "ok"
	}
	return fmt.Sprintf("%d", len(signals))
}
