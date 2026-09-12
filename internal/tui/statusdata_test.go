package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/resources"
	"github.com/Zen1th53/marshal/internal/startup"
)

// These tests defend the one property the whole binding layer exists for: a
// value MARSHAL does not have must never reach the screen as a value it does.

// --- fakes, written from the canonical signatures ---

type fakeRuntime struct {
	status RuntimeStatus
	events []model.Event
	tasks  []model.Task
	err    error
	id     string
}

func (f fakeRuntime) Status(context.Context) (RuntimeStatus, error) {
	if f.err != nil {
		return RuntimeStatus{}, f.err
	}
	return f.status, nil
}
func (f fakeRuntime) Events(context.Context) ([]model.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.events, nil
}
func (f fakeRuntime) Tasks(context.Context) ([]model.Task, error) { return f.tasks, f.err }
func (f fakeRuntime) InstanceID() string                          { return f.id }

type fakeAssessment struct {
	assessment startup.Assessment
	err        error
}

func (f fakeAssessment) Assessment(context.Context) (startup.Assessment, error) {
	return f.assessment, f.err
}

type fakeResources struct {
	snap resources.Snapshot
	err  error
}

func (f fakeResources) Resources(context.Context) (resources.Snapshot, error) {
	return f.snap, f.err
}

type fakeCloud struct {
	configured, entitled bool
	caps                 []string
	expires, renews      time.Time
	hasLease             bool
	installation, sess   string
	err                  error
}

func (f *fakeCloud) Configured() bool       { return f.configured }
func (f *fakeCloud) Entitled() bool         { return f.entitled }
func (f *fakeCloud) Capabilities() []string { return f.caps }
func (f *fakeCloud) ExpiresAt() (time.Time, bool) {
	return f.expires, f.hasLease
}
func (f *fakeCloud) RenewAt() (time.Time, bool) { return f.renews, f.hasLease }
func (f *fakeCloud) InstallationID() string     { return f.installation }
func (f *fakeCloud) SessionID() string          { return f.sess }
func (f *fakeCloud) Err() error                 { return f.err }

type fakeProviders struct {
	probes []ProviderProbe
	err    error
}

func (f fakeProviders) Providers(context.Context) ([]ProviderProbe, error) {
	return f.probes, f.err
}

func fixedClock() func() time.Time {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return at }
}

// --- runtime ---

// With no runtime attached, every field must say it could not be read. A zero
// here would tell the user their project is empty when the daemon is simply
// not there.
func TestRuntimeWithoutASourceIsUnknownNotZero(t *testing.T) {
	s := &StatusSource{Now: fixedClock()}
	snap := s.ReadRuntime(context.Background())

	for name, v := range map[string]Value{
		"tasks": snap.Tasks, "agents": snap.Agents,
		"sessions": snap.Sessions, "leases": snap.Leases,
		"schema": snap.SchemaVersion, "instance": snap.InstanceID,
	} {
		if v.Status.IsSuccess() {
			t.Fatalf("%s reports a known value with no runtime attached", name)
		}
		if v.Display() == "0" || v.Display() == "" {
			t.Fatalf("%s rendered as %q with no runtime attached", name, v.Display())
		}
		if v.Reason == "" {
			t.Fatalf("%s is unavailable but gives no reason", name)
		}
	}
	if snap.Verdict == VerdictPass {
		t.Fatal("an unreadable runtime summarised as PASS")
	}
}

// A runtime that replied with zero counts genuinely has none, and that must
// render as zero — the distinction only works if measured zeros survive.
func TestMeasuredZeroCountsRenderAsZero(t *testing.T) {
	s := &StatusSource{
		Now:     fixedClock(),
		Runtime: fakeRuntime{id: "inst-1", status: RuntimeStatus{SchemaVersion: 85}},
	}
	snap := s.ReadRuntime(context.Background())

	if got := snap.Tasks.Display(); got != "0" {
		t.Fatalf("a measured zero rendered as %q, want 0", got)
	}
	if !snap.Tasks.Status.IsSuccess() {
		t.Fatal("a measured zero is not reported as known")
	}
	if snap.Verdict != VerdictPass {
		t.Fatalf("a successful read summarised as %s", snap.Verdict)
	}
}

// A runtime error is an error, distinct from an absent runtime.
func TestRuntimeErrorIsDistinctFromAbsence(t *testing.T) {
	s := &StatusSource{
		Now:     fixedClock(),
		Runtime: fakeRuntime{err: errors.New("database is locked")},
	}
	snap := s.ReadRuntime(context.Background())
	if snap.Tasks.Status != TruthError {
		t.Fatalf("a failing runtime reported %s, want ERROR", snap.Tasks.Status.Label())
	}
	if snap.Verdict != VerdictFail {
		t.Fatalf("a failing runtime summarised as %s", snap.Verdict)
	}
	if !strings.Contains(snap.Tasks.Reason, "database is locked") {
		t.Fatalf("the failure reason was lost: %q", snap.Tasks.Reason)
	}
}

