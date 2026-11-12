package tui

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/hostwatch"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/usage"
)

var writeDemoSeq = flag.Bool("demo-seq", false, "write the README demo's ANSI frames to ../../out/demo (tools/demo/render.sh)")

// demoCols and demoRows are the README GIF's terminal size.
const demoCols, demoRows = 120, 30

// demoReqCard is the requirements card the Marshal fills for the demo goal.
const demoReqCard = `[` +
	`{"line":"a","text":"email login with a session cookie; out: OAuth","source":"you"},` +
	`{"line":"b","text":"migrations/, the public /v1 routes","source":"AGENTS.md:12"},` +
	`{"line":"c","text":"from main, lands on main","source":"git"},` +
	`{"line":"d","text":"go test ./... passes","source":"Makefile:4"},` +
	`{"line":"e","text":"go vet ./...","source":".github/workflows/ci.yml:31"},` +
	`{"line":"f","text":"internal/auth, web/login","source":"you"},` +
	`{"line":"g","text":"no new network deps","source":"you"},` +
	`{"line":"h","text":"300k tokens","source":"you"}` +
	`]`

// demoGoal is what the owner types to the Marshal, in the chunks the
// frames show it typed.
var demoGoal = []string{"add email ", "login with ", "a session cookie, ", "no OAuth"}

// demoTask is one task of the demo run: its plan id, title, worker
// provider, file and the last action its lane reports.
type demoTask struct {
	id, title, worker, file, last string
}

var demoTasks = []demoTask{
	{"T-1", "login handler", "codex", "internal/auth/login.go", "edit internal/auth/login.go:48"},
	{"T-2", "session store", "claude", "internal/auth/session.go", "test go test ./internal/auth/..."},
	{"T-3", "login page", "agy", "web/login/page.html", "edit web/login/page.html:21"},
	{"T-4", "login tests", "opencode", "internal/auth/login_test.go", "edit internal/auth/login_test.go:96"},
}

// demoReviewer is the provider that reviews each worker's hand-in: never
// the worker's own.
var demoReviewer = map[string]string{"codex": "claude", "claude": "codex", "agy": "codex", "opencode": "claude"}

const demoRunID = "RUN-notes-login"

// The demo's story clock: when each step happens, from the opening. A frame
// is painted "at" a story time, so the Board's own clock (time.Now) reads
// as that moment and every timestamp on screen is consistent with it.
const (
	storyPicked    = 5 * time.Second
	storySaid      = 25 * time.Second
	storyCard      = 50 * time.Second
	storyConfirmed = time.Minute
	storyGate      = 1*time.Minute + 40*time.Second
	storyAccepted  = 2*time.Minute + 30*time.Second
	storyApproved  = 2*time.Minute + 45*time.Second
)

