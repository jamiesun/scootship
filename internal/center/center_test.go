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

	resp, err := client.PostForm(ts.URL+"/logout", nil)
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