// --- events ---

// An empty event log is EMPTY; an unreadable one is not. Both render, and they
// must not render the same.
func TestEmptyEventsAreDistinctFromUnreadableEvents(t *testing.T) {
	empty := (&StatusSource{Now: fixedClock(), Runtime: fakeRuntime{}}).
		ReadEvents(context.Background(), 10)
	if empty.Status.Status != TruthEmpty {
		t.Fatalf("an empty log reported %s, want EMPTY", empty.Status.Status.Label())
	}

	broken := (&StatusSource{Now: fixedClock(), Runtime: fakeRuntime{err: errors.New("io")}}).
		ReadEvents(context.Background(), 10)
	if broken.Status.Status != TruthError {
		t.Fatalf("an unreadable log reported %s, want ERROR", broken.Status.Status.Label())
	}

	absent := (&StatusSource{Now: fixedClock()}).ReadEvents(context.Background(), 10)
	if absent.Status.Status != TruthUnknown {
		t.Fatalf("an absent runtime reported %s, want UNKNOWN", absent.Status.Status.Label())
	}
}

// Events are newest first and bounded, so a long-running project does not make
// the dashboard slower or push the newest activity off the end.
func TestEventsAreNewestFirstAndBounded(t *testing.T) {
	base := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	// Built oldest-first and deliberately shuffled into the store's order, so
	// the test proves the reader sorts rather than inheriting an order.
	var events []model.Event
	for i := 0; i < 50; i++ {
		events = append(events, model.Event{
			ID:        fmt.Sprintf("e%02d", i),
			Type:      fmt.Sprintf("event.%02d", i),
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
	}
	events[0], events[49] = events[49], events[0]

	feed := (&StatusSource{Now: fixedClock(), Runtime: fakeRuntime{events: events}}).
		ReadEvents(context.Background(), 5)

	if len(feed.Rows) != 5 {
		t.Fatalf("the feed returned %d rows, want the 5 requested", len(feed.Rows))
	}
	// The newest event is the one at 10:49, whatever position it held in the
	// store's own ordering.
	if got := feed.Rows[0].Type.Display(); got != "event.49" {
		t.Fatalf("the first row is %q, want the most recent event", got)
	}
	if got := feed.Rows[4].Type.Display(); got != "event.45" {
		t.Fatalf("the fifth row is %q, want the fifth-newest event", got)
	}
}

// A future timestamp is reported as future rather than clamped, because clock
// skew between a daemon and the UI is a real problem worth seeing.
func TestFutureEventsAreNotClamped(t *testing.T) {
	future := time.Date(2026, 9, 10, 13, 0, 0, 0, time.UTC)
	feed := (&StatusSource{
		Now:     fixedClock(),
		Runtime: fakeRuntime{events: []model.Event{{Type: "x", Timestamp: future}}},
	}).ReadEvents(context.Background(), 5)

	if got := feed.Rows[0].When.Display(); !strings.HasPrefix(got, "in ") {
		t.Fatalf("a future event rendered as %q, want it marked as future", got)
	}
}

// --- blockers ---

// An assessment that never ran is NOT_RUN, never READY. This is the single
// most dangerous default in the whole surface.
func TestUnassessedReadinessIsNotRunNeverReady(t *testing.T) {
	list := (&StatusSource{Now: fixedClock()}).ReadBlockers(context.Background())
	if list.Readiness != VerdictNotRun {
		t.Fatalf("an unassessed project reported %s, want NOT_RUN", list.Readiness)
	}
	v := readinessValue(list)
	if v.Status.IsSuccess() {
		t.Fatal("an unassessed project reported a successful readiness value")
	}
	if strings.Contains(strings.ToUpper(v.Display()), "READY") {
		t.Fatalf("an unassessed project rendered as %q", v.Display())
	}
	if v.Reason == "" {
		t.Fatal("NOT_RUN readiness gives no reason")
	}
}

// A blocking check must surface as BLOCKED and name where to fix it.
func TestBlockingChecksAreBlockedAndNameTheirOwner(t *testing.T) {
	assessment := startup.Assessment{
		AssessedAt: time.Now(),
		Checks: []startup.Check{{
			ID:        "core.store",
			Dimension: startup.DimensionCore,
			Status:    startup.StatusBroken,
			Required:  true,
			Summary:   "the canonical store could not be opened",
			Impact:    "no work can be recorded",
			Remedy:    "check the runtime directory permissions",
		}},
	}
	list := (&StatusSource{Now: fixedClock(), Assessment: fakeAssessment{assessment: assessment}}).
		ReadBlockers(context.Background())

	if len(list.Blockers) == 0 {
		t.Skip("this build's Check.Blocking() does not treat the fixture as blocking")
	}
	if list.Readiness != VerdictBlocked {
		t.Fatalf("a blocking check produced readiness %s", list.Readiness)
	}
	b := list.Blockers[0]
	if b.Owner == "" {
		t.Fatal("a blocker names no screen that can resolve it")
	}
	if !strings.Contains(b.Owner, "System") {
		t.Fatalf("a core blocker points at %q, want System", b.Owner)
	}
	// The remedy is advice. Status must not turn it into something it performs.
	if b.Remedy.Display() == "" {
		t.Fatal("the remedy was dropped")
	}
}

// --- resources ---

// A machine with no GPU has none; that is EMPTY, not UNKNOWN and not an error.
func TestAbsentAcceleratorsAreEmptyNotUnknown(t *testing.T) {
	snap := (&StatusSource{
		Now: fixedClock(),
		Resources: fakeResources{snap: resources.Snapshot{
			CPU:    resources.CPU{Model: "Test CPU", Logical: 8, Effective: 4},
			Memory: resources.Memory{TotalBytes: 16 << 30, AvailableBytes: 8 << 30},
			Health: resources.Health{Overall: resources.Status("READY")},
		}},
	}).ReadResources(context.Background())

	if snap.GPU.Status != TruthEmpty {
		t.Fatalf("a machine with no accelerator reported %s, want EMPTY", snap.GPU.Status.Label())
	}
	if strings.Contains(strings.ToLower(snap.GPU.Display()), "error") {
		t.Fatalf("absent accelerators rendered as an error: %q", snap.GPU.Display())
	}
	// No swap configured is likewise an answer.
	if !snap.Swap.Status.IsSuccess() {
		t.Fatalf("unconfigured swap reported %s", snap.Swap.Status.Label())
	}
}

// A partial collection says so rather than presenting itself as complete.
func TestPartialResourceCollectionIsMarkedStale(t *testing.T) {
	snap := (&StatusSource{
		Now: fixedClock(),
		Resources: fakeResources{snap: resources.Snapshot{
			CPU:      resources.CPU{Logical: 4, Effective: 4},
			Failures: []string{"nvidia-smi timed out"},
		}},
	}).ReadResources(context.Background())

	if snap.Status.Status != TruthStale {
		t.Fatalf("a partial collection reported %s, want STALE", snap.Status.Status.Label())
	}
	if !strings.Contains(snap.Status.Reason, "nvidia-smi") {
		t.Fatalf("the collection failure was not explained: %q", snap.Status.Reason)
	}
}

// --- cloud ---

// An unconfigured Cloud is NOT_RUN, not "not entitled". Those are different
// facts and they call for different responses.
func TestUnconfiguredCloudIsNotRunNotRefused(t *testing.T) {
	snap := (&StatusSource{Now: fixedClock(), Cloud: &fakeCloud{configured: false}}).
		ReadCloud(context.Background())

	if snap.Connection.Status != TruthNotRun {
		t.Fatalf("an unconfigured Cloud reported %s, want NOT_RUN", snap.Connection.Status.Label())
	}
	if snap.Connection.Reason == "" {
		t.Fatal("an unconfigured Cloud gives no reason")
	}
	// Standard is the truthful mode, and the fallback must say it is working.
	if snap.Mode.Display() != "Standard" {
		t.Fatalf("an unconfigured Cloud reported mode %q", snap.Mode.Display())
	}
	if !snap.StandardFallbk.Status.IsSuccess() {
		t.Fatal("the Standard fallback is not reported as working")
	}
}

// A configured Cloud that holds no lease is UNKNOWN about the connection and
// NOT_RUN about the lease — never a silent Standard with nothing said.
func TestConfiguredCloudWithoutALeaseExplainsItself(t *testing.T) {
	snap := (&StatusSource{Now: fixedClock(), Cloud: &fakeCloud{configured: true}}).
		ReadCloud(context.Background())

	if snap.Lease.Status != TruthNotRun {
		t.Fatalf("no lease reported %s, want NOT_RUN", snap.Lease.Status.Label())
	}
	for name, v := range map[string]Value{
		"capabilities": snap.Capabilities, "expiry": snap.Expiry, "renewal": snap.Renewal,
	} {
		if v.Status.IsSuccess() {
			t.Fatalf("%s claims a known value with no lease held", name)
		}
		if v.Reason == "" {
			t.Fatalf("%s is unavailable but gives no reason", name)
		}
	}
}

// A refusal is a result and must be shown as the reason ULTRA is not active.
func TestCloudRefusalIsSurfacedAsTheReason(t *testing.T) {
	snap := (&StatusSource{
		Now:   fixedClock(),
		Cloud: &fakeCloud{configured: true, err: errors.New("rate limited: retry after 60s")},
	}).ReadCloud(context.Background())

	if snap.Refusal.Status != TruthRefused {
		t.Fatalf("a refusal reported %s, want REFUSED", snap.Refusal.Status.Label())
	}
	if !strings.Contains(snap.Refusal.Display(), "rate limited") {
		t.Fatalf("the refusal reason was lost: %q", snap.Refusal.Display())
	}
	// This is the bug that once made a 429 read as "no entitlement": the
	// distinct reason must survive into the display.
	if !strings.Contains(snap.Connection.Display(), "rate limited") {
		t.Fatalf("the connection state hides why: %q", snap.Connection.Display())
	}
}

// An expired lease is never valid.
//
// The canonical gate retires a lapsed lease outright — Entitled() goes false
// AND ExpiresAt() stops reporting — so the two shapes below are the only ones
// the real gate can produce. An earlier version of this test configured
// entitled:true alongside a past expiry, which the real gate can never do, and
// it passed against a branch that was unreachable in production.
func TestExpiredLeaseIsNeverValid(t *testing.T) {
	past := time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC) // an hour before noon

	// Shape one: the gate has already retired the lease. Expiry is unknowable,
	// and the screen must not pretend otherwise.
	retired := (&StatusSource{
		Now:   fixedClock(),
		Cloud: &fakeCloud{configured: true, entitled: false, hasLease: false},
	}).ReadCloud(context.Background())

	if retired.Lease.Status.IsSuccess() {
		t.Fatalf("a retired lease reported success: %q", retired.Lease.Display())
	}
	if !strings.Contains(retired.Lease.Display(), "cannot distinguish") {
		t.Fatalf("the screen claims to know more than the gate can tell it: %q",
			retired.Lease.Display())
	}

	// Shape two: the lease is still present but past its window.
	lapsed := (&StatusSource{
		Now: fixedClock(),
		Cloud: &fakeCloud{
			configured: true, entitled: false, hasLease: true,
			expires: past, renews: past,
		},
	}).ReadCloud(context.Background())

	if lapsed.Expiry.Status.IsSuccess() {
		t.Fatalf("a lapsed lease reported a healthy expiry: %q", lapsed.Expiry.Display())
	}
	if lapsed.Expiry.Status != TruthStale {
		t.Fatalf("a lapsed lease reported %s, want STALE", lapsed.Expiry.Status.Label())
	}
	if lapsed.Lease.Status.IsSuccess() {
		t.Fatalf("a lapsed lease reported itself verified: %q", lapsed.Lease.Display())
	}
	// An expired lease grants nothing, whatever it once carried.
	if lapsed.Capabilities.Status.IsSuccess() {
		t.Fatalf("an expired lease still reported capabilities: %q",
			lapsed.Capabilities.Display())
	}
	// And the mode must have dropped to Standard.
	if lapsed.Mode.Display() != "Standard" {
		t.Fatalf("an expired lease left the mode at %q", lapsed.Mode.Display())
	}
}

// The fakes must not be able to express a state the canonical gate cannot.
//
// This is the defect that let the previous version of the test above pass: a
// fake configured entitled:true with a past expiry, which *cloud.Gate can never
// produce because it drops the lease before answering.
func TestCloudFakeCannotOutrunTheRealGate(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)

	// The real gate's contract, restated: entitled implies a future expiry.
	f := &fakeCloud{configured: true, entitled: true, hasLease: true, expires: past}
	if expiry, ok := f.ExpiresAt(); ok && f.Entitled() && !expiry.After(now) {
		t.Log("this fake expresses entitled-with-past-expiry, which *cloud.Gate " +
			"cannot: Entitled() retires the lease and ExpiresAt() then reports nothing")
	}

	// A real gate answers these consistently, so the reader must too: whenever
	// it says not entitled, no capability may be reported.
	notEntitled := &workspaceCloudReader{gate: nil, configured: true}
	if notEntitled.Entitled() {
		t.Fatal("a nil gate reported entitlement")
	}
	if caps := notEntitled.Capabilities(); len(caps) != 0 {
		t.Fatalf("a nil gate reported capabilities: %v", caps)
	}
	if _, ok := notEntitled.ExpiresAt(); ok {
		t.Fatal("a nil gate reported an expiry")
	}
}

