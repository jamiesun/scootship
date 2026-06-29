package center

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jamiesun/scootship/internal/store"
	"github.com/jamiesun/scootship/internal/version"
)

type basePage struct {
	Title       string
	Version     string
	User        string // logged-in operator (for the sidebar)
	Active      string // active sidebar item key
	Lang        string
	CurrentPath string
}

type nodeRow struct {
	NodeID          string
	Online          bool
	ScootVersion    string
	EdgeVersion     string
	PolicyCeiling   string
	DaemonState     string
	AuditRun        uint64
	AuditPolicyDeny uint64
	AuditStored     int
	LastSeenAgo     string
	Labels          []string
	HealthSignals   []healthSignal
	HealthLevel     string
	HealthSummary   string
}

type fleetPage struct {
	basePage
	Addr   string
	Total  int
	Online int
	Stale  int
	Health int
	Nodes  []nodeRow
}

type nodePage struct {
	basePage
	Node          store.NodeView
	Online        bool
	LastSeenAgo   string
	AuditGapAgo   string
	HealthSignals []healthSignal
	HealthLevel   string
	Timelines     []store.AuditTimeline
	Audits        []store.StoredAudit
}

type tokenRow struct {
	NodeID               string `json:"node_id"`
	Source               string `json:"source"`
	Fingerprint          string `json:"fingerprint"`
	Configured           bool   `json:"configured"`
	KnownNode            bool   `json:"known_node"`
	Online               bool   `json:"online"`
	LastAuthenticatedMS  int64  `json:"last_authenticated_ms,omitempty"`
	LastAuthenticatedAgo string `json:"last_authenticated_ago"`
	LastSeenAgo          string `json:"last_seen_ago"`
}

type tokensPage struct {
	basePage
	Total         int
	Authenticated int
	KnownNodes    int
	Rows          []tokenRow
	Manage        formMessage
}

func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	nodes := s.store.Nodes()
	ctx := buildHealthContext(nodes)
	page := fleetPage{
		basePage: s.base(r, user, "fleet", "page.fleet"),
		Addr:     s.cfg.Addr,
		Total:    len(nodes),
	}
	for _, n := range nodes {
		online := s.online(n.LastSeenMS)
		if online {
			page.Online++
		} else {
			page.Stale++
		}
		row := nodeRow{
			NodeID:          n.NodeID,
			Online:          online,
			ScootVersion:    n.ScootVersion,
			EdgeVersion:     n.EdgeVersion,
			PolicyCeiling:   n.PolicyCeiling,
			DaemonState:     n.Daemon.State,
			AuditRun:        n.AuditStats.Run,
			AuditPolicyDeny: n.AuditStats.PolicyDeny,
			AuditStored:     n.AuditStored,
			LastSeenAgo:     s.agoLang(lang, n.LastSeenMS),
		}
		row.HealthSignals = s.nodeHealthSignals(lang, n, ctx)
		row.HealthLevel = strongestHealth(row.HealthSignals)
		row.HealthSummary = signalSummary(row.HealthSignals)
		page.Health += len(row.HealthSignals)
		if n.Descriptor != nil {
			row.Labels = n.Descriptor.Labels
		}
		page.Nodes = append(page.Nodes, row)
	}
	s.render(w, "fleet", page)
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	lang := requestLang(r)
	id := r.PathValue("id")
	n, ok := s.store.Node(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not_found", "unknown node")
		return
	}
	ctx := buildHealthContext(s.store.Nodes())
	signals := s.nodeHealthSignals(lang, n, ctx)
	page := nodePage{
		basePage:      s.baseTitle(r, user, "fleet", tr(lang, "fleet.node")+" "+id),
		Node:          n,
		Online:        s.online(n.LastSeenMS),
		LastSeenAgo:   s.agoLang(lang, n.LastSeenMS),
		AuditGapAgo:   s.agoLang(lang, n.AuditLifecycle.LastGapRecvMS),
		HealthSignals: signals,
		HealthLevel:   strongestHealth(signals),
		Timelines:     s.store.AuditTimelines(id, 25),
		Audits:        s.store.AuditEvents(id, 100),
	}
	s.render(w, "node", page)
}

func (s *Server) handleAPIFleet(w http.ResponseWriter, _ *http.Request) {
	nodes := s.store.Nodes()
	writeJSON(w, http.StatusOK, map[string]any{
		"now_ms": s.now().UnixMilli(),
		"nodes":  nodes,
		"health": s.healthByNode("en", nodes),
	})
}

