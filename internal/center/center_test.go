package center_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jamiesun/scootship/internal/center"
	"github.com/jamiesun/scootship/internal/config"
	"github.com/jamiesun/scootship/internal/operators"
	"github.com/jamiesun/scootship/internal/protocol"
	"github.com/jamiesun/scootship/internal/store"
	"github.com/jamiesun/scootship/internal/tokens"
)

func defaultCfg() config.Config {
	return config.Config{
		Addr:                 ":0",
		AdminUser:            "admin",
		AdminPassword:        "testpass", // a real login is required for the dashboard
		StaleSeconds:         90,
		MaxTelemetryByte:     8 << 20,
		AuditRetentionEvents: 1000,
	}
}

func newServer(t *testing.T, cfg config.Config) (*httptest.Server, store.Store) {
	t.Helper()
	st, err := store.OpenWithOptions("", store.Options{AuditRetentionEvents: cfg.AuditRetentionEvents}) // in-memory
	if err != nil {
		t.Fatal(err)
	}
	reg := tokens.New(map[string]string{"n-1": "secret"})
	ops, err := operators.Open("", cfg.AdminUser, cfg.AdminPassword, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := center.New(cfg, st, reg, ops, logger)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); st.Close() })
	return ts, st
}

func newTestServer(t *testing.T) (*httptest.Server, store.Store) {
	t.Helper()
	return newServer(t, defaultCfg())
}

// loginClient returns an http.Client with a valid dashboard session.
func loginClient(t *testing.T, base string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar}
	resp, err := c.PostForm(base+"/login", url.Values{
		"user": {"admin"}, "password": {"testpass"}, "next": {"/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// The 303 is followed to "/", which only renders (200) once authenticated.
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/" {
		t.Fatalf("login failed: status=%d path=%s", resp.StatusCode, resp.Request.URL.Path)
	}
	return c
}

func csrfToken(t *testing.T, c *http.Client, base, path string) string {
	t.Helper()
	resp, err := c.Get(base + path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("csrf source %s status=%d body=%s", path, resp.StatusCode, body)
	}
	const marker = `name="csrf_token" value="`
	text := string(body)
	i := strings.Index(text, marker)
	if i < 0 {
		t.Fatalf("missing csrf token in %s: %s", path, body)
	}
	i += len(marker)
	j := strings.Index(text[i:], `"`)
	if j < 0 {
		t.Fatalf("unterminated csrf token in %s", path)
	}
	return text[i : i+j]
}

func revealedTokenSecret(t *testing.T, body string) string {
	t.Helper()
	const marker = `<div class="mono secret-value">`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("missing token secret reveal: %s", body)
	}
	i += len(marker)
	j := strings.Index(body[i:], "</div>")
	if j < 0 {
		t.Fatalf("unterminated token secret reveal: %s", body)
	}
	return body[i : i+j]
}

func allCapabilities() []string {
	return []string{
		string(operators.CapabilityFleetView),
		string(operators.CapabilityTokenManage),
		string(operators.CapabilityOperatorManage),
	}
}

func envBytes(t *testing.T, typ, node string, body any) []byte {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	env := protocol.Envelope{V: 1, Type: typ, NodeID: node, SentTS: 1, Body: raw}
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func postTelemetry(t *testing.T, base, token string, payload []byte) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/telemetry", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, body
}

func TestTelemetryFlow(t *testing.T) {
	ts, _ := newTestServer(t)

	status := protocol.StatusBody{
		ScootVersion:  "0.9.0",
		PolicyCeiling: "readonly",
		Daemon:        protocol.DaemonStatus{State: "running"},
		AuditStats:    protocol.AuditStats{Run: 5, ToolCall: 9},
	}

	// 1) A valid heartbeat registers the node.
	resp, body := postTelemetry(t, ts.URL, "secret", envBytes(t, protocol.TypeStatus, "n-1", status))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat status=%d body=%s", resp.StatusCode, body)
	}

	// 2) The dashboard JSON API (after login) now shows the node.
	client := loginClient(t, ts.URL)
	fleet := getJSON(t, client, ts.URL+"/api/fleet")
	nodes, _ := fleet["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node in fleet, got %v", fleet["nodes"])
	}

	// 3) An audit batch is ingested, and a replay is idempotent.
	batch := protocol.AuditBatchBody{
		Cursor: protocol.Cursor{FileGen: 1, ByteTo: 200, SeqTo: 1},
		Events: []protocol.AuditEvent{{Seq: 0, TS: 1, Kind: "run", Msg: "hello"}},
	}
	resp, body = postTelemetry(t, ts.URL, "secret", envBytes(t, protocol.TypeAuditBatch, "n-1", batch))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", resp.StatusCode, body)
	}
	var ack struct {
		AuditStored int             `json:"audit_stored"`
		Cursor      protocol.Cursor `json:"cursor"`
	}
	if err := json.Unmarshal(body, &ack); err != nil {
		t.Fatalf("decode ack: %v (%s)", err, body)
	}
	if ack.AuditStored != 1 || ack.Cursor.ByteTo != 200 {
		t.Fatalf("unexpected ack: %+v", ack)
	}

	// Replaying the same batch stores nothing and returns the same cursor.
	_, body = postTelemetry(t, ts.URL, "secret", envBytes(t, protocol.TypeAuditBatch, "n-1", batch))
	_ = json.Unmarshal(body, &ack)
	if ack.AuditStored != 0 {
		t.Fatalf("duplicate audit stored %d, want 0", ack.AuditStored)
	}
}