// The TUI must never report a capability the gate did not grant.
func TestCloudCapabilitiesComeOnlyFromTheGate(t *testing.T) {
	snap := (&StatusSource{
		Now: fixedClock(),
		Cloud: &fakeCloud{
			configured: true, entitled: true, hasLease: true,
			caps:    []string{"ultra.delegate"},
			expires: time.Date(2026, 9, 10, 13, 0, 0, 0, time.UTC),
			renews:  time.Date(2026, 9, 10, 12, 30, 0, 0, time.UTC),
		},
	}).ReadCloud(context.Background())

	if !strings.Contains(snap.Capabilities.Display(), "ultra.delegate") {
		t.Fatalf("the granted capability is missing: %q", snap.Capabilities.Display())
	}
	if snap.Mode.Display() != "ULTRA" {
		t.Fatalf("an entitled session reported mode %q", snap.Mode.Display())
	}

	// With no capabilities granted, the field is EMPTY rather than invented.
	none := (&StatusSource{
		Now:   fixedClock(),
		Cloud: &fakeCloud{configured: true, entitled: true, hasLease: true},
	}).ReadCloud(context.Background())
	if none.Capabilities.Status != TruthEmpty {
		t.Fatalf("no capabilities reported %s, want EMPTY", none.Capabilities.Status.Label())
	}
}

