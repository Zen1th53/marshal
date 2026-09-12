package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Memory is the section most likely to leak: its records hold whatever an agent
// learned. These tests defend the boundary between showing that MARSHAL knows
// something and handing over what it knows.

type fakeMemoryReader struct {
	status     MemoryStatus
	statusErr  error
	records    []model.MemoryRecordV2
	recordsErr error
	slots      []MemorySlot
}

func (f fakeMemoryReader) MemoryStatus(context.Context) (MemoryStatus, error) {
	return f.status, f.statusErr
}
func (f fakeMemoryReader) RecallRecent(context.Context, int) ([]model.MemoryRecordV2, error) {
	return f.records, f.recordsErr
}
func (f fakeMemoryReader) TaskSlots(context.Context, string) ([]MemorySlot, error) {
	return f.slots, nil
}

func testMemory(t *testing.T, reader MemoryReader) *MemoryFeed {
	t.Helper()
	return &MemoryFeed{Reader: reader, SessionID: "sess-1", ProjectID: "proj-1",
		Now: fixedClock()}
}

// A record's content is never copyable. A terminal that copies it puts it
// somewhere MARSHAL no longer governs.
func TestMemoryContentIsNeverCopyable(t *testing.T) {
	snap := testMemory(t, fakeMemoryReader{records: []model.MemoryRecordV2{{
		ID: "mem-1", Lifecycle: model.MemoryDurable,
		Title: "deploy key", Body: "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI",
		CreatedAt: time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC),
	}}}).ReadMemory(context.Background())

	if len(snap.Records) != 1 {
		t.Fatalf("got %d records", len(snap.Records))
	}
	excerpt := snap.Records[0].Excerpt
	if text, ok := excerpt.CopyText(); ok {
		t.Fatalf("a memory excerpt was copyable: %q", text)
	}
	if !excerpt.Redacted {
		t.Fatal("a memory excerpt is not marked redacted")
	}
	// And the rendered form does not carry the content either.
	if strings.Contains(excerpt.Display(), "wJalrXUtnFEMI") {
		t.Fatalf("the excerpt rendered its content: %q", excerpt.Display())
	}
}

// Identity, standing and provenance are shown freely: knowing what MARSHAL
// remembers and where it learned it is what a user needs.
func TestMemoryIdentityAndProvenanceAreShown(t *testing.T) {
	snap := testMemory(t, fakeMemoryReader{records: []model.MemoryRecordV2{{
		ID: "mem-1", Lifecycle: model.MemoryDurable, Kind: model.MemoryKind("semantic"),
		Source:      model.MemorySource{Kind: "repository", Reference: "abc123"},
		EvidenceIDs: []string{"ev-1", "ev-2"},
		CreatedAt:   time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC),
	}}}).ReadMemory(context.Background())

	row := snap.Records[0]
	if row.ID.Display() != "mem-1" {
		t.Fatalf("the record id rendered as %q", row.ID.Display())
	}
	if !strings.Contains(row.Provenance.Display(), "repository") {
		t.Fatalf("the provenance omits the source kind: %q", row.Provenance.Display())
	}
	if !strings.Contains(row.Provenance.Display(), "2 evidence") {
		t.Fatalf("the provenance omits the evidence count: %q", row.Provenance.Display())
	}
}

// A record with no provenance says so: it cannot be checked against anything.
func TestUnsourcedMemorySaysItCannotBeChecked(t *testing.T) {
	snap := testMemory(t, fakeMemoryReader{records: []model.MemoryRecordV2{{
		ID: "mem-1", Lifecycle: model.MemoryDurable,
		CreatedAt: time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC),
	}}}).ReadMemory(context.Background())

	provenance := snap.Records[0].Provenance
	if provenance.Status.IsSuccess() {
		t.Fatalf("an unsourced record reports provenance %q", provenance.Display())
	}
	if !strings.Contains(provenance.Reason, "nothing can be checked") {
		t.Fatalf("the missing provenance is not explained: %q", provenance.Reason)
	}
}

// A candidate record has not been promoted, and must not read as knowledge
// MARSHAL holds.
func TestCandidateMemoryIsNotRun(t *testing.T) {
	snap := testMemory(t, fakeMemoryReader{records: []model.MemoryRecordV2{{
		ID: "mem-1", Lifecycle: model.MemoryCandidate,
		CreatedAt: time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC),
	}}}).ReadMemory(context.Background())

	standing := snap.Records[0].Standing
	if standing.Status.IsSuccess() {
		t.Fatalf("a candidate record reports %q", standing.Display())
	}
	if standing.Status != TruthNotRun {
		t.Fatalf("a candidate record reports %s, want NOT_RUN", standing.Status.Label())
	}
}

// A retired record is a real answer: it says what MARSHAL used to believe.
func TestRetiredMemoryReadsAsARealAnswer(t *testing.T) {
	for _, lifecycle := range []model.MemoryLifecycle{
		model.MemorySuperseded, model.MemoryRejected, model.MemoryStale,
	} {
		snap := testMemory(t, fakeMemoryReader{records: []model.MemoryRecordV2{{
			ID: "mem-1", Lifecycle: lifecycle,
			CreatedAt: time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC),
		}}}).ReadMemory(context.Background())

		standing := snap.Records[0].Standing
		if standing.Display() == "" || standing.Status == TruthUnset {
			t.Fatalf("%s rendered as nothing", lifecycle)
		}
		if !strings.Contains(standing.Reason, "retired") {
			t.Fatalf("%s does not say it is retired: %q", lifecycle, standing.Reason)
		}
	}
}