func TestDashboardAuditLifecycleGapVisible(t *testing.T) {
	cfg := defaultCfg()
	cfg.AuditRetentionEvents = 2
	ts, _ := newServer(t, cfg)

	batch := protocol.AuditBatchBody{
		Cursor: protocol.Cursor{FileGen: 1, ByteTo: 300, SeqTo: 3},
		Events: []protocol.AuditEvent{
			{Seq: 0, TS: 1, Kind: "run", Msg: "start"},
			{Seq: 1, TS: 2, Kind: "tool_call", Msg: "grep"},
			{Seq: 2, TS: 3, Kind: "final", Msg: "done"},
		},
	}
	resp, body := postTelemetry(t, ts.URL, "secret", envBytes(t, protocol.TypeAuditBatch, "n-1", batch))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", resp.StatusCode, body)
	}

	client := loginClient(t, ts.URL)
	api := getJSON(t, client, ts.URL+"/api/nodes/n-1")
	node, ok := api["node"].(map[string]any)
	if !ok {
		t.Fatalf("node missing from API: %v", api)
	}
	lifecycle, ok := node["audit_lifecycle"].(map[string]any)
	if !ok {
		t.Fatalf("audit lifecycle missing from API node: %v", node)
	}
	if lifecycle["last_gap_kind"] != "audit_gap" || lifecycle["last_gap_reason"] != "retention_limit" {
		t.Fatalf("gap details not explicit in API: %v", lifecycle)
	}

	resp, err := client.Get(ts.URL + "/nodes/n-1")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	pageText := string(page)
	if !strings.Contains(pageText, "Audit Lifecycle") || !strings.Contains(pageText, "audit_gap") || !strings.Contains(pageText, "retention_limit") {
		t.Fatalf("node page missing English lifecycle gap: %s", pageText)
	}

	resp, err = client.Get(ts.URL + "/settings?lang=zh")
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	settingsText := string(settings)
	if !strings.Contains(settingsText, "审计生命周期") || !strings.Contains(settingsText, "每节点保留事件数") {
		t.Fatalf("settings page missing Chinese audit lifecycle: %s", settingsText)
	}
}

func TestDashboardRunAuditTimelineVisible(t *testing.T) {
	ts, _ := newTestServer(t)
	batch := protocol.AuditBatchBody{
		Cursor: protocol.Cursor{FileGen: 1, ByteTo: 400, SeqTo: 4},
		Events: []protocol.AuditEvent{
			{Seq: 0, TS: 100, SessionID: "s-1", RunID: "r-1", Kind: "run", Msg: "start"},
			{Seq: 1, TS: 110, SessionID: "s-1", RunID: "r-1", Kind: "tool_call", Msg: "grep"},
			{Seq: 2, TS: 120, SessionID: "s-1", RunID: "r-1", Kind: "final", Msg: "done"},
			{Seq: 3, TS: 200, SessionID: "s-2", Kind: "policy_deny", Msg: "blocked"},
		},
	}
	resp, body := postTelemetry(t, ts.URL, "secret", envBytes(t, protocol.TypeAuditBatch, "n-1", batch))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", resp.StatusCode, body)
	}

	client := loginClient(t, ts.URL)
	api := getJSON(t, client, ts.URL+"/api/nodes/n-1")
	timelines, ok := api["timelines"].([]any)
	if !ok || len(timelines) != 2 {
		t.Fatalf("API timelines missing or wrong len: %v", api["timelines"])
	}
	first, ok := timelines[0].(map[string]any)
	if !ok || first["session_id"] != "s-2" {
		t.Fatalf("newest timeline should be s-2: %v", timelines[0])
	}

	resp, err := client.Get(ts.URL + "/nodes/n-1")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	pageText := string(page)
	for _, want := range []string{"Run Audit Timeline", "s-1", "r-1", "seq 0..2", "policy_deny"} {
		if !strings.Contains(pageText, want) {
			t.Fatalf("node page missing %q in run timeline: %s", want, pageText)
		}
	}

	resp, err = client.Get(ts.URL + "/nodes/n-1?lang=zh")
	if err != nil {
		t.Fatal(err)
	}
	zhPage, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(zhPage), "运行审计时间线") || !strings.Contains(string(zhPage), "会话") {
		t.Fatalf("node page missing Chinese run timeline: %s", zhPage)
	}
}