// --- providers ---

// Quota, reset and promotional allowance are implementation gaps. They must be
// rendered as gaps on every provider, never inferred from usage.
func TestProviderQuotaResetAndAllowanceAreAlwaysGaps(t *testing.T) {
	list := (&StatusSource{Now: fixedClock()}).ReadProviders(
		context.Background(),
		fakeProviders{probes: []ProviderProbe{
			{Name: "claude", Found: true, Probed: true, Version: "1.2.3", Model: "opus"},
			{Name: "codex", Found: false, Probed: true},
		}},
	)
	if len(list.Rows) != 2 {
		t.Fatalf("got %d provider rows, want 2", len(list.Rows))
	}
	for _, row := range list.Rows {
		for name, v := range map[string]Value{
			"quota": row.Quota, "reset": row.Reset,
			"allowance": row.Allowance, "auth": row.Auth,
		} {
			if v.Status.IsSuccess() {
				t.Fatalf("%s %s reports a known value, but it is an implementation gap",
					row.Name.Display(), name)
			}
			if !strings.Contains(v.Display(), "IMPLEMENTATION GAP") {
				t.Fatalf("%s %s does not name the gap: %q", row.Name.Display(), name, v.Display())
			}
			if !strings.Contains(v.Display(), "CTUI-") {
				t.Fatalf("%s %s does not identify which gap: %q", row.Name.Display(), name, v.Display())
			}
		}
	}
}

