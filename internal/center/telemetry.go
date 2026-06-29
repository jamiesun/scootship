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

type telemetryMessage struct {
	env      protocol.Envelope
	status   protocol.StatusBody
	audit    protocol.AuditBatchBody
	jobEvent protocol.JobEventBody
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
	var msgs []telemetryMessage
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
		msg := telemetryMessage{env: env}
		switch env.Type {
		case protocol.TypeStatus:
			if err := json.Unmarshal(env.Body, &msg.status); err != nil {
				writeJSONError(w, http.StatusBadRequest, "bad_status_body", err.Error())
				return
			}
		case protocol.TypeAuditBatch:
			if err := json.Unmarshal(env.Body, &msg.audit); err != nil {
				writeJSONError(w, http.StatusBadRequest, "bad_audit_body", err.Error())
				return
			}
		case protocol.TypeJobEvent:
			if err := json.Unmarshal(env.Body, &msg.jobEvent); err != nil {
				writeJSONError(w, http.StatusBadRequest, "bad_job_event_body", err.Error())
				return
			}
		}
		msgs = append(msgs, msg)
	}

	if len(msgs) == 0 {
		writeJSONError(w, http.StatusBadRequest, "empty", "no envelopes in request")
		return
	}

	ack := telemetryAck{OK: true, NodeID: node, Received: len(msgs)}
	for _, msg := range msgs {
		switch msg.env.Type {
		case protocol.TypeStatus:
			if err := s.store.UpsertStatus(node, recvMS, msg.env.SentTS, msg.status); err != nil {
				s.log.Error("store status", "node", node, "err", err)
				writeJSONError(w, http.StatusInternalServerError, "store_error", "failed to record status")
				return
			}
		case protocol.TypeAuditBatch:
			cursor, stored, err := s.store.IngestAudit(node, recvMS, msg.audit)
			if err != nil {
				s.log.Error("ingest audit", "node", node, "err", err)
				writeJSONError(w, http.StatusInternalServerError, "store_error", "failed to ingest audit")
				return
			}
			c := cursor
			ack.Cursor = &c
			ack.AuditStored += stored
		case protocol.TypeJobEvent:
			if err := s.store.RecordJobEvent(node, recvMS, msg.jobEvent); err != nil {
				s.log.Error("record job event", "node", node, "err", err)
				writeJSONError(w, http.StatusInternalServerError, "store_error", "failed to record job event")
				return
			}
		}
	}

	writeJSON(w, http.StatusOK, ack)
}
