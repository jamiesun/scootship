package center

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jamiesun/scootship/internal/protocol"
)

// telemetryAck is the center's reply. The cursor is the durably-stored audit
// range; the edge advances its own cursor only after seeing this ack (EDGE.md:
// telemetry advances only after the center acks).
type telemetryAck struct {
	OK          bool             `json:"ok"`
	NodeID      string           `json:"node_id"`
	Received    int              `json:"received"`
	AuditStored int              `json:"audit_stored"`
	Cursor      *protocol.Cursor `json:"cursor,omitempty"`
}

// handleTelemetry ingests one or more NDJSON envelopes (status, audit_batch, or
// job_event) from an authenticated node. Envelopes are validated as a group
// before anything is applied, so a malformed batch is rejected atomically; once
// validated, application is append-only and idempotent.
func (s *Server) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	node := nodeFromCtx(r)
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxTelemetryByte)
	recvMS := s.now().UnixMilli()

	// Parse the whole batch first. json.Decoder reads a stream of JSON values,
	// which covers both a single envelope and newline-delimited envelopes.
	var envs []protocol.Envelope
	dec := json.NewDecoder(r.Body)
	for {
		var env protocol.Envelope
		if err := dec.Decode(&env); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeJSONError(w, http.StatusRequestEntityTooLarge, "too_large", "telemetry body exceeds limit")
				return
			}
			writeJSONError(w, http.StatusBadRequest, "bad_json", "telemetry must be NDJSON envelopes")
			return
		}
		if err := env.Validate(); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad_envelope", err.Error())
			return
		}
		// A token may only speak for its own node.
		if env.NodeID != node {
			writeJSONError(w, http.StatusForbidden, "node_mismatch", "envelope node_id does not match the authenticated node")
			return
		}
		// Jobs travel center -> edge over the lease channel, never upward.
		if env.Type == protocol.TypeJob {
			writeJSONError(w, http.StatusBadRequest, "wrong_direction", "job envelopes are dispatched via /jobs/lease, not posted to /telemetry")
			return
		}
		envs = append(envs, env)
	}

	if len(envs) == 0 {
		writeJSONError(w, http.StatusBadRequest, "empty", "no envelopes in request")
		return
	}

	ack := telemetryAck{OK: true, NodeID: node, Received: len(envs)}
	for _, env := range envs {
		switch env.Type {
		case protocol.TypeStatus:
			var body protocol.StatusBody
			if err := json.Unmarshal(env.Body, &body); err != nil {
				writeJSONError(w, http.StatusBadRequest, "bad_status_body", err.Error())
				return
			}
			if err := s.store.UpsertStatus(node, recvMS, env.SentTS, body); err != nil {
				s.log.Error("store status", "node", node, "err", err)
				writeJSONError(w, http.StatusInternalServerError, "store_error", "failed to record status")
				return
			}
		case protocol.TypeAuditBatch:
			var body protocol.AuditBatchBody
			if err := json.Unmarshal(env.Body, &body); err != nil {
				writeJSONError(w, http.StatusBadRequest, "bad_audit_body", err.Error())
				return
			}
			cursor, stored, err := s.store.IngestAudit(node, recvMS, body)
			if err != nil {
				s.log.Error("ingest audit", "node", node, "err", err)
				writeJSONError(w, http.StatusInternalServerError, "store_error", "failed to ingest audit")
				return
			}
			c := cursor
			ack.Cursor = &c
			ack.AuditStored += stored
		case protocol.TypeJobEvent:
			var body protocol.JobEventBody
			if err := json.Unmarshal(env.Body, &body); err != nil {
				writeJSONError(w, http.StatusBadRequest, "bad_job_event_body", err.Error())
				return
			}
			if err := s.store.RecordJobEvent(node, recvMS, body); err != nil {
				s.log.Error("record job event", "node", node, "err", err)
				writeJSONError(w, http.StatusInternalServerError, "store_error", "failed to record job event")
				return
			}
		}
	}

	writeJSON(w, http.StatusOK, ack)
}