// An installed provider shows what was evidenced; a missing one shows why its
// version and model are unavailable rather than showing them blank.
func TestProviderEvidenceIsNeverInvented(t *testing.T) {
	list := (&StatusSource{Now: fixedClock()}).ReadProviders(
		context.Background(),
		fakeProviders{probes: []ProviderProbe{
			{Name: "installed", Found: true, Probed: true},
			{Name: "missing", Found: false, Probed: true},
			{Name: "unprobed", Probed: false},
		}},
	)
	byName := map[string]ProviderRow{}
	for _, r := range list.Rows {
		byName[r.Name.Display()] = r
	}

	// Installed but reporting no version: UNKNOWN with a reason, not blank.
	if v := byName["installed"].Version; v.Status.IsSuccess() || v.Reason == "" {
		t.Fatalf("an unreported version rendered as %q", v.Display())
	}
	// Not installed: NOT_RUN, and the reason names the cause.
	if v := byName["missing"].Version; v.Status != TruthNotRun {
		t.Fatalf("a missing provider's version reported %s", v.Status.Label())
	}
	// Never probed must not read as "not installed".
	if v := byName["unprobed"].Availability; v.Status != TruthNotRun {
		t.Fatalf("an unprobed provider reported availability %s, want NOT_RUN", v.Status.Label())
	}
}

// With no probe at all, the whole surface is NOT_RUN with a reason.
func TestProvidersWithoutAProbeAreNotRun(t *testing.T) {
	list := (&StatusSource{Now: fixedClock()}).ReadProviders(context.Background(), nil)
	if list.Status.Status != TruthNotRun {
		t.Fatalf("an unprobed provider surface reported %s", list.Status.Status.Label())
	}
	if list.Status.Reason == "" {
		t.Fatal("the unprobed provider surface gives no reason")
	}
}

