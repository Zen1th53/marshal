package tui

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/store"
)

// e2eRunID is the run the end-to-end Board test seeds and follows.
const e2eRunID = "R-e2e1"

// e2eRun is a realistic run: tasks merged, accepted, handed in, out with
// workers, escalated and queued, with dependencies between them.
func e2eRun() marshal.Run {
	return marshal.Run{
		PlanID: "PLAN-e2e", PlanVersion: 2, ApprovalScopeDigest: "d", BaseCommit: "base", Tier: marshal.Ultra, Settings: marshal.DefaultSettings(), State: marshal.Dispatching,
		Tasks: []marshal.Task{
			{PlanTaskID: "T-101", Title: "events list", Worker: "codex", State: marshal.Merged, Files: []string{"internal/mcp/events_list.go"}},
			{PlanTaskID: "T-102", Title: "run digest", Worker: "claude", State: marshal.Accepted, DependsOn: []string{"T-101"}, Files: []string{"internal/mcp/digest.go"}},
			{PlanTaskID: "T-103", Title: "memory bridge", Worker: "codex", State: marshal.HandedIn, DependsOn: []string{"T-101"}, Files: []string{"internal/app/memory_bridge.go"},
				Criteria: []string{"bridge reads durable records"}, Checks: []marshal.Check{{Command: "go test ./internal/app", Criteria: []string{"bridge reads durable records"}}}},
			{PlanTaskID: "T-104", Title: "memory recall", Worker: "codex", State: marshal.Dispatched, DependsOn: []string{"T-102"}, Files: []string{"internal/mcp/memory_recall.go"}},
			{PlanTaskID: "T-105", Title: "candidates", Worker: "claude", State: marshal.Dispatched, Files: []string{"internal/app/memory_candidates.go"}},
			{PlanTaskID: "T-106", Title: "npm broker", Worker: "codex", State: marshal.Escalated, Files: []string{"internal/netpolicy/broker_npm.go"}},
			{PlanTaskID: "T-107", Title: "migrations", State: marshal.Queued, DependsOn: []string{"T-103", "T-104"}, Files: []string{"internal/store/migrations_canonical.go"}},
		},
	}
}

// e2eStore is the real store the e2e workspace runs on, with a second
// connection for the rows the store writes only through flows a test cannot
// drive (owner-signed architecture acceptances, plan versions, leases).
type e2eStore struct {
	st      *store.Store
	db      *sql.DB
	project string // the workspace's project
	memory  string // the runtime's memory project
	start   time.Time
	n       int
}

func (s *e2eStore) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := s.db.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// decide appends one Marshal decision for the run.
func (s *e2eStore) decide(t *testing.T, kind events.EventType, task string, at time.Time, data map[string]any) {
	t.Helper()
	s.n++
	if _, err := s.st.AppendMarshalDecision(t.Context(), events.Event{ID: fmt.Sprintf("EVENT-E2E-%03d", s.n), Type: kind, Subject: s.project, RunID: e2eRunID, TaskID: task, At: at, Data: data}); err != nil {
		t.Fatalf("decision %s: %v", kind, err)
	}
}

