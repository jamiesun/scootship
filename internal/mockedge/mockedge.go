// Package mockedge simulates a scoot-edge node for development and testing.
//
// Center tests cannot rely on a locally installed real edge. This simulator
// dials out exactly like the spec's topology demands: outbound only, per-node
// bearer token, NDJSON envelopes. It drives the heartbeat -> ingest -> dashboard
// path plus lease polling as a faithful client of the frozen v1 contract, not a
// second implementation of scoot.
package mockedge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/jamiesun/scootship/internal/protocol"
)

// Options configures a simulated node.
type Options struct {
	Center    string        // center base URL, e.g. http://localhost:8080
	NodeID    string        // stable node identity
	Token     string        // per-node bearer token
	Interval  time.Duration // heartbeat interval
	ShipAudit bool          // also ship synthetic audit batches (opt-in, like the edge)
	Logger    *slog.Logger
}

type edge struct {
	opts   Options
	client *http.Client
	log    *slog.Logger

	// synthetic, monotonically advancing state
	cursor protocol.Cursor
	stats  protocol.AuditStats
}

// Run simulates the node until ctx is cancelled.
func Run(ctx context.Context, opts Options) error {
	if opts.Interval <= 0 {
		opts.Interval = 10 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	opts.Center = strings.TrimRight(opts.Center, "/")

	e := &edge{
		opts:   opts,
		client: &http.Client{Timeout: 15 * time.Second},
		log:    opts.Logger,
		cursor: protocol.Cursor{FileGen: 1},
	}

	e.log.Info("mock-edge dialing center",
		"center", opts.Center, "node", opts.NodeID, "interval", opts.Interval.String(), "ship_audit", opts.ShipAudit)

	e.heartbeat(ctx)
	if opts.ShipAudit {
		e.shipAudit(ctx)
	}

	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			e.log.Info("mock-edge stopping")
			return nil
		case <-ticker.C:
			e.heartbeat(ctx)
			e.lease(ctx)
			if opts.ShipAudit {
				e.shipAudit(ctx)
			}
		}
	}
}

func (e *edge) heartbeat(ctx context.Context) {
	// Simulate a little activity drift between heartbeats.
	e.stats.Run += 1
	e.stats.ToolCall += 3
	e.stats.Observation += 3
	e.stats.Final += 1

	body := protocol.StatusBody{
		ScootVersion:  "0.9.0",
		EdgeVersion:   "mock-0.1.0",
		Daemon:        protocol.DaemonStatus{State: "running", CleanPrevStop: true, Since: time.Now().Add(-time.Hour).UnixMilli()},
		PolicyCeiling: "readonly",
		AuditStats:    e.stats,
		Node: &protocol.NodeDescriptor{
			Labels: []string{"env:dev", "role:demo", "focus:log-triage"},
			OS:     runtime.GOOS,
			Arch:   runtime.GOARCH,
			Capabilities: &protocol.Capabilities{
				MaxJobPolicy: "readonly",
				Tools:        []string{"file_read", "grep", "glob", "http_request"},
				Skills:       []string{"log-triage", "cert-check"},
			},
		},
	}
	status, resp, err := e.post(ctx, "/telemetry", protocol.TypeStatus, body)
	if err != nil {
		e.log.Error("heartbeat failed", "err", err)
		return
	}
	e.log.Info("heartbeat sent", "status", status, "ack", strings.TrimSpace(string(resp)))
}

func (e *edge) shipAudit(ctx context.Context) {
	// Advance the cursor as if more of the local audit log were shipped.
	from := e.cursor.ByteTo
	to := from + 256
	e.cursor.ByteFrom = from
	e.cursor.ByteTo = to
	e.cursor.SeqTo += 2

	body := protocol.AuditBatchBody{
		Cursor: e.cursor,
		Events: []protocol.AuditEvent{
			{Seq: e.cursor.SeqTo - 1, TS: time.Now().UnixMilli(), SessionID: "sess-demo", Kind: "run", Msg: "summarize today's audit anomalies"},
			{Seq: e.cursor.SeqTo, TS: time.Now().UnixMilli(), SessionID: "sess-demo", Kind: "final", Msg: "no anomalies found in the last 24h"},
		},
	}
	status, resp, err := e.post(ctx, "/telemetry", protocol.TypeAuditBatch, body)
	if err != nil {
		e.log.Error("audit ship failed", "err", err)
		return
	}
	e.log.Info("audit batch shipped", "status", status, "ack", strings.TrimSpace(string(resp)))
}

func (e *edge) lease(ctx context.Context) {
	u := fmt.Sprintf("%s/jobs/lease?node=%s&capacity=1", e.opts.Center, url.QueryEscape(e.opts.NodeID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		e.log.Error("lease request build failed", "err", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+e.opts.Token)
	resp, err := e.client.Do(req)
	if err != nil {
		e.log.Error("lease failed", "err", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if len(bytes.TrimSpace(body)) == 0 {
		e.log.Info("lease: no jobs", "status", resp.StatusCode, "dispatch", resp.Header.Get("X-Scootship-Dispatch"))
		return
	}
	e.log.Info("lease: jobs offered", "status", resp.StatusCode, "body", strings.TrimSpace(string(body)))
}

func (e *edge) post(ctx context.Context, path, typ string, body any) (int, []byte, error) {
	payload, err := encodeEnvelope(typ, e.opts.NodeID, body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.opts.Center+path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.opts.Token)
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := e.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, respBody, nil
}

// encodeEnvelope wraps a typed body in the v1 envelope and marshals it to one
// NDJSON line.
func encodeEnvelope(typ, node string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	env := protocol.Envelope{
		V:      protocol.Version,
		Type:   typ,
		NodeID: node,
		SentTS: time.Now().UnixMilli(),
		Body:   b,
	}
	return json.Marshal(env)
}