func TestDashboardReadOnlyHealthSignalsVisible(t *testing.T) {
	ts, st := newTestServer(t)
	now := time.Now().UnixMilli()
	status := protocol.StatusBody{
		ScootVersion:  "2.0.0",
		EdgeVersion:   "0.2.0",
		PolicyCeiling: "unrestricted",
		Daemon:        protocol.DaemonStatus{State: "running"},
		AuditStats:    protocol.AuditStats{Run: 5, PolicyDeny: 1, SystemError: 1},
	}
	if err := st.UpsertStatus("n-1", now, now, status); err != nil {
		t.Fatal(err)
	}
	baseline := protocol.StatusBody{ScootVersion: "1.0.0", EdgeVersion: "0.2.0", PolicyCeiling: "readonly"}
	if err := st.UpsertStatus("n-2", now-180_000, now-180_000, baseline); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertStatus("n-3", now, now, baseline); err != nil {
		t.Fatal(err)
	}
	batch := protocol.AuditBatchBody{
		Cursor: protocol.Cursor{FileGen: 1, ByteTo: 100, SeqTo: 1},
		Events: []protocol.AuditEvent{{Seq: 0, TS: 1, Kind: "run", Msg: "start"}},
	}
	if _, _, err := st.IngestAudit("n-1", now+1, batch); err != nil {
		t.Fatal(err)
	}
	if _, n, err := st.IngestAudit("n-1", now+2, batch); err != nil || n != 0 {
		t.Fatalf("duplicate ingest stored=%d err=%v", n, err)
	}

	client := loginClient(t, ts.URL)
	api := getJSON(t, client, ts.URL+"/api/nodes/n-1")
	signals, ok := api["health"].([]any)
	if !ok {
		t.Fatalf("health missing from API: %v", api)
	}
	codes := map[string]bool{}
	for _, item := range signals {
		sig, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("bad health signal shape: %v", item)
		}
		codes[sig["code"].(string)] = true
	}
	for _, want := range []string{"scoot_version_drift", "unrestricted_ceiling", "policy_deny", "system_error", "duplicate_audit_report", "audit_body_lag"} {
		if !codes[want] {
			t.Fatalf("missing health code %q in %v", want, codes)
		}
	}

	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	fleet, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fleetText := string(fleet)
	if !strings.Contains(fleetText, "Health Signals") || !strings.Contains(fleetText, "warn") {
		t.Fatalf("fleet page missing health signal summary: %s", fleetText)
	}

	resp, err = client.Get(ts.URL + "/nodes/n-1")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	pageText := string(page)
	for _, want := range []string{"Health Signals", "Scoot version drift", "Duplicate audit report", "Audit body lag"} {
		if !strings.Contains(pageText, want) {
			t.Fatalf("node page missing health text %q: %s", want, pageText)
		}
	}

	resp, err = client.Get(ts.URL + "/nodes/n-1?lang=zh")
	if err != nil {
		t.Fatal(err)
	}
	zhPage, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	zhText := string(zhPage)
	if !strings.Contains(zhText, "健康信号") || !strings.Contains(zhText, "Scoot 版本漂移") {
		t.Fatalf("node page missing Chinese health signals: %s", zhText)
	}
}

func TestTelemetryAuth(t *testing.T) {
	ts, _ := newTestServer(t)
	status := protocol.StatusBody{ScootVersion: "0.9.0"}

	resp, _ := postTelemetry(t, ts.URL, "", envBytes(t, protocol.TypeStatus, "n-1", status))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token: got %d want 401", resp.StatusCode)
	}

	resp, _ = postTelemetry(t, ts.URL, "nope", envBytes(t, protocol.TypeStatus, "n-1", status))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token: got %d want 401", resp.StatusCode)
	}

	resp, _ = postTelemetry(t, ts.URL, "secret", envBytes(t, protocol.TypeStatus, "n-OTHER", status))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("node mismatch: got %d want 403", resp.StatusCode)
	}
}

