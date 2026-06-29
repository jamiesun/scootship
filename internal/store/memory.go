package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/jamiesun/scootship/internal/protocol"
)

const (
	// DefaultAuditRetentionEvents caps how many recent audit events are retained
	// per node in memory for API/dashboard reads. All accepted events are still
	// persisted to the append-only JSONL log before they are acked.
	DefaultAuditRetentionEvents = 1000
	auditGapKind                = "audit_gap"
	auditGapReasonRetention     = "retention_limit"
)

// Options controls the center-side in-memory index. It does not weaken the
// append-only durability contract: acked audit ranges are always written to the
// JSONL log first.
type Options struct {
	AuditRetentionEvents int
}

// persistRecord is one line in the append-only log. Exactly one payload field is
// set per record, selected by Rec.
type persistRecord struct {
	Rec    string                 `json:"rec"`
	NodeID string                 `json:"node_id"`
	RecvMS int64                  `json:"recv_ms"`
	SentTS int64                  `json:"sent_ts,omitempty"`
	Status *protocol.StatusBody   `json:"status,omitempty"`
	Cursor *protocol.Cursor       `json:"cursor,omitempty"`
	Events []protocol.AuditEvent  `json:"events,omitempty"`
	Job    *protocol.JobEventBody `json:"job_event,omitempty"`
}

const (
	recStatus   = "status"
	recAudit    = "audit_batch"
	recJobEvent = "job_event"
)

type nodeState struct {
	view   NodeView
	audits []StoredAudit
}

// Mem is an in-memory fleet view backed by an append-only JSONL log.
type Mem struct {
	mu      sync.Mutex
	nodes   map[string]*nodeState
	file    *os.File
	w       *bufio.Writer
	options Options
}

var _ Store = (*Mem)(nil)

// Open returns a Mem store persisting to <dataDir>/center.jsonl, replaying any
// existing log to rebuild the fleet view. Pass an empty dataDir for a purely
// in-memory store (used by tests).
func Open(dataDir string) (*Mem, error) {
	return OpenWithOptions(dataDir, Options{})
}

// OpenWithOptions is Open plus explicit store retention settings.
func OpenWithOptions(dataDir string, opts Options) (*Mem, error) {
	m := &Mem{nodes: make(map[string]*nodeState), options: normalizeOptions(opts)}
	if dataDir == "" {
		return m, nil
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dataDir, "center.jsonl")
	if err := m.replay(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	m.file = f
	m.w = bufio.NewWriter(f)
	return m, nil
}

func normalizeOptions(opts Options) Options {
	if opts.AuditRetentionEvents <= 0 {
		opts.AuditRetentionEvents = DefaultAuditRetentionEvents
	}
	return opts
}

func (m *Mem) replay(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open log for replay: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec persistRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			// A torn final line (e.g. from a crash mid-write) must not abort
			// startup; skip it and keep replaying durable records.
			continue
		}
		m.applyRecord(rec)
	}
	return sc.Err()
}

// applyRecord mutates in-memory state from a persisted record without writing.
func (m *Mem) applyRecord(rec persistRecord) {
	switch rec.Rec {
	case recStatus:
		if rec.Status != nil {
			m.applyStatus(rec.NodeID, rec.RecvMS, rec.SentTS, *rec.Status)
		}
	case recAudit:
		if rec.Cursor != nil {
			m.applyAudit(rec.NodeID, rec.RecvMS, *rec.Cursor, rec.Events)
		}
	case recJobEvent:
		if rec.Job != nil {
			m.applyJobEvent(rec.NodeID, rec.RecvMS, *rec.Job)
		}
	}
}

func (m *Mem) node(nodeID string) *nodeState {
	ns := m.nodes[nodeID]
	if ns == nil {
		ns = &nodeState{view: NodeView{
			NodeID: nodeID,
			AuditLifecycle: AuditLifecycle{
				RetentionEvents: m.options.AuditRetentionEvents,
			},
		}}
		m.nodes[nodeID] = ns
	}
	return ns
}

func (m *Mem) applyStatus(nodeID string, recvMS, sentTS int64, body protocol.StatusBody) {
	ns := m.node(nodeID)
	if ns.view.FirstSeenMS == 0 {
		ns.view.FirstSeenMS = recvMS
	}
	ns.view.LastSeenMS = recvMS
	ns.view.LastSentTS = sentTS
	ns.view.ScootVersion = body.ScootVersion
	ns.view.EdgeVersion = body.EdgeVersion
	ns.view.Daemon = body.Daemon
	ns.view.PolicyCeiling = body.PolicyCeiling
	ns.view.AuditStats = body.AuditStats
	if body.Node != nil {
		ns.view.Descriptor = body.Node
	}
}

