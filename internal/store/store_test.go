package store

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jamiesun/scootship/internal/protocol"
)

func sampleStatus() protocol.StatusBody {
	return protocol.StatusBody{
		ScootVersion:  "0.9.0",
		EdgeVersion:   "0.1.0",
		Daemon:        protocol.DaemonStatus{State: "running", CleanPrevStop: true, Since: 1000},
		PolicyCeiling: "readonly",
		AuditStats:    protocol.AuditStats{Run: 12, ToolCall: 40, PolicyDeny: 1},
	}
}

func TestUpsertStatusRegistersNode(t *testing.T) {
	m, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if err := m.UpsertStatus("n-1", 2000, 1900, sampleStatus()); err != nil {
		t.Fatal(err)
	}
	got, ok := m.Node("n-1")
	if !ok {
		t.Fatal("node not registered")
	}
	if got.ScootVersion != "0.9.0" || got.PolicyCeiling != "readonly" {
		t.Fatalf("unexpected view: %+v", got)
	}
	if got.AuditStats.ToolCall != 40 {
		t.Fatalf("audit stats not stored: %+v", got.AuditStats)
	}
	if len(m.Nodes()) != 1 {
		t.Fatalf("expected 1 node, got %d", len(m.Nodes()))
	}
}

func TestIngestAuditIsIdempotent(t *testing.T) {
	m, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	batch := protocol.AuditBatchBody{
		Cursor: protocol.Cursor{FileGen: 1, ByteFrom: 0, ByteTo: 100, SeqTo: 2},
		Events: []protocol.AuditEvent{
			{Seq: 0, TS: 1, Kind: "run", Msg: "start"},
			{Seq: 1, TS: 2, Kind: "final", Msg: "done"},
		},
	}

	cur, n, err := m.IngestAudit("n-1", 3000, batch)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("first ingest stored %d events, want 2", n)
	}
	if cur.ByteTo != 100 {
		t.Fatalf("cursor not advanced: %+v", cur)
	}

	// Replaying the exact same range must be a no-op.
	cur2, n2, err := m.IngestAudit("n-1", 3001, batch)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("duplicate ingest stored %d events, want 0", n2)
	}
	if cur2 != cur {
		t.Fatalf("duplicate ingest moved cursor: %+v -> %+v", cur, cur2)
	}
	if got := m.AuditEvents("n-1", 0); len(got) != 2 {
		t.Fatalf("expected 2 stored events, got %d", len(got))
	}
	node, ok := m.Node("n-1")
	if !ok {
		t.Fatal("node not registered")
	}
	if node.AuditLifecycle.DuplicateReports != 1 || node.AuditLifecycle.LastDuplicateMS != 3001 {
		t.Fatalf("duplicate audit report not visible: %+v", node.AuditLifecycle)
	}

	// A newer range advances.
	next := protocol.AuditBatchBody{
		Cursor: protocol.Cursor{FileGen: 1, ByteFrom: 100, ByteTo: 220, SeqTo: 3},
		Events: []protocol.AuditEvent{{Seq: 2, TS: 3, Kind: "tool_call", Msg: "grep"}},
	}
	_, n3, err := m.IngestAudit("n-1", 3002, next)
	if err != nil {
		t.Fatal(err)
	}
	if n3 != 1 {
		t.Fatalf("advancing ingest stored %d events, want 1", n3)
	}
}