func TestTelemetryFailureModesDoNotMutateStore(t *testing.T) {
	tooLargeCfg := defaultCfg()
	tooLargeCfg.MaxTelemetryByte = 32
	tooLargeTS, _ := newServer(t, tooLargeCfg)

	validStatus := envBytes(t, protocol.TypeStatus, "n-1", protocol.StatusBody{ScootVersion: "0.9.0"})
	tooLargeResp, tooLargeBody := postTelemetry(t, tooLargeTS.URL, "secret", validStatus)
	assertJSONError(t, tooLargeResp, tooLargeBody, http.StatusRequestEntityTooLarge, "too_large")

	ts, st := newTestServer(t)
	resp, body := postTelemetry(t, ts.URL, "secret", []byte(`{`))
	assertJSONError(t, resp, body, http.StatusBadRequest, "bad_json")

	resp, body = postTelemetry(t, ts.URL, "secret", nil)
	assertJSONError(t, resp, body, http.StatusBadRequest, "empty")

	badVersion := protocol.Envelope{V: 2, Type: protocol.TypeStatus, NodeID: "n-1", SentTS: 1, Body: json.RawMessage(`{}`)}
	rawBadVersion, err := json.Marshal(badVersion)
	if err != nil {
		t.Fatal(err)
	}
	resp, body = postTelemetry(t, ts.URL, "secret", rawBadVersion)
	assertJSONError(t, resp, body, http.StatusBadRequest, "bad_envelope")

	jobEnvelope := protocol.Envelope{V: 1, Type: protocol.TypeJob, NodeID: "n-1", SentTS: 1, Body: json.RawMessage(`{}`)}
	rawJob, err := json.Marshal(jobEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	resp, body = postTelemetry(t, ts.URL, "secret", rawJob)
	assertJSONError(t, resp, body, http.StatusBadRequest, "wrong_direction")

	// A mixed batch is validated before application; a valid first envelope must
	// not be applied if a later envelope is invalid.
	ts2, st2 := newTestServer(t)
	mixed := append(append([]byte{}, validStatus...), '\n')
	mixed = append(mixed, rawBadVersion...)
	resp, body = postTelemetry(t, ts2.URL, "secret", mixed)
	assertJSONError(t, resp, body, http.StatusBadRequest, "bad_envelope")
	if nodes := st2.Nodes(); len(nodes) != 0 {
		t.Fatalf("invalid batch mutated store: %+v", nodes)
	}
	rawBadAuditBody, err := json.Marshal(protocol.Envelope{
		V:      1,
		Type:   protocol.TypeAuditBatch,
		NodeID: "n-1",
		SentTS: 1,
		Body:   json.RawMessage(`"not-an-audit-body"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	ts3, st3 := newTestServer(t)
	mixed = append(append([]byte{}, validStatus...), '\n')
	mixed = append(mixed, rawBadAuditBody...)
	resp, body = postTelemetry(t, ts3.URL, "secret", mixed)
	assertJSONError(t, resp, body, http.StatusBadRequest, "bad_audit_body")
	if nodes := st3.Nodes(); len(nodes) != 0 {
		t.Fatalf("invalid body batch mutated store: %+v", nodes)
	}
	rawEmptyAudit, err := json.Marshal(protocol.Envelope{
		V:      1,
		Type:   protocol.TypeAuditBatch,
		NodeID: "n-1",
		SentTS: 1,
		Body: json.RawMessage(`{
			"cursor":{"file_gen":1,"byte_from":100,"byte_to":200,"seq_to":1},
			"events":[]
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	ts4, st4 := newTestServer(t)
	mixed = append(append([]byte{}, validStatus...), '\n')
	mixed = append(mixed, rawEmptyAudit...)
	resp, body = postTelemetry(t, ts4.URL, "secret", mixed)
	assertJSONError(t, resp, body, http.StatusBadRequest, "bad_audit_body")
	if nodes := st4.Nodes(); len(nodes) != 0 {
		t.Fatalf("invalid audit batch mutated store: %+v", nodes)
	}
	if nodes := st.Nodes(); len(nodes) != 0 {
		t.Fatalf("failure cases mutated store: %+v", nodes)
	}
}

func TestDashboardRequiresLogin(t *testing.T) {
	ts, _ := newTestServer(t)

	// APIs without a session -> 401.
	for _, path := range []string{"/api/fleet", "/api/tokens"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s: got %d want 401", path, resp.StatusCode)
		}
	}

	// HTML pages without a session -> redirect to /login.
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/", "/tokens"} {
		resp, err := noRedirect.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("unauthenticated page %s: got %d want 303", path, resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
			t.Fatalf("expected redirect to /login, got %q", loc)
		}
	}
}

func TestDashboardPostRequiresCSRF(t *testing.T) {
	ts, _ := newTestServer(t)
	client := loginClient(t, ts.URL)

	resp, err := client.PostForm(ts.URL+"/account", url.Values{
		"display_name": {"Root"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing csrf got %d want 403", resp.StatusCode)
	}

	resp, err = client.PostForm(ts.URL+"/account", url.Values{
		"display_name": {"Root"},
		"csrf_token":   {"bad"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bad csrf got %d want 403", resp.StatusCode)
	}

	resp, err = client.PostForm(ts.URL+"/account", url.Values{
		"display_name": {"Root"},
		"csrf_token":   {csrfToken(t, client, ts.URL, "/account")},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid csrf got %d want 200", resp.StatusCode)
	}
}

func TestOperatorCapabilitiesGateAdminSurfaces(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := loginClient(t, ts.URL)

	resp, err := admin.PostForm(ts.URL+"/operators", url.Values{
		"username":         {"viewer"},
		"display_name":     {"Viewer"},
		"password":         {"viewer-pass"},
		"confirm_password": {"viewer-pass"},
		"capabilities":     {string(operators.CapabilityFleetView)},
		"csrf_token":       {csrfToken(t, admin, ts.URL, "/operators/new")},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create viewer status=%d", resp.StatusCode)
	}

	jar, _ := cookiejar.New(nil)
	viewer := &http.Client{Jar: jar}
	resp, err = viewer.PostForm(ts.URL+"/login", url.Values{
		"user":     {"viewer"},
		"password": {"viewer-pass"},
		"next":     {"/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/" {
		t.Fatalf("viewer login failed: status=%d path=%s", resp.StatusCode, resp.Request.URL.Path)
	}

	for _, path := range []string{"/tokens", "/operators", "/api/tokens", "/api/operators"} {
		resp, err := viewer.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("viewer GET %s got %d want 403", path, resp.StatusCode)
		}
	}

	resp, err = viewer.Get(ts.URL + "/api/fleet")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer fleet API got %d want 200", resp.StatusCode)
	}
}

func TestDashboardLanguageSwitch(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/login?lang=zh")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	bodyText := string(body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login lang page status=%d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(bodyText, `<html lang="zh">`) || !strings.Contains(bodyText, "登录") {
		t.Fatalf("login page did not render Chinese: %s", bodyText)
	}
	foundLangCookie := false
	for _, c := range resp.Cookies() {
		if c.Name == "scootship_lang" && c.Value == "zh" {
			foundLangCookie = true
		}
	}
	if !foundLangCookie {
		t.Fatalf("language switch did not set zh cookie: %v", resp.Cookies())
	}

	client := loginClient(t, ts.URL)
	resp, err = client.Get(ts.URL + "/?lang=zh")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	bodyText = string(body)
	if !strings.Contains(bodyText, `<html lang="zh">`) || !strings.Contains(bodyText, "车队") || !strings.Contains(bodyText, "自动刷新") {
		t.Fatalf("fleet page did not render Chinese: %s", bodyText)
	}

	resp, err = client.Get(ts.URL + "/tokens")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	bodyText = string(body)
	if !strings.Contains(bodyText, `<html lang="zh">`) || !strings.Contains(bodyText, "令牌") {
		t.Fatalf("tokens page did not keep Chinese cookie: %s", bodyText)
	}

	resp, err = client.Get(ts.URL + "/?lang=en")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	bodyText = string(body)
	if !strings.Contains(bodyText, `<html lang="en">`) || !strings.Contains(bodyText, "Fleet") {
		t.Fatalf("fleet page did not switch back to English: %s", bodyText)
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	ts, _ := newTestServer(t)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}

	resp, err := c.PostForm(ts.URL+"/login", url.Values{"user": {"admin"}, "password": {"wrong"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// A failed login must not yield a usable session.
	r2, err := c.Get(ts.URL + "/api/fleet")
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad creds should not authenticate: got %d want 401", r2.StatusCode)
	}
}

func TestLoginLockout(t *testing.T) {
	cfg := defaultCfg()
	cfg.LoginMaxFails = 3
	ts, _ := newServer(t, cfg)

	// Every httptest request originates from 127.0.0.1, so they share one IP
	// bucket. Don't follow redirects: failures render in place, a success 303s.
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	post := func(pass string) *http.Response {
		t.Helper()
		resp, err := c.PostForm(ts.URL+"/login", url.Values{"user": {"admin"}, "password": {pass}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	// The first two failures are rejected with 401, not yet locked.
	for i := 1; i <= 2; i++ {
		if resp := post("wrong"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d want 401", i, resp.StatusCode)
		}
	}

	// The third failure trips the lockout: 429 with a Retry-After hint.
	resp := post("wrong")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("third attempt: got %d want 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("locked-out response must carry Retry-After")
	}

	// While locked out, even the correct password is refused: the guard
	// short-circuits before the credential check, so guessing cannot continue.
	if resp := post("testpass"); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("correct password during lockout: got %d want 429", resp.StatusCode)
	}
}

func TestRememberDeviceExtendsSessionCookie(t *testing.T) {
	ts, _ := newTestServer(t)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.PostForm(ts.URL+"/login", url.Values{
		"user":     {"admin"},
		"password": {"testpass"},
		"remember": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status=%d, want 303", resp.StatusCode)
	}
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "scootship_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("missing session cookie")
	}
	if time.Until(sessionCookie.Expires) < 29*24*time.Hour {
		t.Fatalf("remembered session expires too soon: %s", sessionCookie.Expires)
	}
}

func TestOperatorManagementFlow(t *testing.T) {
	ts, _ := newTestServer(t)
	client := loginClient(t, ts.URL)

	resp, err := client.Get(ts.URL + "/operators")
	if err != nil {
		t.Fatal(err)
	}
	listBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("operators list status=%d", resp.StatusCode)
	}
	if !strings.Contains(string(listBody), "href=\"/operators/new\"") {
		t.Fatalf("operators list missing create link: %s", listBody)
	}
	if strings.Contains(string(listBody), "name=\"confirm_password\"") {
		t.Fatalf("operators list should not render create form by default: %s", listBody)
	}

	resp, err = client.Get(ts.URL + "/operators/new")
	if err != nil {
		t.Fatal(err)
	}
	newBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("operators new status=%d", resp.StatusCode)
	}
	if !strings.Contains(string(newBody), "name=\"confirm_password\"") {
		t.Fatalf("operators new page missing create form: %s", newBody)
	}

	resp, err = client.PostForm(ts.URL+"/operators", url.Values{
		"username":         {"alice"},
		"display_name":     {"Alice"},
		"email":            {"alice@example.test"},
		"password":         {"alice-pass"},
		"confirm_password": {"alice-pass"},
		"capabilities":     allCapabilities(),
		"csrf_token":       {csrfToken(t, client, ts.URL, "/operators/new")},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create operator status=%d", resp.StatusCode)
	}

	resp, err = client.Get(ts.URL + "/api/operators")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("api operators status=%d body=%s", resp.StatusCode, body)
	}
	bodyText := string(body)
	if !strings.Contains(bodyText, "alice") {
		t.Fatalf("created operator missing from API: %s", body)
	}
	if strings.Contains(bodyText, "alice-pass") || strings.Contains(bodyText, "password_hash") {
		t.Fatalf("operator API leaked password material: %s", body)
	}

	resp, err = client.PostForm(ts.URL+"/operators/alice", url.Values{
		"action":           {"password"},
		"new_password":     {"alice-new"},
		"confirm_password": {"alice-new"},
		"csrf_token":       {csrfToken(t, client, ts.URL, "/operators/alice")},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset password status=%d", resp.StatusCode)
	}

	jar, _ := cookiejar.New(nil)
	alice := &http.Client{Jar: jar}
	resp, err = alice.PostForm(ts.URL+"/login", url.Values{
		"user":     {"alice"},
		"password": {"alice-new"},
		"next":     {"/account"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/account" {
		t.Fatalf("alice login failed: status=%d path=%s", resp.StatusCode, resp.Request.URL.Path)
	}
}

func TestAccountPasswordChange(t *testing.T) {
	ts, _ := newTestServer(t)
	client := loginClient(t, ts.URL)
	resp, err := client.PostForm(ts.URL+"/account/password", url.Values{
		"current_password": {"testpass"},
		"new_password":     {"changed"},
		"confirm_password": {"changed"},
		"csrf_token":       {csrfToken(t, client, ts.URL, "/account")},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("change password status=%d", resp.StatusCode)
	}

	jar, _ := cookiejar.New(nil)
	fresh := &http.Client{Jar: jar}
	resp, err = fresh.PostForm(ts.URL+"/login", url.Values{"user": {"admin"}, "password": {"changed"}, "next": {"/account"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/account" {
		t.Fatalf("login with changed password failed: status=%d path=%s", resp.StatusCode, resp.Request.URL.Path)
	}
}

func TestLogout(t *testing.T) {
	ts, _ := newTestServer(t)
	client := loginClient(t, ts.URL)

	// Logged in: API works.
	if _, ok := getJSON(t, client, ts.URL+"/api/fleet")["nodes"]; !ok {
		t.Fatal("expected nodes key while logged in")
	}

	resp, err := client.PostForm(ts.URL+"/logout", url.Values{
		"csrf_token": {csrfToken(t, client, ts.URL, "/")},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// After logout the session is gone -> 401.
	r2, err := client.Get(ts.URL + "/api/fleet")
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout: got %d want 401", r2.StatusCode)
	}
}

func TestLeaseIsObservationOnly(t *testing.T) {
	ts, _ := newTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/jobs/lease?node=n-1&capacity=1", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lease status=%d", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "" {
		t.Fatalf("phase 1 lease must dispatch no jobs, got %q", body)
	}
	if got := resp.Header.Get("X-Scootship-Dispatch"); got != "disabled-phase1" {
		t.Fatalf("expected dispatch disabled marker, got %q", got)
	}
}

func TestLeaseRejectsNodeMismatch(t *testing.T) {
	ts, _ := newTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/jobs/lease?node=n-other&capacity=1", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assertJSONError(t, resp, body, http.StatusForbidden, "node_mismatch")
}

func TestLeaseRejectsInvalidQuery(t *testing.T) {
	ts, _ := newTestServer(t)
	tests := []struct {
		name string
		path string
		code string
	}{
		{name: "missing node", path: "/jobs/lease?capacity=1", code: "missing_node"},
		{name: "missing capacity", path: "/jobs/lease?node=n-1", code: "bad_capacity"},
		{name: "non numeric capacity", path: "/jobs/lease?node=n-1&capacity=many", code: "bad_capacity"},
		{name: "zero capacity", path: "/jobs/lease?node=n-1&capacity=0", code: "bad_capacity"},
		{name: "excessive capacity", path: "/jobs/lease?node=n-1&capacity=65", code: "bad_capacity"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+tt.path, nil)
			req.Header.Set("Authorization", "Bearer secret")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			assertJSONError(t, resp, body, http.StatusBadRequest, tt.code)
		})
	}
}

func TestTokenInventoryDoesNotExposeSecrets(t *testing.T) {
	ts, _ := newTestServer(t)
	status := protocol.StatusBody{ScootVersion: "0.9.0"}
	resp, body := postTelemetry(t, ts.URL, "secret", envBytes(t, protocol.TypeStatus, "n-1", status))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat status=%d body=%s", resp.StatusCode, body)
	}

	client := loginClient(t, ts.URL)
	for _, path := range []string{"/api/tokens", "/tokens"} {
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d body=%s", path, resp.StatusCode, body)
		}
		bodyText := string(body)
		if strings.Contains(bodyText, `"secret"`) || strings.Contains(bodyText, ">secret<") {
			t.Fatalf("%s exposed bearer token secret in response: %s", path, body)
		}
		if !strings.Contains(bodyText, "sha256:") {
			t.Fatalf("%s missing safe token fingerprint: %s", path, body)
		}
		if path == "/api/tokens" && !strings.Contains(bodyText, "last_authenticated") {
			t.Fatalf("%s missing auth activity metadata: %s", path, body)
		}
	}
}

func TestTokenLifecycleFlowRevealsGeneratedSecretsOnlyOnce(t *testing.T) {
	const generatedTokenMinLength = 32

	ts, _ := newTestServer(t)
	client := loginClient(t, ts.URL)

	resp, err := client.Get(ts.URL + "/tokens")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), `name="node_id"`) {
		t.Fatalf("tokens list should not render create form: %s", body)
	}

	resp, err = client.Get(ts.URL + "/tokens/new")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `name="node_id"`) {
		t.Fatalf("tokens new page missing create form: status=%d body=%s", resp.StatusCode, body)
	}

	resp, err = client.PostForm(ts.URL+"/tokens", url.Values{
		"node_id":    {"n-2"},
		"csrf_token": {csrfToken(t, client, ts.URL, "/tokens/new")},
	})
	if err != nil {
		t.Fatal(err)
	}
	createBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create token status=%d body=%s", resp.StatusCode, createBody)
	}
	createdSecret := revealedTokenSecret(t, string(createBody))
	if len(createdSecret) < generatedTokenMinLength {
		t.Fatalf("generated token too short: %q", createdSecret)
	}

	status := protocol.StatusBody{ScootVersion: "0.9.0"}
	resp, body = postTelemetry(t, ts.URL, createdSecret, envBytes(t, protocol.TypeStatus, "n-2", status))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("created token telemetry status=%d body=%s", resp.StatusCode, body)
	}

	for _, path := range []string{"/api/tokens", "/tokens"} {
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodyText := string(body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d body=%s", path, resp.StatusCode, body)
		}
		if strings.Contains(bodyText, createdSecret) {
			t.Fatalf("%s exposed created token secret: %s", path, body)
		}
		if !strings.Contains(bodyText, "n-2") || !strings.Contains(bodyText, "managed") || !strings.Contains(bodyText, "sha256:") {
			t.Fatalf("%s missing managed token metadata: %s", path, body)
		}
	}

	resp, err = client.PostForm(ts.URL+"/tokens/n-2/rotate", url.Values{
		"csrf_token": {csrfToken(t, client, ts.URL, "/tokens")},
	})
	if err != nil {
		t.Fatal(err)
	}
	rotateBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate token status=%d body=%s", resp.StatusCode, rotateBody)
	}
	if strings.Contains(string(rotateBody), createdSecret) {
		t.Fatalf("rotate response leaked previous token secret: %s", rotateBody)
	}
	rotatedSecret := revealedTokenSecret(t, string(rotateBody))
	if rotatedSecret == createdSecret || len(rotatedSecret) < generatedTokenMinLength {
		t.Fatalf("bad rotated token: created=%q rotated=%q", createdSecret, rotatedSecret)
	}
	resp, _ = postTelemetry(t, ts.URL, createdSecret, envBytes(t, protocol.TypeStatus, "n-2", status))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after rotate got %d want 401", resp.StatusCode)
	}
	resp, body = postTelemetry(t, ts.URL, rotatedSecret, envBytes(t, protocol.TypeStatus, "n-2", status))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotated token telemetry status=%d body=%s", resp.StatusCode, body)
	}

	resp, err = client.PostForm(ts.URL+"/tokens/n-2/revoke", url.Values{
		"csrf_token": {csrfToken(t, client, ts.URL, "/tokens")},
	})
	if err != nil {
		t.Fatal(err)
	}
	revokeBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke token status=%d body=%s", resp.StatusCode, revokeBody)
	}
	if strings.Contains(string(revokeBody), rotatedSecret) {
		t.Fatalf("revoke response leaked token secret: %s", revokeBody)
	}
	resp, _ = postTelemetry(t, ts.URL, rotatedSecret, envBytes(t, protocol.TypeStatus, "n-2", status))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token got %d want 401", resp.StatusCode)
	}
}

func TestSidebarOmitsHealthNavigation(t *testing.T) {
	ts, _ := newTestServer(t)
	client := loginClient(t, ts.URL)

	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fleet page status=%d body=%s", resp.StatusCode, body)
	}
	text := string(body)
	for _, unwanted := range []string{`href="/healthz"`, `target="_blank"`, `id="healthCheck"`, `id="healthPanel"`} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("sidebar should not expose health navigation %q: %s", unwanted, body)
		}
	}
}

func TestHealth(t *testing.T) {
	ts, _ := newTestServer(t)
	m := getJSON(t, http.DefaultClient, ts.URL+"/healthz")
	if m["ok"] != true || m["service"] != "scootship" {
		t.Fatalf("unexpected health: %v", m)
	}
}

func getJSON(t *testing.T, client *http.Client, url string) map[string]any {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", url, resp.StatusCode)
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return m
}

func assertJSONError(t *testing.T, resp *http.Response, body []byte, status int, code string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("status=%d body=%s, want %d", resp.StatusCode, body, status)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode error response: %v body=%s", err, body)
	}
	if got["ok"] != false || got["code"] != code {
		t.Fatalf("error response = %v, want ok=false code=%s", got, code)
	}
}