// TestWriteDemoSequence renders the README demo: the shipped Board, driven
// through its own key handlers and fed fixture state, from the opening to
// the "what next?" card. Every frame is the Workspace's own frame
// (frameLinesFull, the Board beside the docked chat), so the GIF shows
// nothing the build cannot. It writes nothing unless -demo-seq is set, so it
// is inert in normal runs; tools/demo/render.sh runs it and turns the
// frames into .github/assets/demo.gif.
func TestWriteDemoSequence(t *testing.T) {
	if !*writeDemoSeq {
		t.Skip("set -demo-seq to write demo frames")
	}
	if !editionExtreme {
		// The README shows the shipped build; this edition has no
		// architecture gate and asks the owner to approve each plan.
		t.Skip("the README demo renders the shipped (untagged) build")
	}
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("BOARD_TRUECOLOR", "")
	t.Setenv("MARSHAL_REDUCED_MOTION", "")
	ctx := context.Background()

	outDir := filepath.Join("..", "..", "out", "demo")
	if err := os.RemoveAll(outDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	w := demoWorkspace(t)
	defer w.Close()
	s := &demoStory{t: t, w: w, now: time.Now(), dir: outDir, at: map[string]time.Duration{}}
	key := func(ev KeyEvent) { w.handleTerminalKey(ctx, ev) }
	r := func(c rune) KeyEvent { return KeyEvent{Type: KeyRune, Rune: c} }

	// 1. The opening: the wordmark one letter a frame, then the tagline,
	// held over the project check until it finishes.
	for f := 1; f <= openingFrames; f++ {
		w.opening.mu.Lock()
		w.opening.active, w.opening.done, w.opening.frame = true, false, f
		w.opening.mu.Unlock()
		hold := 120 * time.Millisecond
		if f == openingFrames {
			hold = 900 * time.Millisecond
		}
		s.shot(fmt.Sprintf("opening-%d", f), 0, hold)
	}
	w.endOpening()
	if show, _, checking := w.openingPaint(); !show || !checking {
		t.Fatal("the opening does not hold over the project check")
	}
	s.shot("checking", time.Second, 500*time.Millisecond)

	// The "Who plans?" picker: the check is ready and no Marshal model is
	// saved, so it asks, each candidate with its limit. The owner moves
	// from the preselected one to codex and keeps it.
	w.finishLaunchCheck(nil, nil)
	if !w.pickerOpen() {
		t.Fatal("the picker did not open")
	}
	s.shot("picker", 2*time.Second, 1500*time.Millisecond)
	key(KeyEvent{Type: KeyLeft})
	s.shot("picker-codex", 4*time.Second, 900*time.Millisecond)
	key(KeyEvent{Type: KeyEnter})
	if w.pickerOpen() || loadDefaultProvider(w.providerRoot()) != "codex" {
		t.Fatal("Enter did not keep codex")
	}
	s.stamp(storyPicked)

	// 2. The Board: the vitals row, the first-use card and the hint bar.
	s.shot("board", 6*time.Second, 2300*time.Millisecond)

	// 3. The goal, said from the Board; the Marshal's requirements card;
	// Enter confirms it.
	key(r('s'))
	for i, chunk := range demoGoal {
		for _, c := range chunk {
			key(r(c))
		}
		s.shot("say", 20*time.Second+time.Duration(i)*time.Second, 200*time.Millisecond)
	}
	s.shot("say-full", 24*time.Second, 700*time.Millisecond)
	key(KeyEvent{Type: KeyEnter})
	s.stamp(storySaid)
	s.shot("said", storySaid+5*time.Second, 800*time.Millisecond)
	p, err := parseMarshalProposal(reqProposalLine(demoReqCard))
	if err != nil {
		t.Fatal(err)
	}
	w.openRequirements(p)
	s.stamp(storyCard)
	s.shot("requirements", storyCard+5*time.Second, 3000*time.Millisecond)
	key(KeyEvent{Type: KeyEnter})
	if w.currentReq() != nil {
		t.Fatal("Enter did not confirm the requirements")
	}
	s.stamp(storyConfirmed)
	s.shot("confirmed", storyConfirmed+3*time.Second, 800*time.Millisecond)

	// 4. The architecture gate. Accept happens only in the browser: y
	// opens the page and the Board says so. The demo drives no browser; the
	// Board after the owner's Accept shows what browserAccepted records.
	w.gateContractSource = func(context.Context) (*gateContract, error) {
		return &gateContract{ID: "notes-api-login", Revision: 1, Digest: "sha256:4be1c0de", Purpose: "Email login for notes-api: a login handler, a session store, a login page and their tests.", Submitted: time.Now()}, nil
	}
	w.refreshGates(ctx)
	s.shot("gate", storyGate, 2400*time.Millisecond)
	w.memoryAnswer("Opening the architecture in your browser…", false)
	s.shot("gate-open", storyGate+5*time.Second, 1300*time.Millisecond)
	w.gateContractSource = func(context.Context) (*gateContract, error) { return nil, nil }
	w.refreshGates(ctx)
	w.wakeMarshalAfterAccept()
	w.RecordActivity("Architecture accepted in the browser (rev 1).")
	s.stamp(storyAccepted)

	// 5. The run: four workers on four providers in parallel, each hand-in
	// reviewed by a different provider, the run verified, then merged.
	s.run("build", 4*time.Minute, 2500*time.Millisecond, marshal.Dispatching, 0, nil)
	s.run("review", 7*time.Minute+30*time.Second, 1700*time.Millisecond, marshal.Reviewing, 1, map[string]marshal.TaskState{"T-1": marshal.HandedIn, "T-2": marshal.HandedIn})
	w.verSource = demoVerify{demoOverview()}
	w.openVerifyView()
	w.refreshVerify(ctx)
	s.shot("verify-view", 7*time.Minute+40*time.Second, 2800*time.Millisecond)
	w.closeVerifyView()
	all := func(st marshal.TaskState) map[string]marshal.TaskState {
		m := map[string]marshal.TaskState{}
		for _, d := range demoTasks {
			m[d.id] = st
		}
		return m
	}
	s.run("verifying", 8*time.Minute+40*time.Second, 1400*time.Millisecond, marshal.Verifying, 2, all(marshal.Accepted))
	merging := all(marshal.Merged)
	merging["T-4"] = marshal.Accepted
	s.run("merging", 9*time.Minute+5*time.Second, 1400*time.Millisecond, marshal.Merging, 3, merging)

	// 6. The run closes: the "what next?" card, from the run's own counts.
	s.run("next", 9*time.Minute+25*time.Second, 3600*time.Millisecond, marshal.Closed, 4, all(marshal.Merged))
	if w.currentNext() == nil {
		t.Fatal("the what-next card did not open")
	}

	if err := os.WriteFile(filepath.Join(outDir, "frames.txt"), []byte(s.manifest.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("wrote %d frames to %s\n", s.n, outDir)
}

// demoStory paints the demo's frames on its story clock.
type demoStory struct {
	t        *testing.T
	w        *Workspace
	now      time.Time // the real time a frame is painted at: story "present"
	dir      string
	n        int
	manifest strings.Builder
	at       map[string]time.Duration // each Activity note and echo's story time
	fresh    time.Duration            // story time of entries not stamped yet
}

// stamp gives every Activity note and echo recorded since the last stamp
// the story time at.
func (s *demoStory) stamp(at time.Duration) {
	for _, n := range s.w.noteList() {
		if _, ok := s.at[fmt.Sprint("n", n.id)]; !ok {
			s.at[fmt.Sprint("n", n.id)] = at
		}
	}
	for _, e := range s.w.sayEchoList() {
		if _, ok := s.at[fmt.Sprint("e", e.id)]; !ok {
			s.at[fmt.Sprint("e", e.id)] = at
		}
	}
}

// real is the wall time of story time at in a frame painted at present.
func (s *demoStory) real(present, at time.Duration) time.Time {
	return s.now.Add(at - present)
}

// shot paints one frame at story time present and holds it for hold.
func (s *demoStory) shot(name string, present, hold time.Duration) {
	t, w := s.t, s.w
	t.Helper()
	w.notes.mu.Lock()
	for i := range w.notes.list {
		if at, ok := s.at[fmt.Sprint("n", w.notes.list[i].id)]; ok {
			w.notes.list[i].At = s.real(present, at)
		}
	}
	w.notes.mu.Unlock()
	w.say.mu.Lock()
	for i := range w.say.echo {
		if at, ok := s.at[fmt.Sprint("e", w.say.echo[i].id)]; ok {
			w.say.echo[i].At = s.real(present, at)
		}
	}
	w.say.mu.Unlock()
	s.now = time.Now()
	lines, _, _ := w.frameLinesFull(demoCols, demoRows)
	if len(lines) != demoRows {
		t.Fatalf("%s: %d rows", name, len(lines))
	}
	for i, l := range lines {
		if got := VisibleLen(l); got > demoCols {
			t.Fatalf("%s: row %d is %d cells", name, i, got)
		}
		// The screen clears each row's tail; a cat of the frame must too.
		lines[i] = PadCell(l, demoCols)
	}
	s.n++
	file := fmt.Sprintf("%02d-%s.ans", s.n, name)
	// No trailing newline: the frame fills the terminal exactly.
	if err := os.WriteFile(filepath.Join(s.dir, file), []byte(strings.Join(lines, "\r\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&s.manifest, "%s %d\n", file, hold.Milliseconds())
}

// run publishes the run in state st at story time present, as
// marshalPublish does (the "what next?" card included), and paints it.
func (s *demoStory) run(name string, present, hold time.Duration, st marshal.RunState, stage int, states map[string]marshal.TaskState) {
	run := marshal.Run{State: st, PlanID: "PLAN-login", Settings: marshal.DefaultSettings(), Budget: marshal.Budget{Tokens: marshal.Ceiling{Plan: 300000}}}
	for _, d := range demoTasks {
		ts, ok := states[d.id]
		if !ok {
			ts = marshal.Dispatched
		}
		run.Tasks = append(run.Tasks, marshal.Task{PlanTaskID: d.id, Title: d.title, Worker: d.worker, State: ts, Files: []string{d.file}, Checks: []marshal.Check{{Command: "go test ./internal/auth/..."}}})
	}
	// The live workers: one lane per task still with its worker.
	var workers []boardmodel.Worker
	for i, d := range demoTasks {
		if ts := run.Tasks[i].State; ts != marshal.Dispatched && ts != marshal.HandedIn {
			continue
		}
		// A worker reports its state and last action over OSC 7501; one
		// that has handed in reports done.
		status, last := "working", d.last
		if states[d.id] == marshal.HandedIn {
			status, last = "done", "done"
		}
		workers = append(workers, boardmodel.Worker{ID: d.worker + "-1", Provider: d.worker, TaskID: d.id, Last: last, Status: status})
	}
	p := newMarshalPanel(demoRunID, "codex", run, string(st))
	p.Board = boardmodel.Project(boardmodel.Snapshot{RunID: demoRunID, Run: run, Workers: workers}, demoEvents(s.real(present, storyApproved), present-storyApproved, stage))
	s.w.noteRunClosed(s.w.marshalPanel(), p)
	s.w.setMarshalPanel(p)
	s.shot(name, present, hold)
}

// demoEvents is the run's events from its plan approval at start, painted
// elapsed after it, up to stage: 0 dispatched and working, 1 two hand-ins, 2 every task accepted,
// 3 three merged, 4 all merged and the run closed.
func demoEvents(start time.Time, elapsed time.Duration, stage int) []events.Event {
	var evs []events.Event
	add := func(at time.Duration, typ events.EventType, task string, data map[string]any) {
		evs = append(evs, events.Event{Sequence: events.Sequence(len(evs) + 1), Type: typ, RunID: demoRunID, TaskID: task, At: start.Add(at), Data: data})
	}
	add(0, events.EventTypeMarshalPlanApproved, "", map[string]any{"plan_version": int64(1)})
	for i, d := range demoTasks {
		add(time.Duration(5+i*3)*time.Second, events.EventTypeMarshalTaskDispatched, d.id, map[string]any{"worker": d.worker})
	}
	// Usage charges, about every 12 s per lane while it works and up to
	// the moment painted.
	worked := []time.Duration{4*time.Minute + 15*time.Second, 4*time.Minute + 35*time.Second, 5 * time.Minute, 5 * time.Minute}
	for k := 0; ; k++ {
		charged := false
		for i, d := range demoTasks {
			at := time.Duration(20+k*12+i*3) * time.Second
			if at >= min(elapsed, worked[i]) {
				continue
			}
			add(at, events.EventTypeMarshalUsageCharged, d.id, map[string]any{"charge_phase": "worker", "usage_known": true, "usage_units": int64(800 + (k*k*37+k*11+i*53)%23*110)})
			charged = true
		}
		if !charged {
			break
		}
	}
	if stage >= 1 {
		add(4*time.Minute+15*time.Second, events.EventTypeMarshalTaskHandedIn, "T-1", map[string]any{"reason": "hand-in with 3 evidence refs"})
		add(4*time.Minute+35*time.Second, events.EventTypeMarshalTaskHandedIn, "T-2", map[string]any{"reason": "hand-in with 2 evidence refs"})
	}
	if stage >= 2 {
		for i, d := range demoTasks {
			add(5*time.Minute+time.Duration(i*15)*time.Second, events.EventTypeMarshalTaskAccepted, d.id, nil)
		}
	}
	if stage >= 3 {
		for i, d := range demoTasks[:3] {
			add(6*time.Minute+time.Duration(i*4)*time.Second, events.EventTypeMarshalTaskMerged, d.id, nil)
		}
	}
	if stage >= 4 {
		add(6*time.Minute+25*time.Second, events.EventTypeMarshalTaskMerged, "T-4", nil)
		add(6*time.Minute+35*time.Second, events.EventTypeMarshalRunClosed, "", map[string]any{"target": "main", "commit": "4be1c0de2f9a"})
	}
	return evs
}

// demoVerify is a Verify view source with a fixed overview.
type demoVerify struct{ o store.VerifyOverview }

func (d demoVerify) VerifyOverview(context.Context, string) (store.VerifyOverview, error) {
	return d.o, nil
}
func (demoVerify) ReturnTask(context.Context, string, string) (string, error) { return "", nil }

// demoOverview is the Verify view of the run in review: each hand-in so far
// checked by a reviewer from another provider.
func demoOverview() store.VerifyOverview {
	o := store.VerifyOverview{RunID: demoRunID}
	test := store.VerifyClaim{Status: store.ClaimSupported, Text: "go test ./internal/auth/... passes", Evidence: "check:go test ./internal/auth/...", CheckedBy: "runtime", Criterion: true}
	claims := map[string][]store.VerifyClaim{
		"T-1": {test, {Status: store.ClaimSupported, Text: "a wrong password returns 401", Evidence: "login_test.go:58", CheckedBy: "claude-1"}, {Status: store.ClaimSupported, Text: "the cookie is HttpOnly and Secure", Evidence: "login.go:71", CheckedBy: "claude-1"}},
		"T-2": {test, {Status: store.ClaimSupported, Text: "sessions expire after 24 h", Evidence: "session_test.go:33", CheckedBy: "codex-1"}},
	}
	for _, d := range demoTasks {
		vt := store.VerifyTask{ID: d.id, Title: d.title, State: marshal.Dispatched, Worker: d.worker, WorkerProvider: d.worker}
		if c, ok := claims[d.id]; ok {
			rv := demoReviewer[d.worker]
			vt.State, vt.Attempt, vt.Reviewer, vt.ReviewerProvider, vt.OtherProvider, vt.Verdict, vt.Claims = marshal.HandedIn, 1, rv+"-1", rv, true, marshal.VerdictAccept, c
		}
		o.Tasks = append(o.Tasks, vt)
	}
	return o
}

// demoSampler is the fixture host with CPU ticks, so the vitals row has a
// CPU figure.
type demoSampler struct {
	*fakeHost
}

func (d demoSampler) Read(ctx context.Context) (hostwatch.Sample, error) {
	s, err := d.fakeHost.Read(ctx)
	n := uint64(d.fakeHost.n)
	s.CPUTicksTotal, s.CPUTicksIdle, s.CPUTicksKnown = n*1000, n*720, true
	return s, err
}

// demoWorkspace is a fresh project's Board: four providers installed and
// verified, their limits read, the host watched, and a native Marshal chat
// attached so what the owner says reaches a chat.
func demoWorkspace(t *testing.T) *Workspace {
	t.Helper()
	installPickerStubs(t, "codex", "claude", "agy", "opencode")
	w := NewWorkspace(nil, "demo", "session")
	w.workDir = filepath.Join(t.TempDir(), "notes-api")
	if err := os.MkdirAll(w.workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	w.theme = NewTheme(ThemeDefault, true, true)
	w.screen = NewScreen(NewTerminal(strings.NewReader(""), &strings.Builder{}))
	w.homeBoard = true

	now := time.Now()
	qs := []usage.Quota{
		{Provider: "claude", Known: true, Windows: []usage.Window{{Label: "5h", LeftPercent: 82, ResetsAt: now.Add(3*time.Hour + 10*time.Minute)}}, AsOf: now.Add(-time.Minute)},
		{Provider: "codex", Known: true, Windows: []usage.Window{{Label: "5h", LeftPercent: 67, ResetsAt: now.Add(4*time.Hour + 20*time.Minute)}}, AsOf: now.Add(-time.Minute)},
		{Provider: "agy", Reason: usage.NotExposed},
		{Provider: "opencode", Reason: usage.NotExposed},
	}
	w.usage.readFn = func(time.Time) []usage.Quota { return qs }
	w.storeUsage(qs)
	w.provSource = fakePickerProviders{}
	w.prov.mu.Lock()
	w.prov.snap = providerSnapshot{Loaded: true, At: now, Providers: []store.ProviderStatus{
		{Provider: "claude", VerifiedAt: now}, {Provider: "codex", VerifiedAt: now}, {Provider: "agy", VerifiedAt: now}, {Provider: "opencode", VerifiedAt: now},
	}}
	w.prov.mu.Unlock()
	w.firstUse.mu.Lock()
	w.firstUse.installed = map[string]bool{"claude": true, "codex": true, "agy": true, "opencode": true}
	w.firstUse.mu.Unlock()

	c := &fakeClock{t: hostEpoch}
	f := newFakeHost(c)
	f.free = 1150e9
	h := newHostWatcher(demoSampler{f})
	h.now = c.now
	h.facts = func() hostFacts { return hostFacts{} }
	h.cacheSize = func(context.Context) (uint64, bool) { return 0, false }
	steps(h, c, 3)
	w.hostWatch.Store(h)

	w.attachNativeChat(newFakeChatTransport(), w.workDir, nil, nil, nil, nil, "", false)
	t.Cleanup(w.stopNativeChat)
	return w
}
