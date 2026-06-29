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
	Title   string
	Version string
	User    string // logged-in operator (for the sidebar)
	Active  string // active sidebar item key
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
}

type fleetPage struct {
	basePage
	Addr   string
	Total  int
	Online int
	Stale  int
	Nodes  []nodeRow
}

type nodePage struct {
	basePage
	Node        store.NodeView
	Online      bool
	LastSeenAgo string
	Audits      []store.StoredAudit
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
}

func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	nodes := s.store.Nodes()
	page := fleetPage{
		basePage: basePage{Title: "Fleet", Version: version.Version, User: user, Active: "fleet"},
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
			LastSeenAgo:     s.ago(n.LastSeenMS),
		}
		if n.Descriptor != nil {
			row.Labels = n.Descriptor.Labels
		}
		page.Nodes = append(page.Nodes, row)
	}
	s.render(w, "fleet", page)
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	id := r.PathValue("id")
	n, ok := s.store.Node(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not_found", "unknown node")
		return
	}
	page := nodePage{
		basePage:    basePage{Title: "node " + id, Version: version.Version, User: user, Active: "fleet"},
		Node:        n,
		Online:      s.online(n.LastSeenMS),
		LastSeenAgo: s.ago(n.LastSeenMS),
		Audits:      s.store.AuditEvents(id, 100),
	}
	s.render(w, "node", page)
}

func (s *Server) handleAPIFleet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"now_ms": s.now().UnixMilli(),
		"nodes":  s.store.Nodes(),
	})
}

func (s *Server) handleAPINode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, ok := s.store.Node(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not_found", "unknown node")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"now_ms": s.now().UnixMilli(),
		"online": s.online(n.LastSeenMS),
		"node":   n,
		"audits": s.store.AuditEvents(id, 100),
	})
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	rows := s.tokenRows()
	page := tokensPage{
		basePage: basePage{Title: "Tokens", Version: version.Version, User: user, Active: "tokens"},
		Total:    len(rows),
		Rows:     rows,
	}
	for _, row := range rows {
		if row.LastAuthenticatedMS != 0 {
			page.Authenticated++
		}
		if row.KnownNode {
			page.KnownNodes++
		}
	}
	s.render(w, "tokens", page)
}

func (s *Server) handleAPITokens(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"now_ms": s.now().UnixMilli(),
		"tokens": s.tokenRows(),
	})
}

func (s *Server) tokenRows() []tokenRow {
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
			LastAuthenticatedAgo: "never",
			LastSeenAgo:          "never",
		}
		if snap.LastAuthenticatedMS != 0 {
			row.LastAuthenticatedAgo = s.ago(snap.LastAuthenticatedMS)
		}
		if n, ok := nodes[snap.NodeID]; ok {
			row.KnownNode = true
			row.Online = s.online(n.LastSeenMS)
			row.LastSeenAgo = s.ago(n.LastSeenMS)
		}
		rows = append(rows, row)
	}
	return rows
}

// --- view helpers ---

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
	if ms == 0 {
		return "never"
	}
	d := s.now().Sub(time.UnixMilli(ms))
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
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
