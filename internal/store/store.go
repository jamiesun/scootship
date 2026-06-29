// Package store holds the center's view of the fleet.
//
// Everything the edge sends up is append-only and is never re-applied to a
// node's local state (EDGE.md). The Phase 1 implementation persists an
// append-only JSONL log and keeps an in-memory index rebuilt by replaying that
// log on startup. The Store interface deliberately hides this so a queryable
// backend (for example SQLite) can replace it later without touching callers.
package store

import "github.com/jamiesun/scootship/internal/protocol"

// NodeView is the center's current picture of one node.
type NodeView struct {
	NodeID         string                   `json:"node_id"`
	FirstSeenMS    int64                    `json:"first_seen_ms"`
	LastSeenMS     int64                    `json:"last_seen_ms"` // center receive time
	LastSentTS     int64                    `json:"last_sent_ts"` // edge sent_ts
	ScootVersion   string                   `json:"scoot_version"`
	EdgeVersion    string                   `json:"edge_version"`
	Daemon         protocol.DaemonStatus    `json:"daemon"`
	PolicyCeiling  string                   `json:"policy_ceiling"`
	AuditStats     protocol.AuditStats      `json:"audit_stats"`
	Descriptor     *protocol.NodeDescriptor `json:"descriptor,omitempty"`
	Cursor         protocol.Cursor          `json:"cursor"`       // last durably stored audit range
	AuditStored    int                      `json:"audit_stored"` // events accepted into the append-only log
	AuditLifecycle AuditLifecycle           `json:"audit_lifecycle"`
}

// AuditLifecycle reports the center-side retention state for a node's audit
// bodies. The append-only JSONL log remains the durable source of accepted audit
// events; these fields describe the bounded in-memory/dashboard window and any
// explicit audit_gap created by trimming that window.
type AuditLifecycle struct {
	RetentionEvents   int    `json:"retention_events"`
	RetainedEvents    int    `json:"retained_events"`
	GapCount          int    `json:"gap_count"`
	DroppedEvents     int    `json:"dropped_events"`
	OldestRetainedSeq uint64 `json:"oldest_retained_seq"`
	NewestRetainedSeq uint64 `json:"newest_retained_seq"`
	DuplicateReports  int    `json:"duplicate_reports"`
	LastDuplicateMS   int64  `json:"last_duplicate_ms,omitempty"`
	LastGapRecvMS     int64  `json:"last_gap_recv_ms,omitempty"`
	LastGapKind       string `json:"last_gap_kind,omitempty"`
	LastGapReason     string `json:"last_gap_reason,omitempty"`
}

// StoredAudit is one ingested audit event tagged with its node and receive time.
type StoredAudit struct {
	NodeID string              `json:"node_id"`
	RecvMS int64               `json:"recv_ms"`
	Event  protocol.AuditEvent `json:"event"`
}

// AuditTimeline groups retained audit events by session_id/run_id so operators
// can read a run in chronological order without spelunking raw JSONL.
type AuditTimeline struct {
	NodeID      string              `json:"node_id"`
	TimelineID  string              `json:"timeline_id"`
	SessionID   string              `json:"session_id,omitempty"`
	RunID       string              `json:"run_id,omitempty"`
	FirstTS     int64               `json:"first_ts,omitempty"`
	LastTS      int64               `json:"last_ts,omitempty"`
	FirstRecvMS int64               `json:"first_recv_ms"`
	LastRecvMS  int64               `json:"last_recv_ms"`
	FirstSeq    uint64              `json:"first_seq"`
	LastSeq     uint64              `json:"last_seq"`
	EventCount  int                 `json:"event_count"`
	KindCounts  protocol.AuditStats `json:"kind_counts"`
	Events      []StoredAudit       `json:"events"`
}

// StoredJobEvent is one ingested job lifecycle report (E2 telemetry).
type StoredJobEvent struct {
	NodeID string                `json:"node_id"`
	RecvMS int64                 `json:"recv_ms"`
	Body   protocol.JobEventBody `json:"body"`
}

// Store is the center's append-only fleet view.
type Store interface {
	// UpsertStatus records a heartbeat and refreshes the node registry.
	UpsertStatus(nodeID string, recvMS, sentTS int64, body protocol.StatusBody) error

	// IngestAudit applies an audit batch idempotently. It returns the cursor the
	// store has durably stored (which the caller acks back to the edge so the
	// edge only advances after a durable ack) and the count of newly stored
	// events (0 when the batch is a duplicate).
	IngestAudit(nodeID string, recvMS int64, batch protocol.AuditBatchBody) (protocol.Cursor, int, error)

	// RecordJobEvent stores a job lifecycle report.
	RecordJobEvent(nodeID string, recvMS int64, body protocol.JobEventBody) error

	// Nodes returns every known node, newest activity first.
	Nodes() []NodeView

	// Node returns one node by id.
	Node(nodeID string) (NodeView, bool)

	// AuditEvents returns up to limit of the most recent audit events for a node,
	// newest first.
	AuditEvents(nodeID string, limit int) []StoredAudit

	// AuditTimelines returns retained audit events grouped by session_id/run_id,
	// newest run first, with each run's events ordered chronologically.
	AuditTimelines(nodeID string, limit int) []AuditTimeline

	// Close flushes and releases any resources.
	Close() error
}