// applyAudit stores events idempotently and returns how many were newly stored.
func (m *Mem) applyAudit(nodeID string, recvMS int64, cursor protocol.Cursor, events []protocol.AuditEvent) int {
	ns := m.node(nodeID)
	if ns.view.FirstSeenMS == 0 {
		ns.view.FirstSeenMS = recvMS
	}
	if !cursor.After(ns.view.Cursor) {
		return 0 // duplicate range: no-op
	}
	for _, ev := range events {
		ns.audits = append(ns.audits, StoredAudit{NodeID: nodeID, RecvMS: recvMS, Event: ev})
	}
	m.applyAuditRetention(ns, recvMS)
	ns.view.Cursor = cursor
	ns.view.AuditStored += len(events)
	if recvMS > ns.view.LastSeenMS {
		ns.view.LastSeenMS = recvMS
	}
	return len(events)
}

func (m *Mem) applyAuditRetention(ns *nodeState, recvMS int64) {
	limit := m.options.AuditRetentionEvents
	if limit <= 0 {
		limit = DefaultAuditRetentionEvents
	}
	lc := &ns.view.AuditLifecycle
	lc.RetentionEvents = limit
	if len(ns.audits) > limit {
		dropped := len(ns.audits) - limit
		ns.audits = ns.audits[dropped:]
		lc.GapCount++
		lc.DroppedEvents += dropped
		lc.LastGapRecvMS = recvMS
		lc.LastGapKind = auditGapKind
		lc.LastGapReason = auditGapReasonRetention
	}
	lc.RetainedEvents = len(ns.audits)
	lc.OldestRetainedSeq = 0
	lc.NewestRetainedSeq = 0
	if len(ns.audits) > 0 {
		lc.OldestRetainedSeq = ns.audits[0].Event.Seq
		lc.NewestRetainedSeq = ns.audits[len(ns.audits)-1].Event.Seq
	}
}

func (m *Mem) applyJobEvent(nodeID string, recvMS int64, _ protocol.JobEventBody) {
	ns := m.node(nodeID)
	if recvMS > ns.view.LastSeenMS {
		ns.view.LastSeenMS = recvMS
	}
}

func (m *Mem) persist(rec persistRecord) error {
	if m.w == nil {
		return nil // in-memory only
	}
	enc, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := m.w.Write(enc); err != nil {
		return err
	}
	if err := m.w.WriteByte('\n'); err != nil {
		return err
	}
	if err := m.w.Flush(); err != nil {
		return err
	}
	// fsync so an acked audit range is durable before the edge advances its
	// cursor (EDGE.md: telemetry advances only after the center acks).
	return m.file.Sync()
}

// UpsertStatus implements Store.
func (m *Mem) UpsertStatus(nodeID string, recvMS, sentTS int64, body protocol.StatusBody) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := body
	if err := m.persist(persistRecord{Rec: recStatus, NodeID: nodeID, RecvMS: recvMS, SentTS: sentTS, Status: &b}); err != nil {
		return err
	}
	m.applyStatus(nodeID, recvMS, sentTS, body)
	return nil
}

// IngestAudit implements Store.
func (m *Mem) IngestAudit(nodeID string, recvMS int64, batch protocol.AuditBatchBody) (protocol.Cursor, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ns := m.node(nodeID)
	if !batch.Cursor.After(ns.view.Cursor) {
		return ns.view.Cursor, 0, nil // duplicate: ack the cursor we already hold
	}
	c := batch.Cursor
	if err := m.persist(persistRecord{Rec: recAudit, NodeID: nodeID, RecvMS: recvMS, Cursor: &c, Events: batch.Events}); err != nil {
		return ns.view.Cursor, 0, err
	}
	n := m.applyAudit(nodeID, recvMS, batch.Cursor, batch.Events)
	return ns.view.Cursor, n, nil
}

// RecordJobEvent implements Store.
func (m *Mem) RecordJobEvent(nodeID string, recvMS int64, body protocol.JobEventBody) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := body
	if err := m.persist(persistRecord{Rec: recJobEvent, NodeID: nodeID, RecvMS: recvMS, Job: &b}); err != nil {
		return err
	}
	m.applyJobEvent(nodeID, recvMS, body)
	return nil
}

// Nodes implements Store.
func (m *Mem) Nodes() []NodeView {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]NodeView, 0, len(m.nodes))
	for _, ns := range m.nodes {
		out = append(out, ns.view)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeenMS != out[j].LastSeenMS {
			return out[i].LastSeenMS > out[j].LastSeenMS
		}
		return out[i].NodeID < out[j].NodeID
	})
	return out
}

// Node implements Store.
func (m *Mem) Node(nodeID string) (NodeView, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ns := m.nodes[nodeID]
	if ns == nil {
		return NodeView{}, false
	}
	return ns.view, true
}

// AuditEvents implements Store.
func (m *Mem) AuditEvents(nodeID string, limit int) []StoredAudit {
	m.mu.Lock()
	defer m.mu.Unlock()
	ns := m.nodes[nodeID]
	if ns == nil || len(ns.audits) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(ns.audits) {
		limit = len(ns.audits)
	}
	out := make([]StoredAudit, 0, limit)
	for i := len(ns.audits) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, ns.audits[i])
	}
	return out
}

// Close implements Store.
func (m *Mem) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.w != nil {
		if err := m.w.Flush(); err != nil {
			return err
		}
	}
	if m.file != nil {
		return m.file.Close()
	}
	return nil
}