// An unrecognised lifecycle becomes UNKNOWN, never a durable fact.
func TestUnrecognisedMemoryLifecycleIsUnknown(t *testing.T) {
	snap := testMemory(t, fakeMemoryReader{records: []model.MemoryRecordV2{{
		ID: "mem-1", Lifecycle: model.MemoryLifecycle("brand-new-state"),
		CreatedAt: time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC),
	}}}).ReadMemory(context.Background())

	standing := snap.Records[0].Standing
	if standing.Status.IsSuccess() {
		t.Fatalf("an unrecognised lifecycle was treated as known: %q", standing.Display())
	}
	if standing.Status != TruthUnknown {
		t.Fatalf("an unrecognised lifecycle reports %s", standing.Status.Label())
	}
}

// An unreadable record feed says why, rather than rendering as empty memory.
func TestUnreadableMemoryIsDistinctFromEmptyMemory(t *testing.T) {
	empty := testMemory(t, fakeMemoryReader{}).ReadMemory(context.Background())
	if empty.RecordsStatus.Status != TruthEmpty {
		t.Fatalf("an empty memory reported %s", empty.RecordsStatus.Status.Label())
	}

	broken := testMemory(t, fakeMemoryReader{
		recordsErr: errors.New("the index is rebuilding"),
	}).ReadMemory(context.Background())
	if broken.RecordsStatus.Status != TruthError {
		t.Fatalf("an unreadable memory reported %s", broken.RecordsStatus.Status.Label())
	}
	if !strings.Contains(broken.RecordsStatus.Reason, "rebuilding") {
		t.Fatalf("the read failure was lost: %q", broken.RecordsStatus.Reason)
	}
}

// An unhealthy memory service says so: recall may be incomplete, and a user
// reading a record list needs to know the list may be missing entries.
func TestUnhealthyMemoryServiceWarns(t *testing.T) {
	snap := testMemory(t, fakeMemoryReader{
		status: MemoryStatus{Version: "v2", Healthy: false},
	}).ReadMemory(context.Background())

	if !strings.Contains(snap.ServiceHealth.Display(), "unhealthy") {
		t.Fatalf("an unhealthy service rendered as %q", snap.ServiceHealth.Display())
	}
	if !strings.Contains(snap.ServiceHealth.Reason, "degraded") {
		t.Fatalf("the health warning does not explain itself: %q", snap.ServiceHealth.Reason)
	}
}

// Memory actions that still lack their own typed form remain unavailable and
// say why. CTUI-0424 and CTUI-0426 are deliberately excluded from this list:
// they respectively collect semantic knowledge and Process05-bound outcome
// metadata before submitting through the canonical MemoryService boundary.
func TestMemoryWritesThatNeedContentAreUnavailable(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	for _, id := range []ActionID{
		"CTUI-0433", "CTUI-0434",
		"CTUI-0450", "CTUI-0461", "CTUI-0485", "CTUI-0491",
	} {
		binding, ok := source.Bindings()[id]
		if !ok {
			t.Fatalf("%s has no binding", id)
		}
		if binding.Bound() {
			t.Fatalf("%s writes memory content this screen does not collect", id)
		}
		if !strings.Contains(binding.Requires, "Process 07") {
			t.Fatalf("%s does not name the owning process: %q", id, binding.Requires)
		}

		c := &Confirmation{}
		if err := c.Begin(ctx, binding, ActionRequest{Action: id}); err == nil {
			t.Fatalf("%s opened a confirmation despite being unavailable", id)
		}
	}
	if n := atomic.LoadInt32(&auth.memoryInvalidations); n != 0 {
		t.Fatalf("unavailable memory writes made %d invalidations", n)
	}
}

// A hard purge destroys records irrecoverably and must not be a keystroke.
func TestHardPurgeIsUnavailableAndDestructive(t *testing.T) {
	source, _ := testControl(t)
	binding := source.Bindings()["CTUI-0457"]

	if binding.Safety != SafetyDestructive {
		t.Fatalf("a hard purge is classed %s", binding.Safety)
	}
	if binding.Bound() {
		t.Fatal("a hard purge is bound to a keystroke")
	}
	if !strings.Contains(binding.Requires, "irrecoverably") {
		t.Fatalf("the refusal does not say what is at stake: %q", binding.Requires)
	}
}

// Rebuilding projections carries no content, so it is bound and reaches the
// canonical service.
func TestRebuildProjectionsReachesTheService(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	binding := source.Bindings()["CTUI-0499"]
	if !binding.Bound() {
		t.Fatal("rebuilding projections is not bound")
	}
	c := &Confirmation{}
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0499"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	outcome, err := c.Submit(ctx)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	// The service is reached, but it returns only an error and exposes no
	// projection state to reread, so the outcome must stay UNKNOWN. Asserting
	// PASS here would be asserting that a nil error proves a transition.
	if outcome.Verdict != VerdictUnknown {
		t.Fatalf("a rebuild produced %s: %s", outcome.Verdict, outcome.Detail)
	}
	if n := atomic.LoadInt32(&auth.projectionRebuilds); n != 1 {
		t.Fatalf("the service was called %d times", n)
	}
}

// A failed rebuild does not report success.
func TestFailedRebuildIsNotReportedAsApplied(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	auth.mu.Lock()
	auth.memoryErr = errors.New("the index is locked")
	auth.mu.Unlock()

	outcome, _ := source.executeRebuildProjections(ctx,
		ActionRequest{Action: "CTUI-0499", Target: Target{Kind: "session", ID: "sess-1"}})
	if outcome.Verdict == VerdictPass {
		t.Fatal("a failed rebuild reported success")
	}
	if outcome.Proof.Status.IsSuccess() {
		t.Fatal("a failed rebuild carries a successful proof")
	}
}
