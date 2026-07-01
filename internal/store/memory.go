package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/jamiesun/scootship/internal/protocol"
)

const (
	// DefaultAuditRetentionEvents caps how many recent audit events are retained
	// per node in memory for API/dashboard reads. All accepted events are still
	// persisted to the append-only JSONL log before they are acked.
	DefaultAuditRetentionEvents = 1000
	// DefaultDispatchQueueLimitPerNode caps how many non-terminal (queued/leased/
	// accepted/running) dispatch jobs a single node may have outstanding at once.
	// This bounds operator-created dispatch queue growth per node; it does not
	// affect audit ingest or telemetry.
	DefaultDispatchQueueLimitPerNode = 200
	auditGapKind                     = "audit_gap"
	auditGapReasonRetention          = "retention_limit"
)

// Options controls the center-side in-memory index. It does not weaken the
// append-only durability contract: acked audit ranges are always written to the
// JSONL log first.
type Options struct {
	AuditRetentionEvents int
	// DispatchQueueLimitPerNode bounds how many non-terminal dispatch jobs one
	// node may have queued/leased/running at the same time. New jobs beyond the
	// limit are rejected with ErrDispatchQueueFull instead of persisted.
	DispatchQueueLimitPerNode int
}

// persistRecord is one line in the append-only log. Exactly one payload field is
// set per record, selected by Rec.
type persistRecord struct {
	Rec      string                 `json:"rec"`
	NodeID   string                 `json:"node_id"`
	RecvMS   int64                  `json:"recv_ms"`
	SentTS   int64                  `json:"sent_ts,omitempty"`
	Status   *protocol.StatusBody   `json:"status,omitempty"`
	Cursor   *protocol.Cursor       `json:"cursor,omitempty"`
	Events   []protocol.AuditEvent  `json:"events,omitempty"`
	Job      *protocol.JobEventBody `json:"job_event,omitempty"`
	Dispatch *DispatchJob           `json:"dispatch_job,omitempty"`
}

const (
	recStatus      = "status"
	recAudit       = "audit_batch"
	recJobEvent    = "job_event"
	recDispatchJob = "dispatch_job"
	dispatchQueued = "queued"
	dispatchLeased = "leased"
)

type nodeState struct {
	view   NodeView
	audits []StoredAudit
}