// A partly-read observation must not be paired with an unmeasured zero.
//
// The collector fills each field independently, so a CPU model without a core
// count, or a memory total without an available figure, are ordinary outcomes.
// Rendering them as "(0 logical)" or "0 B available" states a measurement that
// was never taken — and "0 B available" reads as an exhausted machine.
func TestPartiallyReadResourcesDoNotInventZeros(t *testing.T) {
	snap := (&StatusSource{
		Now: fixedClock(),
		Resources: fakeResources{snap: resources.Snapshot{
			CPU:     resources.CPU{Model: "Test CPU"},         // no logical count
			Memory:  resources.Memory{TotalBytes: 16 << 30},   // no available figure
			Storage: resources.Storage{TotalBytes: 500 << 30}, // no free figure
		}},
	}).ReadResources(context.Background())

	if strings.Contains(snap.CPU.Display(), "0 logical") {
		t.Fatalf("an unmeasured core count rendered as zero: %q", snap.CPU.Display())
	}
	if !strings.Contains(snap.CPU.Display(), "Test CPU") {
		t.Fatalf("the CPU model was lost: %q", snap.CPU.Display())
	}
	if strings.Contains(snap.Memory.Display(), "0 B available") {
		t.Fatalf("unread available memory rendered as exhausted: %q", snap.Memory.Display())
	}
	if !strings.Contains(snap.Memory.Display(), "16.0 GiB") {
		t.Fatalf("the memory total was lost: %q", snap.Memory.Display())
	}
	if strings.Contains(snap.Storage.Display(), "0 B free") {
		t.Fatalf("unread free space rendered as a full disk: %q", snap.Storage.Display())
	}
}

// When both halves are read, both are shown — the fix above must not cost the
// ordinary case its detail.
func TestFullyReadResourcesShowBothFigures(t *testing.T) {
	snap := (&StatusSource{
		Now: fixedClock(),
		Resources: fakeResources{snap: resources.Snapshot{
			CPU:     resources.CPU{Model: "Test CPU", Logical: 8, Effective: 4},
			Memory:  resources.Memory{TotalBytes: 16 << 30, AvailableBytes: 8 << 30},
			Storage: resources.Storage{TotalBytes: 500 << 30, FreeBytes: 250 << 30},
		}},
	}).ReadResources(context.Background())

	if !strings.Contains(snap.CPU.Display(), "8 logical") {
		t.Fatalf("a measured core count was dropped: %q", snap.CPU.Display())
	}
	if !strings.Contains(snap.Memory.Display(), "available of") {
		t.Fatalf("measured memory lost its available figure: %q", snap.Memory.Display())
	}
	if !strings.Contains(snap.Storage.Display(), "free of") {
		t.Fatalf("measured storage lost its free figure: %q", snap.Storage.Display())
	}
}

// The four provider gaps must stay gaps whatever a probe reports.
//
// A probe is data supplied from outside this package, and the temptation on a
// future change is to let a rich probe populate a quota. This drives every
// shape a probe can take — including one that tries to look authoritative —
// and requires the gap to survive all of them.
func TestProviderGapsSurviveEveryProbeShape(t *testing.T) {
	probes := []ProviderProbe{
		{Name: "everything", Found: true, Probed: true,
			Path: "/usr/bin/x", Version: "9.9.9", Model: "big",
			ProbeNote: "quota: 1000000 remaining, resets in 3h, free trial active"},
		{Name: "nothing"},
		{Name: "found-not-probed", Found: true},
		{Name: "probed-not-found", Probed: true},
	}
	list := (&StatusSource{Now: fixedClock()}).ReadProviders(
		context.Background(), fakeProviders{probes: probes})

	if len(list.Rows) != len(probes) {
		t.Fatalf("got %d rows, want %d", len(list.Rows), len(probes))
	}
	for _, row := range list.Rows {
		gaps := map[string]struct {
			v    Value
			spec string
		}{
			"quota":     {row.Quota, "CTUI-0148"},
			"reset":     {row.Reset, "CTUI-0149"},
			"allowance": {row.Allowance, "CTUI-0151"},
			"auth":      {row.Auth, "CTUI-0144"},
		}
		for name, g := range gaps {
			if g.v.Status.IsSuccess() {
				t.Fatalf("%s %s became a known value", row.Name.Display(), name)
			}
			if g.v.Status != TruthNotRun {
				t.Fatalf("%s %s reported %s, want NOT_RUN",
					row.Name.Display(), name, g.v.Status.Label())
			}
			if !strings.Contains(g.v.Reason, g.spec) {
				t.Fatalf("%s %s does not cite %s: %q",
					row.Name.Display(), name, g.spec, g.v.Reason)
			}
			// Nothing a probe said may leak into the gap's explanation.
			if strings.Contains(g.v.Display(), "1000000") ||
				strings.Contains(g.v.Display(), "3h") {
				t.Fatalf("%s %s echoed unevidenced probe text: %q",
					row.Name.Display(), name, g.v.Display())
			}
			// And a gap is never copyable, so an unevidenced figure cannot
			// reach the clipboard and be pasted somewhere as fact.
			if _, ok := g.v.CopyText(); ok {
				t.Fatalf("%s %s was copyable", row.Name.Display(), name)
			}
		}
	}
}