// seedE2EStore writes the run and everything each view reads into the
// workspace's real store.
func seedE2EStore(t *testing.T, w *Workspace, rt *app.Runtime) *e2eStore {
	t.Helper()
	ctx := t.Context()
	layout, err := project.Discover(rt.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := &e2eStore{st: rt.Store(), db: db, project: w.projectID, memory: rt.ProjectID(), start: time.Now().Add(-6 * time.Minute)}

	if err := s.st.SaveMarshalState(ctx, s.project, e2eRunID, e2eRun(), 0, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	// Decisions: approval, dispatches, usage, hand-in, escalation.
	at := s.start
	s.decide(t, events.EventTypeMarshalPlanApproved, "", at, nil)
	for i, d := range []struct{ task, worker string }{{"T-101", "codex"}, {"T-102", "claude"}, {"T-103", "codex"}, {"T-104", "codex"}, {"T-105", "claude"}, {"T-106", "codex"}} {
		s.decide(t, events.EventTypeMarshalTaskDispatched, d.task, at.Add(time.Duration(10+i)*time.Second), map[string]any{"worker": d.worker})
	}
	s.decide(t, events.EventTypeMarshalUsageCharged, "T-104", at.Add(time.Minute), map[string]any{"charge_phase": "worker", "usage_known": true, "usage_units": float64(12000)})
	s.decide(t, events.EventTypeMarshalTaskHandedIn, "T-103", at.Add(2*time.Minute), map[string]any{"reason": "hand-in with 3 evidence refs"})
	s.decide(t, events.EventTypeMarshalEscalated, "T-106", at.Add(3*time.Minute), map[string]any{"reason": "egress registry.npmjs.org outside manifest"})

	// Verification: hand-ins, cross-provider reviews and the verifier.
	for _, h := range []struct {
		task   string
		handin marshal.HandIn
		review *marshal.Review
	}{
		{"T-101", marshal.HandIn{ResultCommit: "r1", Provider: "codex", Claims: []string{"events_list pages by sequence"}},
			&marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal", Independent: &marshal.IndependentReview{Verdict: marshal.VerdictAccept, Reviewer: "claude-2", Provider: "claude", EvidenceRefs: []string{"internal/mcp/events_list.go:61"}}}},
		{"T-102", marshal.HandIn{ResultCommit: "r2", Provider: "claude", Claims: []string{"digest is stable"}},
			&marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal", Independent: &marshal.IndependentReview{Verdict: marshal.VerdictAccept, Reviewer: "codex-2", Provider: "codex", EvidenceRefs: []string{"verify log a91f2c"}}}},
		{"T-103", marshal.HandIn{ResultCommit: "r3", Provider: "codex", Claims: []string{"restore keeps tags"},
			CheckResults: []marshal.CheckResult{{Command: "go test ./internal/app", Criteria: []string{"bridge reads durable records"}, Passed: true, ResultCommit: "r3"}}},
			&marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal", Independent: &marshal.IndependentReview{Verdict: marshal.VerdictReturn, Reviewer: "claude-1", Provider: "claude", Reasons: []string{"no test for tags"}}}},
	} {
		if _, err := s.st.SetMarshalHandIn(ctx, e2eRunID, h.task, 1, h.handin); err != nil {
			t.Fatal(err)
		}
		if _, err := s.st.SetMarshalReview(ctx, e2eRunID, h.task, 1, *h.review); err != nil {
			t.Fatal(err)
		}
	}
	s.decide(t, events.EventTypeMarshalVerifierRecorded, "", at.Add(4*time.Minute), map[string]any{"verifier": marshal.VerifierEvidence{Reviewer: "agy-v", Provider: "agy", Verdict: "pass", Commit: "a91f2c"}})

	// Provider escalation: a refused egress the owner has been asked about.
	for i, e := range []struct {
		kind events.EventType
		data map[string]any
	}{
		{events.EventTypeNetworkEgressAttempt, map[string]any{"endpoint": "github.com:443", "allowed": true, "rule_id": "manifest"}},
		{events.EventTypeNetworkEgressAttempt, map[string]any{"endpoint": "registry.npmjs.org:443", "allowed": false, "reason": "outside manifest"}},
		{events.EventTypeNetworkEgressNotification, map[string]any{"endpoint": "registry.npmjs.org:443", "requestable": true, "message": "codex-3 wants to reach registry.npmjs.org:443. Allow?"}},
	} {
		e.data["parent_run_id"] = e2eRunID
		if _, err := s.st.Append(ctx, events.Event{ID: fmt.Sprintf("EVENT-E2E-EGRESS-%d", i), Type: e.kind, Subject: "codex-3", RunID: "RUN-EGRESS-e2e", TaskID: "T-106", At: at.Add(time.Duration(200+i) * time.Second), Data: e.data}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	s.exec(t, `INSERT INTO tasks(task_id,project_id,title,status,risk,revision,created_at,updated_at) VALUES('TASK-E2E',?,'t','proposed','R1',0,?,?)`, s.project, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	s.exec(t, `INSERT INTO secret_leases(lease_id,subject,task_id,provider,secret_name,secret_version,purpose,issued_at,expires_at,revoked_at,state) VALUES('L-e2e','codex-1','TASK-E2E','env','GITHUB_TOKEN','1','github read token',?,?,NULL,'leased')`,
		now.Add(-time.Hour).Format(time.RFC3339Nano), now.Add(20*time.Minute).Format(time.RFC3339Nano))

	// Providers: harness profiles, one fresh and one expired.
	for _, p := range []model.HarnessProfile{
		{Harness: "codex", InstalledVersion: "0.28.0", BinaryPath: "codex", SupportedModels: []string{"gpt-5.5"}, DefaultModel: "gpt-5.5", ProbeEvidenceID: "ev-codex", ProbedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(22 * time.Hour)},
		{Harness: "claude", InstalledVersion: "2.1.0", BinaryPath: "claude", SupportedModels: []string{"opus"}, DefaultModel: "opus", ProbeEvidenceID: "ev-claude", ProbedAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-24 * time.Hour)},
	} {
		if err := s.st.SaveHarnessProfile(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	// Memory: durable records and candidates waiting review.
	for i, r := range []struct {
		id, title string
		kind      model.MemoryKind
		life      model.MemoryLifecycle
	}{
		{"mem-e2e-1", "store migrations run in order", model.MemoryKindSemantic, model.MemoryDurable},
		{"mem-e2e-2", "npm needs the broker", model.MemoryKindFailure, model.MemoryCandidate},
		{"mem-e2e-3", "bridge reads durable records only", model.MemoryKindSemantic, model.MemoryCandidate},
	} {
		ts := s.start.Add(time.Duration(i) * time.Minute).UTC()
		rec := model.MemoryRecordV2{ID: r.id, ProjectID: s.memory, Kind: r.kind, Lifecycle: r.life, Confidence: model.ConfidenceObserved, Authority: model.AuthorityAgent,
			Title: r.title, Body: r.title + ".", Scope: "project", ScopeID: s.memory, ObservedAt: ts, IngestedAt: ts, ValidFrom: ts, CreatedAt: ts}
		if err := s.st.WriteMemoryV2(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	// Plan: two versions, the second approved and current.
	for _, v := range []struct {
		version, supersedes int64
		state, reason       string
	}{{1, 0, "STALE", ""}, {2, 1, "APPROVED", "added T-107 migrations"}} {
		s.exec(t, `INSERT INTO execution_plans(plan_id,version,project_id,goal_id,goal_revision,state,mode,constitution_version,supersedes,revision_reason,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			"PLAN-e2e", v.version, s.project, "goal", 1, v.state, "ultra", "v1", v.supersedes, v.reason, s.start.Add(time.Duration(v.version)*time.Minute).UTC().Format(time.RFC3339Nano))
	}

	// Architecture: the owner's signed acceptance of revision 7.
	s.exec(t, `INSERT OR IGNORE INTO architecture_target_generations(project_id,generation,enrollment_id,enrollment) VALUES(?,1,'enrol-e2e',x'7b7d')`, s.project)
	s.exec(t, `INSERT INTO architecture_owner_nonces(nonce,project_id,generation,payload_digest,expires_unix) VALUES('nonce-e2e',?,1,'pd',0)`, s.project)
	var receipt store.SignedArchitectureReceipt
	receipt.Challenge.Binding.ArchitectureID, receipt.Challenge.Binding.Revision, receipt.Challenge.Binding.Digest = "ARCH-1", 7, "a3f1c9e2"
	receipt.Challenge.Message = "accept architecture"
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	s.exec(t, `INSERT INTO architecture_accepted_revisions(receipt_id,receipt_digest,project_id,generation,nonce,receipt) VALUES('rcpt-e2e','d-rcpt-e2e',?,1,'nonce-e2e',?)`, s.project, raw)
	s.exec(t, `INSERT INTO architecture_owner_audit(receipt_id,project_id,generation,message_id,session_id,origin,accepted_at) VALUES('rcpt-e2e',?,1,'m','s','owner_command',?)`, s.project, s.start.UTC().Format(time.RFC3339Nano))
	return s
}

// e2eReads counts the reads each view makes of the real store.
type e2eReads struct{ mem, prov, ver, sec, plan, arch atomic.Int64 }

func (r *e2eReads) String() string {
	return fmt.Sprintf("memory %d, providers %d, verify %d, security %d, plan %d, architecture %d", r.mem.Load(), r.prov.Load(), r.ver.Load(), r.sec.Load(), r.plan.Load(), r.arch.Load())
}

func (r *e2eReads) total() int64 {
	return r.mem.Load() + r.prov.Load() + r.ver.Load() + r.sec.Load() + r.plan.Load() + r.arch.Load()
}

type countedMemory struct {
	memorySource
	n *atomic.Int64
}

func (c countedMemory) MemoryOverview(ctx context.Context) (store.MemoryOverview, error) {
	c.n.Add(1)
	return c.memorySource.MemoryOverview(ctx)
}

type countedProviders struct {
	providerViewSource
	n *atomic.Int64
}

func (c countedProviders) ProviderOverview(ctx context.Context) ([]store.ProviderStatus, error) {
	c.n.Add(1)
	return c.providerViewSource.ProviderOverview(ctx)
}

type countedVerify struct {
	verifyViewSource
	n *atomic.Int64
}

func (c countedVerify) VerifyOverview(ctx context.Context, runID string) (store.VerifyOverview, error) {
	c.n.Add(1)
	return c.verifyViewSource.VerifyOverview(ctx, runID)
}

type countedSecurity struct {
	securityViewSource
	n *atomic.Int64
}

func (c countedSecurity) SecurityOverview(ctx context.Context, projectID, runID string) (store.SecurityOverview, error) {
	c.n.Add(1)
	return c.securityViewSource.SecurityOverview(ctx, projectID, runID)
}

type countedPlan struct {
	planViewSource
	n *atomic.Int64
}

func (c countedPlan) PlanOverview(ctx context.Context, runID string) (store.PlanOverview, error) {
	c.n.Add(1)
	return c.planViewSource.PlanOverview(ctx, runID)
}

type countedArch struct {
	archViewSource
	n *atomic.Int64
}

func (c countedArch) ArchitectureOverview(ctx context.Context, projectID string) (store.ArchitectureOverview, error) {
	c.n.Add(1)
	return c.archViewSource.ArchitectureOverview(ctx, projectID)
}

// countViewReads puts a counter in front of each view's current source.
func countViewReads(w *Workspace) *e2eReads {
	r := &e2eReads{}
	w.memSource = countedMemory{w.memorySrc(), &r.mem}
	w.provSource = countedProviders{w.providerSrc(), &r.prov}
	w.verSource = countedVerify{w.verifySrc(), &r.ver}
	w.secSource = countedSecurity{w.securitySrc(), &r.sec}
	w.planSource = countedPlan{w.planSrc(), &r.plan}
	w.archSource = countedArch{w.archSrc(), &r.arch}
	return r
}

// followerStacks is the stacks of the workspace's follower goroutines
// still running.
func followerStacks() []string {
	var buf bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&buf, 2)
	var out []string
	for _, g := range strings.Split(buf.String(), "\n\n") {
		for _, f := range []string{"followBoard", "followMemory", "followProviders", "followVerify", "followSecurity", "followPlan", "followArch"} {
			if strings.Contains(g, "tui.(*Workspace)."+f+"(") {
				out = append(out, f)
			}
		}
	}
	return out
}

// setPollIntervals sets the Board's poll and every view's periodic read,
// and returns what puts them back. Call it before any follower starts and
// restore only after they stop.
func setPollIntervals(board, views time.Duration) (restore func()) {
	vars := []*time.Duration{&archRefreshEvery, &memoryRefreshEvery, &planRefreshEvery, &providerRefreshEvery, &securityRefreshEvery, &verifyRefreshEvery}
	old := make([]time.Duration, len(vars))
	for i, v := range vars {
		old[i], *v = *v, views
	}
	oldBoard := boardFollowEvery
	boardFollowEvery = board
	return func() {
		for i, v := range vars {
			*v = old[i]
		}
		boardFollowEvery = oldBoard
	}
}

// e2eWorkspace is a workspace on a real runtime and store seeded with a
// run, with the run's Board on screen and its followers running as the
// raw terminal loop runs them.
func e2eWorkspace(t *testing.T) (*Workspace, *e2eStore, *e2eReads, context.CancelFunc) {
	t.Helper()
	w, rt := realControlWorkspace(t, "SESSION-board-e2e", false)
	w.screen = NewScreen(nil)
	s := seedE2EStore(t, w, rt)
	hw, _, clock := testWatcher(t, nil)
	steps(hw, clock, 3)
	w.hostWatch.Store(hw)
	m := w.marshalSession()
	m.mu.Lock()
	m.service, m.runID = &app.MarshalService{Store: s.st}, e2eRunID
	m.mu.Unlock()
	p := newMarshalPanel(e2eRunID, "codex", e2eRun(), "running")
	evs, _, _, err := w.boardFeed.read(t.Context(), s.st, e2eRunID)
	if err != nil {
		t.Fatal(err)
	}
	p.Board = w.projectBoard(p, evs)
	w.setMarshalPanel(p)
	reads := countViewReads(w)
	ctx, cancel := context.WithCancel(context.Background())
	w.startFollowers(ctx)
	return w, s, reads, cancel
}

// waitFrame waits for the frame to show every want, woken by the repaints
// the followers ask for, and returns it.
func waitFrame(t *testing.T, w *Workspace, cols, rows int, want ...string) string {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		frame := plainFrame(w, cols, rows)
		missing := ""
		for _, s := range want {
			if !strings.Contains(frame, s) {
				missing = s
				break
			}
		}
		if missing == "" {
			return frame
		}
		select {
		case <-w.repaint:
		case <-time.After(20 * time.Millisecond):
			// A read that finished before this wait began asked for its
			// repaint already; look again.
		case <-deadline.C:
			t.Fatalf("frame %dx%d never showed %q:\n%s", cols, rows, missing, frame)
		}
	}
}

// checkFrameShape fails unless the frame fills cols by rows exactly.
func checkFrameShape(t *testing.T, w *Workspace, cols, rows int) {
	t.Helper()
	lines, _, _ := w.frameLines(cols, rows)
	if len(lines) != rows {
		t.Fatalf("%dx%d frame has %d rows", cols, rows, len(lines))
	}
	for i, l := range lines {
		if n := VisibleLen(l); n != cols {
			t.Fatalf("%dx%d frame row %d is %d cells", cols, rows, i, n)
		}
	}
}

// TestBoardEndToEnd drives the Board on a real store through the raw
// terminal loop's key path: every view by its key and back, the
// inspector, both inbox decisions, the palette, help and a resize between
// the two sizes, then a live update the follower picks up.
func TestBoardEndToEnd(t *testing.T) {
	// The Board polls fast so the live update shows promptly; the views'
	// periodic reads wait an hour, so every read counted below is a read
	// on open, never one a slow run happened to reach.
	restore := setPollIntervals(20*time.Millisecond, time.Hour)
	w, s, reads, cancel := e2eWorkspace(t)
	t.Cleanup(func() {
		cancel()
		w.Close()
		restore()
	})
	ctx := t.Context()
	press := func(events ...KeyEvent) {
		t.Helper()
		for _, e := range events {
			if w.handleTerminalKey(ctx, e) {
				t.Fatalf("%+v asked the workspace to exit", e)
			}
		}
	}
	key := func(r rune) KeyEvent { return KeyEvent{Type: KeyRune, Rune: r} }
	sizes := [][2]int{{120, 40}, {80, 24}}

	// The Board itself.
	for _, sz := range sizes {
		checkFrameShape(t, w, sz[0], sz[1])
	}
	// "arch rev 7" is the Architecture follower's one read at start-up; the
	// frame counts below begin after it.
	frame := waitFrame(t, w, 120, 40, "Marshal", e2eRunID, "DISPATCHING", "TASKS", "LANES", "ACTIVITY", "T-101", "T-106", "arch rev 7")

	// Every view by its key, at both sizes, and back with Esc.
	views := []struct {
		name string
		keys []rune
		open func() bool
		want []string
	}{
		{"Host", []rune{'2', 'h'}, w.hostView.Load, []string{"HOST Asusworkstation"}},
		{"Memory", []rune{'3'}, w.memView.Load, []string{"MEMORY", "2 waiting review"}},
		{"Providers", []rune{'4'}, w.provView.Load, []string{"PROVIDERS", "codex", "gpt-5.5"}},
		{"Verify", []rune{'5'}, w.verView.Load, []string{"VERIFY", "T-103", "1/2 claude-1", "verifier       pass · agy-v (agy)"}},
		{"Security", []rune{'6'}, w.secView.Load, []string{"SECURITY", "registry.npmjs.org"}},
		{"Plan", []rune{'7'}, w.planView.Load, []string{"PLAN " + e2eRunID, "T-107"}},
		{"Architecture", []rune{'b', '8'}, w.archView.Load, []string{"ARCHITECTURE", "rev 7", "a3f1c9e2"}},
	}
	for _, v := range views {
		for _, k := range v.keys {
			archBefore := reads.arch.Load()
			press(key(k))
			if !v.open() {
				t.Fatalf("%c did not open the %s view", k, v.name)
			}
			for _, sz := range sizes {
				checkFrameShape(t, w, sz[0], sz[1])
			}
			// Only the 120-column frame is wide enough for every line.
			waitFrame(t, w, 120, 40, v.want...)
			waitFrame(t, w, 80, 24, v.want[0])
			// Opening the Architecture view wakes its follower for a fresh
			// read, but the frame already shows the start-up read: wait for
			// that read so it is not counted against painting below.
			if v.name == "Architecture" {
				for deadline := time.Now().Add(10 * time.Second); reads.arch.Load() == archBefore; time.Sleep(5 * time.Millisecond) {
					if time.Now().After(deadline) {
						t.Fatal("opening the Architecture view did not read it")
					}
				}
			}
			// Painting reads nothing: frames come from the last read.
			before := reads.total()
			for i := range 50 {
				sz := sizes[i%2]
				w.frameLines(sz[0], sz[1])
			}
			if got := reads.total(); got != before {
				t.Fatalf("50 frames of the %s view read the store %d times (%s)", v.name, got-before, reads)
			}
			press(KeyEvent{Type: KeyEsc})
			if v.open() {
				t.Fatalf("Esc did not close the %s view", v.name)
			}
			waitFrame(t, w, 120, 40, "TASKS")
		}
	}
	// 1 is the Board too.
	press(key('7'), key('1'))
	if w.planView.Load() {
		t.Fatal("1 did not return to the Board")
	}

	// The inspector: focus the tasks, Enter, Esc.
	press(key('t'), key('j'), KeyEvent{Type: KeyEnter})
	if w.boardUI.inspect == "" {
		t.Fatal("Enter did not open the inspector")
	}
	inspected := w.boardUI.inspect
	waitFrame(t, w, 120, 40, inspected)
	checkFrameShape(t, w, 80, 24)
	press(KeyEvent{Type: KeyEsc})
	if w.boardUI.inspect != "" {
		t.Fatal("Esc did not close the inspector")
	}

	// The inbox: allow the folder read, deny the escalated egress.
	folder := t.TempDir()
	w.permissions.mu.Lock()
	w.permissions.inbox = []permission.Request{
		{Kind: "network", RunID: e2eRunID, TaskID: "T-106", Object: "registry.npmjs.org:443", Scope: "one run", Reason: "outside manifest", Who: "Marshal"},
		{Kind: "read", Object: folder, Scope: "this project, until revoked, read-only", Reason: "read the vendored module", Who: "Marshal"},
	}
	w.permissions.mu.Unlock()
	w.boardUI.focus = ""
	waitFrame(t, w, 120, 40, "INBOX", "registry.npmjs.org:443")
	for range 2 {
		w.inboxView.Update(w.boardInbox())
		item, ok := w.inboxView.Current()
		if !ok {
			t.Fatal("the inbox has no current item")
		}
		action := 'd'
		if item.Request.Kind == "read" {
			action = 'a'
		}
		press(key(action))
		if !strings.Contains(plainFrame(w, 120, 40), "Enter to confirm "+string(action)) {
			t.Fatalf("no confirmation for %c:\n%s", action, plainFrame(w, 120, 40))
		}
		press(KeyEvent{Type: KeyEnter})
		if state := w.GetUIState(); state.LastOutputIsError && strings.Contains(state.LastOutput, "Permission decision failed") {
			t.Fatalf("%c on %s: %s", action, item.Request.Kind, state.LastOutput)
		}
	}
	if n := len(w.boardInbox()); n != 0 {
		t.Fatalf("%d requests left in the inbox", n)
	}
	evs, err := s.st.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	decided := map[string]bool{}
	for _, e := range evs {
		if e.Type != "PERMISSION_DECIDED" {
			continue
		}
		allow, _ := e.Data["allow"].(bool)
		switch {
		case e.Data["kind"] == "read" && e.Data["object"] == folder && allow:
			decided["allow"] = true
		case e.Data["kind"] == "network" && e.Data["object"] == "registry.npmjs.org:443" && !allow:
			// The egress had no pending runtime request, so the deny is
			// recorded as an expired run request; it is still a deny.
			decided["deny"] = true
		}
	}
	if !decided["allow"] || !decided["deny"] {
		t.Fatalf("decisions recorded %v", decided)
	}

	// The palette: Ctrl+P, search, Enter opens the view.
	press(KeyEvent{Type: KeyCtrlP})
	if !w.palette.IsOpen() {
		t.Fatal("Ctrl+P did not open the palette")
	}
	for _, r := range "plan view" {
		press(key(r))
	}
	if !strings.Contains(plainFrame(w, 120, 40), "Open Plan view") {
		t.Fatalf("palette frame:\n%s", plainFrame(w, 120, 40))
	}
	checkFrameShape(t, w, 80, 24)
	press(KeyEvent{Type: KeyEnter})
	if w.palette.IsOpen() || !w.planView.Load() {
		t.Fatal("the palette did not open the Plan view")
	}
	waitFrame(t, w, 120, 40, "PLAN "+e2eRunID)
	press(KeyEvent{Type: KeyEsc})

	// Help: ?, scroll at 80x24, any other key closes it.
	press(key('?'))
	if !w.boardHelp.Load() {
		t.Fatal("? did not open the help")
	}
	waitFrame(t, w, 120, 40, "KEYS", "architecture and business process")
	waitFrame(t, w, 80, 24, "j/k scroll")
	press(key('j'))
	if w.boardUI.helpScroll != 1 {
		t.Fatalf("j scrolled help to %d", w.boardUI.helpScroll)
	}
	press(key('q'))
	if w.boardHelp.Load() {
		t.Fatal("a key did not close the help")
	}

	// Resizing back and forth keeps every frame whole.
	for i := range 20 {
		sz := sizes[i%2]
		w.screen.Reset()
		checkFrameShape(t, w, sz[0], sz[1])
	}

	// Live: decisions appended to the store reach the Board on the
	// follower's next poll, with no key pressed.
	if !strings.Contains(frame, "T-105") {
		t.Fatalf("Board lacks T-105:\n%s", frame)
	}
	s.decide(t, events.EventTypeMarshalEscalated, "T-105", time.Now(), map[string]any{"reason": "candidate write outside scope e2e-live"})
	s.decide(t, events.EventTypeMarshalUsageCharged, "T-105", time.Now(), map[string]any{"charge_phase": "worker", "usage_known": true, "usage_units": float64(3000)})
	waitFrame(t, w, 120, 40, "T-105 parked · candidate write")
	// The two decisions may reach the Board on different polls, so the
	// escalation can show before the charge is counted.
	for deadline := time.Now().Add(10 * time.Second); ; {
		got := w.marshalPanel().Board.Run.Tokens.Value
		if got == 15000 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tokens after the live update %d, want 15000", got)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Exit stops every follower.
	if n := len(followerStacks()); n != 7 {
		t.Fatalf("%d followers running, want 7", n)
	}
	cancel()
	w.Close()
	if left := followerStacks(); len(left) != 0 {
		t.Fatalf("followers left after exit: %v", left)
	}
}