func TestAuditRetentionGapIsExplicit(t *testing.T) {
	m, err := OpenWithOptions("", Options{AuditRetentionEvents: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	batch := protocol.AuditBatchBody{
		Cursor: protocol.Cursor{FileGen: 1, ByteFrom: 0, ByteTo: 300, SeqTo: 3},
		Events: []protocol.AuditEvent{
			{Seq: 0, TS: 1, Kind: "run", Msg: "start"},
			{Seq: 1, TS: 2, Kind: "tool_call", Msg: "grep"},
			{Seq: 2, TS: 3, Kind: "final", Msg: "done"},
		},
	}
	if _, n, err := m.IngestAudit("n-1", 3000, batch); err != nil || n != 3 {
		t.Fatalf("ingest stored=%d err=%v", n, err)
	}
	got, ok := m.Node("n-1")
	if !ok {
		t.Fatal("node not registered")
	}
	if got.AuditStored != 3 {
		t.Fatalf("AuditStored = %d, want accepted 3", got.AuditStored)
	}
	lc := got.AuditLifecycle
	if lc.RetentionEvents != 2 || lc.RetainedEvents != 2 || lc.GapCount != 1 || lc.DroppedEvents != 1 {
		t.Fatalf("unexpected lifecycle: %+v", lc)
	}
	if lc.LastGapKind != "audit_gap" || lc.LastGapReason != "retention_limit" || lc.LastGapRecvMS != 3000 {
		t.Fatalf("gap not explicit: %+v", lc)
	}
	if lc.OldestRetainedSeq != 1 || lc.NewestRetainedSeq != 2 {
		t.Fatalf("retained range = %d..%d, want 1..2", lc.OldestRetainedSeq, lc.NewestRetainedSeq)
	}
	events := m.AuditEvents("n-1", 0)
	if len(events) != 2 || events[0].Event.Seq != 2 || events[1].Event.Seq != 1 {
		t.Fatalf("retained events newest-first wrong: %+v", events)
	}

	if _, n, err := m.IngestAudit("n-1", 3001, batch); err != nil || n != 0 {
		t.Fatalf("duplicate ingest stored=%d err=%v", n, err)
	}
	after, _ := m.Node("n-1")
	if after.AuditLifecycle.GapCount != 1 || after.AuditLifecycle.DroppedEvents != 1 {
		t.Fatalf("duplicate changed lifecycle gap: %+v", after.AuditLifecycle)
	}
}

func TestAuditTimelinesGroupRetainedEventsByRun(t *testing.T) {
	m, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	_, _, err = m.IngestAudit("n-1", 3000, protocol.AuditBatchBody{
		Cursor: protocol.Cursor{FileGen: 1, ByteTo: 500, SeqTo: 5},
		Events: []protocol.AuditEvent{
			{Seq: 0, TS: 100, SessionID: "s-1", RunID: "r-1", Kind: "run", Msg: "start"},
			{Seq: 1, TS: 110, SessionID: "s-1", RunID: "r-1", Kind: "tool_call", Msg: "grep"},
			{Seq: 2, TS: 120, SessionID: "s-1", RunID: "r-1", Kind: "final", Msg: "done"},
			{Seq: 3, TS: 210, SessionID: "s-2", Kind: "run", Msg: "other"},
			{Seq: 4, TS: 220, Kind: "system_error", Msg: "unscoped"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	timelines := m.AuditTimelines("n-1", 0)
	if len(timelines) != 3 {
		t.Fatalf("timelines len = %d, want 3: %+v", len(timelines), timelines)
	}
	if timelines[0].TimelineID != "unscoped" || timelines[0].KindCounts.SystemError != 1 {
		t.Fatalf("newest timeline should be unscoped system_error: %+v", timelines[0])
	}
	run := timelines[2]
	if run.SessionID != "s-1" || run.RunID != "r-1" || run.EventCount != 3 {
		t.Fatalf("session/run grouping wrong: %+v", run)
	}
	if run.FirstSeq != 0 || run.LastSeq != 2 || run.FirstTS != 100 || run.LastTS != 120 {
		t.Fatalf("timeline bounds wrong: %+v", run)
	}
	if run.KindCounts.Run != 1 || run.KindCounts.ToolCall != 1 || run.KindCounts.Final != 1 {
		t.Fatalf("kind counts wrong: %+v", run.KindCounts)
	}
	if len(run.Events) != 3 || run.Events[0].Event.Seq != 0 || run.Events[2].Event.Seq != 2 {
		t.Fatalf("events not chronological: %+v", run.Events)
	}

	limited := m.AuditTimelines("n-1", 1)
	if len(limited) != 1 || limited[0].TimelineID != "unscoped" {
		t.Fatalf("timeline limit wrong: %+v", limited)
	}
}

func TestReplayRebuildsState(t *testing.T) {
	dir := t.TempDir()

	m, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.UpsertStatus("n-1", 2000, 1900, sampleStatus()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.IngestAudit("n-1", 3000, protocol.AuditBatchBody{
		Cursor: protocol.Cursor{FileGen: 1, ByteTo: 100, SeqTo: 1},
		Events: []protocol.AuditEvent{{Seq: 0, TS: 1, Kind: "run", Msg: "x"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen and confirm the append-only log rebuilt the fleet view.
	m2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	got, ok := m2.Node("n-1")
	if !ok {
		t.Fatal("node lost after replay")
	}
	if got.ScootVersion != "0.9.0" {
		t.Fatalf("status lost after replay: %+v", got)
	}
	if got.Cursor.ByteTo != 100 || got.AuditStored != 1 {
		t.Fatalf("audit state lost after replay: %+v", got)
	}
	if _, err := filepath.Abs(dir); err != nil {
		t.Fatal(err)
	}
}

func TestReplayRebuildsAuditRetentionGap(t *testing.T) {
	dir := t.TempDir()

	m, err := OpenWithOptions(dir, Options{AuditRetentionEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.IngestAudit("n-1", 3000, protocol.AuditBatchBody{
		Cursor: protocol.Cursor{FileGen: 1, ByteTo: 200, SeqTo: 2},
		Events: []protocol.AuditEvent{
			{Seq: 0, TS: 1, Kind: "run", Msg: "start"},
			{Seq: 1, TS: 2, Kind: "final", Msg: "done"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}

	m2, err := OpenWithOptions(dir, Options{AuditRetentionEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	got, ok := m2.Node("n-1")
	if !ok {
		t.Fatal("node lost after replay")
	}
	if got.AuditStored != 2 || got.AuditLifecycle.RetainedEvents != 1 || got.AuditLifecycle.DroppedEvents != 1 {
		t.Fatalf("retention gap not rebuilt after replay: %+v", got)
	}
	if events := m2.AuditEvents("n-1", 0); len(events) != 1 || events[0].Event.Seq != 1 {
		t.Fatalf("retained replay events wrong: %+v", events)
	}
}

func TestDispatchQueueLeasesNodeBoundJobs(t *testing.T) {
	m, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if err := m.UpsertStatus("n-1", 1000, 900, protocol.StatusBody{
		PolicyCeiling: protocol.PolicyReadonly,
		Node: &protocol.NodeDescriptor{
			Labels: []string{"role:db"},
			Capabilities: &protocol.Capabilities{
				Tools:  []string{"grep"},
				Skills: []string{"log-triage"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.UpsertStatus("n-2", 1000, 900, protocol.StatusBody{PolicyCeiling: protocol.PolicyUnrestricted}); err != nil {
		t.Fatal(err)
	}

	job, dup, err := m.EnqueueJob(1100, DispatchRequest{
		JobID:           "j-1",
		IdemKey:         "idem-1",
		NodeID:          "n-1",
		Goal:            "summarize audit anomalies",
		RequestedPolicy: protocol.PolicyUnrestricted,
		DeadlineTS:      10_000,
		Requestor:       "admin",
		RequiredLabels:  []string{"role:db"},
		RequiredTools:   []string{"grep"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if dup {
		t.Fatal("first enqueue reported duplicate")
	}
	if job.RequestedPolicy != protocol.PolicyReadonly {
		t.Fatalf("policy not clamped to node ceiling: %+v", job)
	}

	dupJob, dup, err := m.EnqueueJob(1200, DispatchRequest{
		JobID:      "j-other",
		IdemKey:    "idem-1",
		NodeID:     "n-1",
		Goal:       "different",
		DeadlineTS: 10_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !dup || dupJob.JobID != "j-1" {
		t.Fatalf("idem duplicate did not return original job: dup=%v job=%+v", dup, dupJob)
	}

	none, err := m.LeaseJobs("n-2", 1, 1300)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("wrong node leased job: %+v", none)
	}

	got, err := m.LeaseJobs("n-1", 1, 1300)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("leased %d jobs, want 1", len(got))
	}
	if got[0].Type != protocol.TypeJob || got[0].NodeID != "n-1" {
		t.Fatalf("bad lease envelope: %+v", got[0])
	}
	var body protocol.JobBody
	if err := json.Unmarshal(got[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.JobID != "j-1" || body.RequestedPolicy != protocol.PolicyReadonly || body.Kind != protocol.JobKindRun {
		t.Fatalf("bad job body: %+v", body)
	}
	after, ok := m.Job("j-1")
	if !ok || after.Phase != dispatchLeased || after.Attempts != 1 {
		t.Fatalf("job not marked leased: %+v ok=%v", after, ok)
	}
}

func TestDispatchQueueRejectsCapabilityMismatch(t *testing.T) {
	m, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if err := m.UpsertStatus("n-1", 1000, 900, protocol.StatusBody{
		PolicyCeiling: protocol.PolicyReadonly,
		Node: &protocol.NodeDescriptor{
			Labels:       []string{"role:web"},
			Capabilities: &protocol.Capabilities{Tools: []string{"grep"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.EnqueueJob(1100, DispatchRequest{
		JobID:          "j-1",
		IdemKey:        "idem-1",
		NodeID:         "n-1",
		Goal:           "read logs",
		DeadlineTS:     10_000,
		RequiredLabels: []string{"role:db"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := m.LeaseJobs("n-1", 1, 1200)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("mismatched job leased: %+v", got)
	}
	job, ok := m.Job("j-1")
	if !ok || job.Phase != protocol.JobPhaseRejected || job.RejectReason != "no_matching_capability" {
		t.Fatalf("mismatched job not rejected: %+v ok=%v", job, ok)
	}
}

func TestDispatchReplayRebuildsJobs(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.UpsertStatus("n-1", 1000, 900, protocol.StatusBody{PolicyCeiling: protocol.PolicyReadonly}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.EnqueueJob(1100, DispatchRequest{
		JobID:      "j-1",
		IdemKey:    "idem-1",
		NodeID:     "n-1",
		Goal:       "summarize",
		DeadlineTS: 10_000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.LeaseJobs("n-1", 1, 1200); err != nil {
		t.Fatal(err)
	}
	if err := m.RecordJobEvent("n-1", 1300, protocol.JobEventBody{
		JobID:           "j-1",
		Phase:           protocol.JobPhaseRunning,
		SessionID:       "s-1",
		EffectivePolicy: protocol.PolicyReadonly,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}

	m2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	job, ok := m2.Job("j-1")
	if !ok {
		t.Fatal("job lost after replay")
	}
	if job.Phase != protocol.JobPhaseRunning || job.SessionID != "s-1" || job.EffectivePolicy != protocol.PolicyReadonly {
		t.Fatalf("job lifecycle not rebuilt: %+v", job)
	}
	dup, duplicate, err := m2.EnqueueJob(1400, DispatchRequest{
		JobID:      "j-other",
		IdemKey:    "idem-1",
		NodeID:     "n-1",
		Goal:       "duplicate",
		DeadlineTS: 10_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate || dup.JobID != "j-1" {
		t.Fatalf("idem index not rebuilt: duplicate=%v job=%+v", duplicate, dup)
	}
}
