// Package protocol fixes the scoot-edge wire contract (v1) on the center side.
//
// The contract is frozen in the scoot repository's docs/EDGE.md. scootship only
// ever speaks this contract and must not depend on scoot internals. Unknown
// fields are ignored by the JSON decoder, and an unknown major protocol version
// is rejected, so a fleet can run mixed scoot/edge versions during rollout.
//
// Direction of travel (EDGE.md topology): the edge dials out to the center; the
// center is the server. status / audit_batch / job_event flow up from the edge;
// job flows down from the center over the lease channel.
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Version is the only protocol major version scootship speaks. EDGE.md pins v:1.
const Version = 1

// Envelope is the single wire envelope shared by every scoot-edge message:
//
//	{"v":1,"type":"status|audit_batch|job|job_event","node_id":"n-7a3",
//	 "sent_ts":1719600000000,"body":{}}
type Envelope struct {
	V      int             `json:"v"`
	Type   string          `json:"type"`
	NodeID string          `json:"node_id"`
	SentTS int64           `json:"sent_ts"`
	Body   json.RawMessage `json:"body"`
}

// Message types carried by the envelope.
const (
	TypeStatus     = "status"      // up: heartbeat + optional node descriptor
	TypeAuditBatch = "audit_batch" // up: append-only audit log shipping
	TypeJob        = "job"         // down: schema'd dispatch (E2)
	TypeJobEvent   = "job_event"   // up: job lifecycle report (E2)
)

// ErrUnknownType is returned for an envelope type outside the closed set.
var ErrUnknownType = errors.New("unknown envelope type")

// Validate checks the envelope shape without trusting any field value.
func (e Envelope) Validate() error {
	if e.V != Version {
		return fmt.Errorf("unsupported protocol version %d (want %d)", e.V, Version)
	}
	if e.NodeID == "" {
		return errors.New("missing node_id")
	}
	switch e.Type {
	case TypeStatus, TypeAuditBatch, TypeJob, TypeJobEvent:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnknownType, e.Type)
	}
}

// StatusBody is the heartbeat. policy_ceiling is the local edge.max_job_policy
// (the ceiling for center-dispatched jobs), defaulting to "readonly". audit_stats
// is derived by the edge from logs/*.jsonl.
type StatusBody struct {
	ScootVersion  string          `json:"scoot_version"`
	EdgeVersion   string          `json:"edge_version"`
	Daemon        DaemonStatus    `json:"daemon"`
	PolicyCeiling string          `json:"policy_ceiling"`
	AuditStats    AuditStats      `json:"audit_stats"`
	Node          *NodeDescriptor `json:"node,omitempty"`
}

// DaemonStatus reflects scoot's daemon lifecycle state.
type DaemonStatus struct {
	State         string `json:"state"`
	CleanPrevStop bool   `json:"clean_prev_stop"`
	Since         int64  `json:"since"`
}

// AuditStats are the derived per-kind counts from scoot's audit log.
type AuditStats struct {
	Run         uint64 `json:"run"`
	Thought     uint64 `json:"thought"`
	ToolCall    uint64 `json:"tool_call"`
	Observation uint64 `json:"observation"`
	Final       uint64 `json:"final"`
	PolicyDeny  uint64 `json:"policy_deny"`
	SystemError uint64 `json:"system_error"`
}

// NodeDescriptor is the optional identity/capability advertisement. It is
// advisory only: advertising is never authority. The center uses it to decide
// what to dispatch; the local policy_ceiling still gates execution.
type NodeDescriptor struct {
	Labels       []string      `json:"labels,omitempty"`
	OS           string        `json:"os,omitempty"`
	Arch         string        `json:"arch,omitempty"`
	Capabilities *Capabilities `json:"capabilities,omitempty"`
}

// Capabilities advertise what a node is for. Claiming a capability never expands
// what the edge will do.
type Capabilities struct {
	MaxJobPolicy string   `json:"max_job_policy,omitempty"`
	Tools        []string `json:"tools,omitempty"`
	Skills       []string `json:"skills,omitempty"`
}

// AuditBatchBody ships append-only audit records with an idempotency cursor.
type AuditBatchBody struct {
	Cursor Cursor       `json:"cursor"`
	Events []AuditEvent `json:"events"`
}

// Cursor is the idempotency cursor from EDGE.md: a monotonic rotation generation
// plus byte offsets, with seq as a secondary correlation. The center stores
// append-only; replaying the same range is a no-op (at-least-once, idempotent
// apply).
type Cursor struct {
	FileGen  uint64 `json:"file_gen"`
	ByteFrom uint64 `json:"byte_from"`
	ByteTo   uint64 `json:"byte_to"`
	SeqTo    uint64 `json:"seq_to"`
}

// After reports whether c is strictly newer than prev in (file_gen, byte_to)
// lexicographic order. A batch whose cursor is not After the stored cursor is a
// duplicate and must be applied as a no-op. seq is not a safe dedup key on its
// own because it restarts at 0 after rotation.
func (c Cursor) After(prev Cursor) bool {
	if c.FileGen != prev.FileGen {
		return c.FileGen > prev.FileGen
	}
	return c.ByteTo > prev.ByteTo
}

// AuditEvent mirrors a scoot src/audit.zig JSONL line. msg is untrusted data and
// must never be treated as an instruction.
type AuditEvent struct {
	Seq       uint64 `json:"seq"`
	TS        int64  `json:"ts"`
	SessionID string `json:"session_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	Kind      string `json:"kind"`
	Msg       string `json:"msg"`
}

// JobBody is a schema'd dispatch. kind is a closed enum (currently only "run").
// goal is opaque data handed to scoot -e; the center never synthesizes shell.
type JobBody struct {
	JobID           string `json:"job_id"`
	IdemKey         string `json:"idem_key"`
	Kind            string `json:"kind"`
	Goal            string `json:"goal"`
	RequestedPolicy string `json:"requested_policy"`
	DeadlineTS      int64  `json:"deadline_ts"`
	MaxRetries      int    `json:"max_retries"`
}

// JobEventBody reports a job's lifecycle back over the append-only channel.
type JobEventBody struct {
	JobID           string `json:"job_id"`
	Phase           string `json:"phase"`
	SessionID       string `json:"session_id,omitempty"`
	EffectivePolicy string `json:"effective_policy,omitempty"`
	RejectReason    string `json:"reject_reason,omitempty"`
}