// Mem is an in-memory fleet view backed by an append-only JSONL log.
type Mem struct {
	mu       sync.Mutex
	nodes    map[string]*nodeState
	jobs     map[string]*DispatchJob
	idem     map[string]string
	jobOrder []string
	file     *os.File
	w        *bufio.Writer
	options  Options
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
	m := &Mem{
		nodes:   make(map[string]*nodeState),
		jobs:    make(map[string]*DispatchJob),
		idem:    make(map[string]string),
		options: normalizeOptions(opts),
	}
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
	if opts.DispatchQueueLimitPerNode <= 0 {
		opts.DispatchQueueLimitPerNode = DefaultDispatchQueueLimitPerNode
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
	case recDispatchJob:
		if rec.Dispatch != nil {
			m.applyDispatchJob(*rec.Dispatch)
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

func (m *Mem) applyJobEvent(nodeID string, recvMS int64, body protocol.JobEventBody) {
	ns := m.node(nodeID)
	if recvMS > ns.view.LastSeenMS {
		ns.view.LastSeenMS = recvMS
	}
	job := m.jobs[body.JobID]
	if job == nil {
		return
	}
	if isTerminalDispatchPhase(job.Phase) && body.Phase != job.Phase {
		return
	}
	job.Phase = body.Phase
	job.UpdatedMS = recvMS
	if body.SessionID != "" {
		job.SessionID = body.SessionID
	}
	if body.EffectivePolicy != "" {
		job.EffectivePolicy = body.EffectivePolicy
	}
	if body.RejectReason != "" {
		job.RejectReason = body.RejectReason
	}
}

func (m *Mem) applyDispatchJob(job DispatchJob) {
	j := job
	if _, ok := m.jobs[j.JobID]; !ok {
		m.jobOrder = append(m.jobOrder, j.JobID)
	}
	m.jobs[j.JobID] = &j
	if j.IdemKey != "" {
		m.idem[j.IdemKey] = j.JobID
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
		ns.view.AuditLifecycle.DuplicateReports++
		ns.view.AuditLifecycle.LastDuplicateMS = recvMS
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

// EnqueueJob implements Store.
func (m *Mem) EnqueueJob(recvMS int64, req DispatchRequest) (DispatchJob, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id := m.idem[req.IdemKey]; id != "" {
		return cloneDispatchJob(*m.jobs[id]), true, nil
	}
	job, err := m.buildDispatchJobLocked(recvMS, req)
	if err != nil {
		return DispatchJob{}, false, err
	}
	if err := m.persistDispatchJobLocked(job); err != nil {
		return DispatchJob{}, false, err
	}
	m.applyDispatchJob(job)
	return cloneDispatchJob(job), false, nil
}

func (m *Mem) buildDispatchJobLocked(recvMS int64, req DispatchRequest) (DispatchJob, error) {
	if strings.TrimSpace(req.JobID) == "" {
		return DispatchJob{}, errors.New("missing job_id")
	}
	if strings.TrimSpace(req.IdemKey) == "" {
		return DispatchJob{}, errors.New("missing idem_key")
	}
	if _, exists := m.jobs[req.JobID]; exists {
		return DispatchJob{}, fmt.Errorf("duplicate job_id %q", req.JobID)
	}
	node, ok := m.nodes[req.NodeID]
	if !ok {
		return DispatchJob{}, fmt.Errorf("%w: %q", ErrUnknownNode, req.NodeID)
	}
	if limit := m.options.DispatchQueueLimitPerNode; limit > 0 && m.pendingDispatchCountLocked(req.NodeID) >= limit {
		return DispatchJob{}, fmt.Errorf("%w: node %q already has %d pending jobs", ErrDispatchQueueFull, req.NodeID, limit)
	}
	policy, err := clampDispatchPolicy(req.RequestedPolicy, node.view.PolicyCeiling)
	if err != nil {
		return DispatchJob{}, err
	}
	job := DispatchJob{
		JobID:           req.JobID,
		IdemKey:         req.IdemKey,
		NodeID:          req.NodeID,
		Kind:            protocol.JobKindRun,
		Goal:            req.Goal,
		RequestedPolicy: policy,
		DeadlineTS:      req.DeadlineTS,
		MaxRetries:      req.MaxRetries,
		Requestor:       req.Requestor,
		RequiredLabels:  cloneStrings(req.RequiredLabels),
		RequiredTools:   cloneStrings(req.RequiredTools),
		RequiredSkills:  cloneStrings(req.RequiredSkills),
		Phase:           dispatchQueued,
		CreatedMS:       recvMS,
		UpdatedMS:       recvMS,
	}
	if err := job.Body().Validate(); err != nil {
		return DispatchJob{}, err
	}
	return job, nil
}

// LeaseJobs implements Store.
func (m *Mem) LeaseJobs(nodeID string, capacity int, recvMS int64) ([]protocol.Envelope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if capacity <= 0 {
		return nil, errors.New("capacity must be positive")
	}
	node, ok := m.nodes[nodeID]
	if !ok {
		return nil, nil
	}
	out := make([]protocol.Envelope, 0, capacity)
	for _, id := range m.jobOrder {
		if len(out) >= capacity {
			break
		}
		job := m.jobs[id]
		if job == nil || job.NodeID != nodeID || job.Phase != dispatchQueued {
			continue
		}
		if job.DeadlineTS <= recvMS {
			if err := m.rejectDispatchJobLocked(job, recvMS, "deadline_exceeded"); err != nil {
				return nil, err
			}
			continue
		}
		if !dispatchMatchesNode(*job, node.view) {
			if err := m.rejectDispatchJobLocked(job, recvMS, "no_matching_capability"); err != nil {
				return nil, err
			}
			continue
		}
		leased := cloneDispatchJob(*job)
		env, err := dispatchEnvelope(nodeID, recvMS, leased)
		if err != nil {
			return nil, err
		}
		leased.Phase = dispatchLeased
		leased.Attempts++
		leased.LeasedMS = recvMS
		leased.UpdatedMS = recvMS
		if err := m.persistDispatchJobLocked(leased); err != nil {
			return nil, err
		}
		m.applyDispatchJob(leased)
		out = append(out, env)
	}
	return out, nil
}

func (m *Mem) rejectDispatchJobLocked(job *DispatchJob, recvMS int64, reason string) error {
	rejected := cloneDispatchJob(*job)
	rejected.Phase = protocol.JobPhaseRejected
	rejected.RejectReason = reason
	rejected.UpdatedMS = recvMS
	if err := m.persistDispatchJobLocked(rejected); err != nil {
		return err
	}
	m.applyDispatchJob(rejected)
	return nil
}

func dispatchEnvelope(nodeID string, sentMS int64, job DispatchJob) (protocol.Envelope, error) {
	body := job.Body()
	if err := body.Validate(); err != nil {
		return protocol.Envelope{}, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return protocol.Envelope{}, err
	}
	return protocol.Envelope{
		V:      protocol.Version,
		Type:   protocol.TypeJob,
		NodeID: nodeID,
		SentTS: sentMS,
		Body:   raw,
	}, nil
}

func (m *Mem) persistDispatchJobLocked(job DispatchJob) error {
	j := cloneDispatchJob(job)
	return m.persist(persistRecord{Rec: recDispatchJob, NodeID: job.NodeID, RecvMS: job.UpdatedMS, Dispatch: &j})
}

// Jobs implements Store.
func (m *Mem) Jobs() []DispatchJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]DispatchJob, 0, len(m.jobs))
	for _, id := range m.jobOrder {
		if job := m.jobs[id]; job != nil {
			out = append(out, cloneDispatchJob(*job))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UpdatedMS != out[j].UpdatedMS {
			return out[i].UpdatedMS > out[j].UpdatedMS
		}
		return out[i].JobID < out[j].JobID
	})
	return out
}

// Job implements Store.
func (m *Mem) Job(jobID string) (DispatchJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[jobID]
	if job == nil {
		return DispatchJob{}, false
	}
	return cloneDispatchJob(*job), true
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

// AuditTimelines implements Store.
func (m *Mem) AuditTimelines(nodeID string, limit int) []AuditTimeline {
	m.mu.Lock()
	defer m.mu.Unlock()
	ns := m.nodes[nodeID]
	if ns == nil || len(ns.audits) == 0 {
		return nil
	}

	order := make([]string, 0)
	byKey := map[string]*AuditTimeline{}
	for _, audit := range ns.audits {
		key, id := timelineKey(audit.Event)
		tl := byKey[key]
		if tl == nil {
			tl = &AuditTimeline{
				NodeID:      nodeID,
				TimelineID:  id,
				SessionID:   audit.Event.SessionID,
				RunID:       audit.Event.RunID,
				FirstTS:     audit.Event.TS,
				LastTS:      audit.Event.TS,
				FirstRecvMS: audit.RecvMS,
				LastRecvMS:  audit.RecvMS,
				FirstSeq:    audit.Event.Seq,
				LastSeq:     audit.Event.Seq,
			}
			byKey[key] = tl
			order = append(order, key)
		}
		tl.Events = append(tl.Events, audit)
		tl.EventCount++
		tl.LastTS = audit.Event.TS
		tl.LastRecvMS = audit.RecvMS
		tl.LastSeq = audit.Event.Seq
		incrementAuditStats(&tl.KindCounts, audit.Event.Kind)
	}

	out := make([]AuditTimeline, 0, len(order))
	for _, key := range order {
		tl := byKey[key]
		events := make([]StoredAudit, len(tl.Events))
		copy(events, tl.Events)
		tl.Events = events
		out = append(out, *tl)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastTS != out[j].LastTS {
			return out[i].LastTS > out[j].LastTS
		}
		if out[i].LastRecvMS != out[j].LastRecvMS {
			return out[i].LastRecvMS > out[j].LastRecvMS
		}
		return out[i].LastSeq > out[j].LastSeq
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func timelineKey(ev protocol.AuditEvent) (key, id string) {
	switch {
	case ev.SessionID != "" && ev.RunID != "":
		return "session:" + ev.SessionID + "\x00run:" + ev.RunID, ev.SessionID + " / " + ev.RunID
	case ev.SessionID != "":
		return "session:" + ev.SessionID, ev.SessionID
	case ev.RunID != "":
		return "run:" + ev.RunID, ev.RunID
	default:
		return "unscoped", "unscoped"
	}
}

func incrementAuditStats(stats *protocol.AuditStats, kind string) {
	switch kind {
	case "run":
		stats.Run++
	case "thought":
		stats.Thought++
	case "tool_call":
		stats.ToolCall++
	case "observation":
		stats.Observation++
	case "final":
		stats.Final++
	case "policy_deny":
		stats.PolicyDeny++
	case "system_error":
		stats.SystemError++
	}
}

func clampDispatchPolicy(requested, ceiling string) (string, error) {
	if requested == "" {
		requested = protocol.PolicyReadonly
	}
	if ceiling == "" {
		ceiling = protocol.PolicyReadonly
	}
	reqRank, ok := policyRank(requested)
	if !ok {
		return "", fmt.Errorf("unsupported requested_policy %q", requested)
	}
	ceilRank, ok := policyRank(ceiling)
	if !ok {
		return "", fmt.Errorf("unsupported node policy_ceiling %q", ceiling)
	}
	if reqRank > ceilRank {
		requested = ceiling
	}
	// Edge jobs are unattended; guarded has no meaningful prompt boundary and
	// collapses to readonly in Scoot's clamp. Match that on the center side.
	if requested == protocol.PolicyGuarded {
		requested = protocol.PolicyReadonly
	}
	return requested, nil
}

func policyRank(policy string) (int, bool) {
	switch policy {
	case protocol.PolicyReadonly:
		return 0, true
	case protocol.PolicyGuarded:
		return 1, true
	case protocol.PolicyUnrestricted:
		return 2, true
	default:
		return 0, false
	}
}

func dispatchMatchesNode(job DispatchJob, node NodeView) bool {
	desc := node.Descriptor
	if len(job.RequiredLabels) > 0 {
		if desc == nil || !containsAll(desc.Labels, job.RequiredLabels) {
			return false
		}
	}
	if len(job.RequiredTools) > 0 {
		if desc == nil || desc.Capabilities == nil || !containsAll(desc.Capabilities.Tools, job.RequiredTools) {
			return false
		}
	}
	if len(job.RequiredSkills) > 0 {
		if desc == nil || desc.Capabilities == nil || !containsAll(desc.Capabilities.Skills, job.RequiredSkills) {
			return false
		}
	}
	return true
}

func containsAll(have, want []string) bool {
	if len(want) == 0 {
		return true
	}
	set := make(map[string]bool, len(have))
	for _, item := range have {
		set[item] = true
	}
	for _, item := range want {
		if !set[item] {
			return false
		}
	}
	return true
}

func isTerminalDispatchPhase(phase string) bool {
	switch phase {
	case protocol.JobPhaseDone, protocol.JobPhaseFailed, protocol.JobPhaseRejected:
		return true
	default:
		return false
	}
}

// pendingDispatchCountLocked counts dispatch jobs targeting nodeID that have
// not reached a terminal phase yet, bounding unattended queue growth.
func (m *Mem) pendingDispatchCountLocked(nodeID string) int {
	count := 0
	for _, job := range m.jobs {
		if job == nil || job.NodeID != nodeID {
			continue
		}
		if !isTerminalDispatchPhase(job.Phase) {
			count++
		}
	}
	return count
}

func cloneDispatchJob(job DispatchJob) DispatchJob {
	job.RequiredLabels = cloneStrings(job.RequiredLabels)
	job.RequiredTools = cloneStrings(job.RequiredTools)
	job.RequiredSkills = cloneStrings(job.RequiredSkills)
	return job
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
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