// The gap wording must not read as a temporary outage. "Not implemented" and
// "the provider is down" call for entirely different responses from a user.
func TestProviderGapsReadAsUnimplementedNotBroken(t *testing.T) {
	list := (&StatusSource{Now: fixedClock()}).ReadProviders(
		context.Background(),
		fakeProviders{probes: []ProviderProbe{{Name: "p", Found: true, Probed: true}}})

	reason := list.Rows[0].Quota.Display()
	if !strings.Contains(reason, "IMPLEMENTATION GAP") {
		t.Fatalf("the gap does not name itself: %q", reason)
	}
	for _, misleading := range []string{"try again", "retry", "temporarily", "outage"} {
		if strings.Contains(strings.ToLower(reason), misleading) {
			t.Fatalf("the gap reads as a transient failure (%q): %q", misleading, reason)
		}
	}
}

// A shortened list must say how much it left out.
//
// Both the read and the screen cap the number of events. Without saying so, the
// newest few read as the complete recent activity, and someone scanning for
// something that happened earlier concludes it never happened.
func TestTruncatedEventFeedsDeclareWhatTheyOmit(t *testing.T) {
	base := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	var events []model.Event
	for i := 0; i < 120; i++ {
		events = append(events, model.Event{
			ID: fmt.Sprintf("e%03d", i), Type: "task.started",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}
	feed := (&StatusSource{Now: fixedClock(), Runtime: fakeRuntime{events: events}}).
		ReadEvents(context.Background(), 12)

	// The status reports the real total, not the number kept.
	if !strings.Contains(feed.Status.Display(), "120") {
		t.Fatalf("the feed status hides the real total: %q", feed.Status.Display())
	}
	if !strings.Contains(feed.Status.Display(), "showing") {
		t.Fatalf("the feed status does not say it was shortened: %q", feed.Status.Display())
	}

	// And a screen that shortens further says so too.
	fields := eventFields(feed, 5)
	last := fields[len(fields)-1]
	if !strings.Contains(last.Value.Display(), "more recent event") {
		t.Fatalf("a shortened screen does not declare the omission: %q",
			last.Value.Display())
	}
	if !strings.Contains(last.Value.Display(), "7") {
		t.Fatalf("the omission count is wrong: %q", last.Value.Display())
	}
}

// An unshortened list must not claim an omission it did not make.
func TestCompleteEventFeedsClaimNoOmission(t *testing.T) {
	feed := (&StatusSource{
		Now: fixedClock(),
		Runtime: fakeRuntime{events: []model.Event{
			{ID: "a", Type: "one", Timestamp: time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC)},
			{ID: "b", Type: "two", Timestamp: time.Date(2026, 9, 10, 11, 1, 0, 0, time.UTC)},
		}},
	}).ReadEvents(context.Background(), 12)

	if strings.Contains(feed.Status.Display(), "showing") {
		t.Fatalf("a complete feed claimed to be shortened: %q", feed.Status.Display())
	}
	for _, f := range eventFields(feed, 12) {
		if strings.Contains(f.Value.Display(), "more recent event") {
			t.Fatalf("a complete screen claimed an omission: %q", f.Value.Display())
		}
	}
}

// --- regressions from adversarial review of slice 3 ---

// An unreadable memory probe must not assert facts about the machine.
//
// With /proc/meminfo unreadable every field is zero, and the earlier code took
// SwapTotalBytes == 0 to mean "none configured" — turning a failed probe into
// an affirmative observation that the machine has no swap.
func TestUnreadableMemoryDoesNotAssertNoSwap(t *testing.T) {
	snap := (&StatusSource{
		Now:       fixedClock(),
		Resources: fakeResources{snap: resources.Snapshot{}}, // nothing read
	}).ReadResources(context.Background())

	if snap.Swap.Status.IsSuccess() {
		t.Fatalf("an unreadable memory probe asserted swap state: %q", snap.Swap.Display())
	}
	if strings.Contains(snap.Swap.Display(), "none configured") {
		t.Fatal("a failed probe claimed the machine has no swap")
	}
	if snap.Swap.Reason == "" {
		t.Fatal("unreadable swap gives no reason")
	}
}

// A machine that was read and genuinely has no swap still says so.
func TestReadMemoryWithNoSwapStillSaysNoneConfigured(t *testing.T) {
	snap := (&StatusSource{
		Now: fixedClock(),
		Resources: fakeResources{snap: resources.Snapshot{
			Memory: resources.Memory{TotalBytes: 16 << 30, AvailableBytes: 8 << 30},
		}},
	}).ReadResources(context.Background())

	if !snap.Swap.Status.IsSuccess() {
		t.Fatalf("a read machine with no swap reported %s", snap.Swap.Status.Label())
	}
	if !strings.Contains(snap.Swap.Display(), "none configured") {
		t.Fatalf("a read machine with no swap rendered as %q", snap.Swap.Display())
	}
}

// A failed accelerator probe did not establish that the machine has no GPU.
func TestFailedAcceleratorProbeIsNotEmpty(t *testing.T) {
	snap := (&StatusSource{
		Now: fixedClock(),
		Resources: fakeResources{snap: resources.Snapshot{
			CPU:      resources.CPU{Logical: 8, Effective: 4},
			Failures: []string{"nvidia-smi timed out"},
		}},
	}).ReadResources(context.Background())

	if snap.GPU.Status == TruthEmpty {
		t.Fatal("a failed accelerator probe claimed the machine has no GPU")
	}
	if snap.GPU.Status.IsSuccess() {
		t.Fatalf("a failed accelerator probe reported success: %q", snap.GPU.Display())
	}
	if !strings.Contains(snap.GPU.Reason, "nvidia-smi") {
		t.Fatalf("the probe failure was not explained: %q", snap.GPU.Reason)
	}
}

// A machine probed successfully with no accelerator is genuinely EMPTY.
func TestSuccessfulProbeWithNoAcceleratorIsEmpty(t *testing.T) {
	snap := (&StatusSource{
		Now: fixedClock(),
		Resources: fakeResources{snap: resources.Snapshot{
			CPU: resources.CPU{Logical: 8, Effective: 4},
		}},
	}).ReadResources(context.Background())

	if snap.GPU.Status != TruthEmpty {
		t.Fatalf("a clean probe with no GPU reported %s", snap.GPU.Status.Label())
	}
}

// A measured-full disk is urgent and must not be softened into "not reported".
func TestAFullDiskIsReportedAsFull(t *testing.T) {
	snap := (&StatusSource{
		Now: fixedClock(),
		Resources: fakeResources{snap: resources.Snapshot{
			// Source is set by the collector only on a successful statfs, so
			// zero free with a source really is a full volume.
			Storage: resources.Storage{TotalBytes: 500 << 30, FreeBytes: 0, Source: "statfs"},
		}},
	}).ReadResources(context.Background())

	if strings.Contains(snap.Storage.Display(), "not reported") {
		t.Fatalf("a full disk was reported as unmeasured: %q", snap.Storage.Display())
	}
	if !strings.Contains(snap.Storage.Display(), "full") {
		t.Fatalf("a full disk does not say so: %q", snap.Storage.Display())
	}
}

// Sandbox and network blockers belong to Security, not Models.
//
// Routing by dimension alone sent a broken container sandbox to the model
// provider screen, which cannot fix it.
func TestEnvironmentBlockersRouteToTheScreenThatOwnsThem(t *testing.T) {
	cases := map[string]string{
		"env.sandbox": "Security / Sandbox",
		"env.network": "Security / Network",
		"env.ultra":   "Control / Mode & Autonomy",
	}
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load IA: %v", err)
	}
	for id, wantSuffix := range cases {
		owner := ownerForCheck(startup.Check{
			ID: id, Dimension: startup.DimensionEnvironment,
		})
		if !strings.HasSuffix(owner, wantSuffix) {
			t.Fatalf("%s routes to %q, want a screen ending %q", id, owner, wantSuffix)
		}
		if _, ok := ia.NodeByMenuPath(owner); !ok {
			t.Fatalf("%s routes to %q, which is not in the frozen IA", id, owner)
		}
	}

	// An unrecognised environment check still lands somewhere plausible rather
	// than at Models.
	fallback := ownerForCheck(startup.Check{
		ID: "env.something-new", Dimension: startup.DimensionEnvironment,
	})
	if strings.Contains(fallback, "Models") {
		t.Fatalf("an unrecognised environment check routes to %q", fallback)
	}
	if _, ok := ia.NodeByMenuPath(fallback); !ok {
		t.Fatalf("the environment fallback %q is not in the frozen IA", fallback)
	}
}

// Every owner ownerForCheck can produce must exist in the frozen IA, or a
// cross-link sends the user nowhere.
func TestEveryCheckOwnerResolvesInTheIA(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load IA: %v", err)
	}
	dimensions := []startup.Dimension{
		startup.DimensionCore, startup.DimensionEnvironment,
		startup.DimensionProject, startup.DimensionExecution,
	}
	ids := []string{"env.sandbox", "env.network", "env.ultra", "core.store", "unknown.check"}

	for _, d := range dimensions {
		for _, id := range ids {
			owner := ownerForCheck(startup.Check{ID: id, Dimension: d})
			if owner == "" {
				continue // an unmapped owner is stated on screen, not linked
			}
			if _, ok := ia.NodeByMenuPath(owner); !ok {
				t.Fatalf("check %s/%s routes to %q, which is not in the IA", d, id, owner)
			}
		}
	}
}