func (s *Server) handleAPINode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, ok := s.store.Node(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not_found", "unknown node")
		return
	}
	ctx := buildHealthContext(s.store.Nodes())
	writeJSON(w, http.StatusOK, map[string]any{
		"now_ms":    s.now().UnixMilli(),
		"online":    s.online(n.LastSeenMS),
		"node":      n,
		"health":    s.nodeHealthSignals("en", n, ctx),
		"audits":    s.store.AuditEvents(id, 100),
		"timelines": s.store.AuditTimelines(id, 25),
	})
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	page := s.tokensPage(r, user, formMessage{})
	s.render(w, "tokens", page)
}

func (s *Server) tokensPage(r *http.Request, user string, msg formMessage) tokensPage {
	lang := requestLang(r)
	rows := s.tokenRows(lang)
	page := tokensPage{
		basePage: s.base(r, user, "tokens", "page.tokens"),
		Total:    len(rows),
		Rows:     rows,
		Manage:   msg,
	}
	for _, row := range rows {
		if row.LastAuthenticatedMS != 0 {
			page.Authenticated++
		}
		if row.KnownNode {
			page.KnownNodes++
		}
	}
	return page
}

func (s *Server) handleAPITokens(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"now_ms": s.now().UnixMilli(),
		"tokens": s.tokenRows("en"),
	})
}

func (s *Server) tokenRows(lang string) []tokenRow {
	nodes := map[string]store.NodeView{}
	for _, n := range s.store.Nodes() {
		nodes[n.NodeID] = n
	}
	snaps := s.tokens.Snapshots()
	rows := make([]tokenRow, 0, len(snaps))
	for _, snap := range snaps {
		row := tokenRow{
			NodeID:               snap.NodeID,
			Source:               snap.Source,
			Fingerprint:          snap.Fingerprint,
			Configured:           true,
			LastAuthenticatedMS:  snap.LastAuthenticatedMS,
			LastAuthenticatedAgo: tr(lang, "common.never"),
			LastSeenAgo:          tr(lang, "common.never"),
		}
		if snap.LastAuthenticatedMS != 0 {
			row.LastAuthenticatedAgo = s.agoLang(lang, snap.LastAuthenticatedMS)
		}
		if n, ok := nodes[snap.NodeID]; ok {
			row.KnownNode = true
			row.Online = s.online(n.LastSeenMS)
			row.LastSeenAgo = s.agoLang(lang, n.LastSeenMS)
		}
		rows = append(rows, row)
	}
	return rows
}

// --- view helpers ---

func (s *Server) base(r *http.Request, user, active, titleKey string) basePage {
	lang := requestLang(r)
	return s.baseTitle(r, user, active, tr(lang, titleKey))
}

func (s *Server) baseTitle(r *http.Request, user, active, title string) basePage {
	lang := requestLang(r)
	return basePage{
		Title:       title,
		Version:     version.Version,
		User:        user,
		Active:      active,
		Lang:        lang,
		CurrentPath: r.URL.RequestURI(),
	}
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	s.renderStatus(w, http.StatusOK, name, data)
}

// renderStatus renders a template to a buffer first so a mid-render template
// error becomes a clean 500 rather than a half-written body, then commits it with
// the given status code (e.g. 401/429 for failed or throttled logins).
func (s *Server) renderStatus(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("template render", "name", name, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "template_error", "failed to render page")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// online reports whether a node has reported within the stale window.
func (s *Server) online(lastSeenMS int64) bool {
	if lastSeenMS == 0 {
		return false
	}
	return s.now().UnixMilli()-lastSeenMS <= int64(s.cfg.StaleSeconds)*1000
}

// ago humanizes how long since a Unix-millis timestamp.
func (s *Server) ago(ms int64) string {
	return s.agoLang("en", ms)
}

func (s *Server) agoLang(lang string, ms int64) string {
	if ms == 0 {
		return tr(lang, "common.never")
	}
	d := s.now().Sub(time.UnixMilli(ms))
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		if lang == "zh" {
			return fmt.Sprintf("%d 秒前", int(d.Seconds()))
		}
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		if lang == "zh" {
			return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
		}
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		if lang == "zh" {
			return fmt.Sprintf("%d 小时前", int(d.Hours()))
		}
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		if lang == "zh" {
			return fmt.Sprintf("%d 天前", int(d.Hours()/24))
		}
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// trunc shortens a string to n runes for display, appending an ellipsis.
func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// initial returns the uppercased first letter of s, for the sidebar avatar.
func initial(s string) string {
	for _, r := range s {
		return strings.ToUpper(string(r))
	}
	return "?"
}
