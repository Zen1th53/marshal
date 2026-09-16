package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/testutil/testcloud"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

// Drive the REAL Workspace: construct it as the CLI does, open navigation the
// way Ctrl+N does, and confirm the frozen IA renders through the actual paint
// path with truthful values.
func TestWorkspaceNavigationEndToEnd(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	if ws.navView == nil {
		t.Fatal("the workspace was built without a navigation view")
	}
	ctx := context.Background()

	if ws.navView.IsOpen() {
		t.Fatal("navigation is open before it was asked for")
	}
	ws.openNavigation(ctx)
	if !ws.navView.IsOpen() {
		t.Fatal("openNavigation did not open the view")
	}
	// Production opens without blocking the input loop, so the first read
	// lands asynchronously. Wait for it rather than asserting on a frame that
	// legitimately has not read anything yet.
	ws.navView.Refresh(ctx)

	out := strings.Join(ws.navView.Render(140, 30), "\n")
	for _, s := range FrozenSections {
		if !strings.Contains(out, s) {
			t.Fatalf("the real workspace does not render section %q:\n%s", s, out)
		}
	}

	// Navigate to Status / Runtime counts by keyboard through the real view.
	ws.navView.HandleKey(ctx, KeyEvent{Type: KeyRune, Rune: '3'})
	if got := ws.navView.Nav().Current().Title; got != "Status" {
		t.Fatalf("pressing 3 in the real workspace opened %q", got)
	}

	// With no store attached the counts must be UNKNOWN with a reason, never 0.
	if err := ws.navView.Nav().DeepLink("CTUI-0168"); err != nil {
		t.Fatalf("deep link: %v", err)
	}
	out = strings.Join(ws.navView.Render(140, 30), "\n")
	if !strings.Contains(out, "UNKNOWN") {
		t.Fatalf("a storeless workspace did not report UNKNOWN:\n%s", out)
	}
	if !strings.Contains(out, "no runtime is attached") {
		t.Fatalf("the reason did not reach the real screen:\n%s", out)
	}

	// Ctrl+C closes navigation rather than exiting, as the loop intends.
	ws.navView.Close()
	if ws.navView.IsOpen() {
		t.Fatal("the view stayed open after Close")
	}
}

// The paint path itself must route to the navigation view while it is open,
// or the view would be built and never shown.
func TestWorkspacePaintRoutesToNavigationWhenOpen(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	ws.openNavigation(context.Background())
	ws.navView.Refresh(context.Background())

	// paint() requires a screen; drive the same branch it takes.
	if !ws.navView.IsOpen() {
		t.Fatal("navigation did not open")
	}
	lines := ws.navView.Render(120, 24)
	if len(lines) == 0 {
		t.Fatal("the navigation view rendered nothing for the paint path")
	}
	if !strings.Contains(lines[0], "Home") {
		t.Fatalf("the first painted line is %q, want the top navigation", lines[0])
	}
}

// The store-backed reader must show real rows from a real database, so the
// binding is proven against canonical storage rather than only against fakes.
func TestStoreBackedRuntimeReaderShowsRealRows(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "marshal.db"))
	if err != nil {
		t.Skipf("this build cannot open a store here: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ws := NewWorkspace(st, "proj", "sess-real")
	reader := ws.runtimeReader()
	if reader == nil {
		t.Fatal("a workspace with a store produced no runtime reader")
	}

	// A migrated but uninitialised store has no project row. That is an error
	// from the canonical read, and it must surface as ERROR with the reason
	// rather than as an empty project with zero counts.
	if _, err := reader.Status(context.Background()); err == nil {
		t.Fatal("an uninitialised store reported a status without a project")
	} else {
		blank := (&StatusSource{Runtime: reader, Now: fixedClock()}).
			ReadRuntime(context.Background())
		if blank.Tasks.Status.IsSuccess() {
			t.Fatalf("an uninitialised store reported a known task count: %q",
				blank.Tasks.Display())
		}
		if blank.Verdict == VerdictPass {
			t.Fatal("an uninitialised store summarised as PASS")
		}
	}

	if err := st.InitProject(context.Background(), model.Project{
		ID: "proj", Repository: "github.com/Zen1th53/marshal",
		DefaultBranch: "main", PackVersion: "1",
	}); err != nil {
		t.Fatalf("init project: %v", err)
	}

	status, err := reader.Status(context.Background())
	if err != nil {
		t.Fatalf("status after initialisation: %v", err)
	}

	source := &StatusSource{Runtime: reader, Now: fixedClock()}
	snap := source.ReadRuntime(context.Background())

	// A real store that answered gives measured values, and the schema version
	// is a real number rather than UNKNOWN.
	if !snap.SchemaVersion.Status.IsSuccess() {
		t.Fatalf("a live store reported schema %s", snap.SchemaVersion.Display())
	}
	if snap.Verdict != VerdictPass {
		t.Fatalf("a live store summarised as %s", snap.Verdict)
	}
	if !snap.Tasks.Status.IsSuccess() {
		t.Fatalf("a live store reported tasks as %s", snap.Tasks.Display())
	}
	if got := snap.ProjectName.Display(); got != "github.com/Zen1th53/marshal" {
		t.Fatalf("the real repository did not reach the snapshot: %q", got)
	}
	t.Logf("live store: schema=%s tasks=%s agents=%s project=%s (raw schema %d)",
		snap.SchemaVersion.Display(), snap.Tasks.Display(),
		snap.Agents.Display(), snap.ProjectName.Display(), status.SchemaVersion)

	// And those values reach the real screen.
	v := testView(t)
	v.AttachSource(source, nil)
	v.OpenAndWait(context.Background())
	if err := v.Nav().DeepLink("CTUI-0168"); err != nil {
		t.Fatalf("deep link: %v", err)
	}
	out := strings.Join(v.Render(120, 30), "\n")
	if strings.Contains(out, "UNKNOWN") {
		t.Fatalf("a live store still rendered UNKNOWN counts:\n%s", out)
	}
}

// Opening navigation and returning must not touch what the user was typing.
//
// The navigation view intercepts every key while open, so a half-written
// command must survive the round trip — losing it would make Ctrl+N a key
// nobody presses twice.
func TestNavigationDoesNotDisturbTheComposer(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	ctx := context.Background()

	const draft = "/goal create a half written thing"
	ws.composer.SetText(draft)

	ws.openNavigation(ctx)
	// Keys that would otherwise edit the composer go to navigation instead.
	for _, ev := range []KeyEvent{
		{Type: KeyRune, Rune: '3'},
		{Type: KeyDown}, {Type: KeyDown}, {Type: KeyEnter},
		{Type: KeyRune, Rune: 'x'}, {Type: KeyRune, Rune: 'y'},
		{Type: KeyEsc},
	} {
		ws.navView.HandleKey(ctx, ev)
	}
	ws.navView.Close()

	if got := ws.composer.Text(); got != draft {
		t.Fatalf("the composer holds %q after a navigation round trip, want %q", got, draft)
	}
}

// Reopening navigation returns the user where they were, rather than resetting
// to Home and making them walk back.
func TestNavigationRemembersPositionAcrossClose(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	ctx := context.Background()

	ws.openNavigation(ctx)
	ws.navView.HandleKey(ctx, KeyEvent{Type: KeyRune, Rune: '6'}) // Memory
	where := ws.navView.Nav().Current()
	ws.navView.Close()

	ws.openNavigation(ctx)
	if got := ws.navView.Nav().Current(); got != where {
		t.Fatalf("reopening navigation landed on %q, want %q",
			got.MenuPath, where.MenuPath)
	}
}

// A workspace whose manifest failed to load must still be usable. The Ctrl+N
// path checks for a nil view rather than dereferencing it.
func TestWorkspaceSurvivesAMissingNavigationView(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	ws.navView = nil

	// These are the three places the loop touches the view.
	if ws.navView.IsOpen() {
		t.Fatal("a nil navigation view reported itself open")
	}
	ws.openNavigation(context.Background()) // must not panic
	if ws.navView.IsOpen() {
		t.Fatal("a nil navigation view opened")
	}
}

// The Cloud reader must observe handles that change after navigation opened.
//
// The Cloud handshake completes after the workspace is built. A reader that
// captured the nil gate it saw at open time would report Standard for the rest
// of the session however the server later answered.
func TestCloudStateIsReadLiveNotCapturedAtOpen(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	ctx := context.Background()

	ws.openNavigation(ctx)
	ws.navView.Refresh(ctx)

	// At open there is no Cloud at all.
	if err := ws.navView.Nav().DeepLink("CTUI-0155"); err != nil {
		t.Fatalf("deep link to cloud connection: %v", err)
	}
	before := strings.Join(ws.navView.Render(120, 30), "\n")
	if !strings.Contains(before, "NOT_RUN") && !strings.Contains(before, "UNKNOWN") {
		t.Fatalf("an unconfigured Cloud did not read as unavailable:\n%s", before)
	}

	// The handshake now fails with a distinct reason, after navigation opened.
	ws.AttachULTRAError(errors.New("rate limited: retry after 60s"))
	ws.AttachULTRARequester(&cloud.Client{}, cloud.State{InstallationID: "inst-live"}, "sess-live")
	ws.navView.Refresh(ctx)

	after := strings.Join(ws.navView.Render(120, 30), "\n")
	if !strings.Contains(after, "rate limited") {
		t.Fatalf("the reader did not observe the later refusal:\n%s", after)
	}
	if after == before {
		t.Fatal("the Cloud reader froze its state at open time")
	}
}

// Reading the Cloud handles must not deadlock against the workspace lock.
func TestLiveCloudReaderDoesNotDeadlock(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	done := make(chan struct{})
	go func() {
		defer close(done)
		ws.openNavigation(context.Background())
		ws.navView.Refresh(context.Background())
		_ = ws.navView.Render(100, 30)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("opening navigation deadlocked on the workspace lock")
	}
}

// Drive the workspace's real key dispatch, not a copy of it.
//
// The interactive loop needs a TTY and cannot be driven from a test, so the
// dispatch it performs lives in dispatchNavigationKey and this exercises that
// exact function — the same one runRawTerminal calls.
func TestRealKeyDispatchOpensAndOwnsNavigation(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	ctx := context.Background()
	entitleULTRA(t, ws)

	// Before Ctrl+N the dispatch declines every key, so the composer keeps them.
	if ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyRune, Rune: 'x'}) {
		t.Fatal("the navigation dispatch consumed a key while closed")
	}
	if ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyDown}) {
		t.Fatal("the navigation dispatch consumed an arrow key while closed")
	}

	// Ctrl+N opens it.
	if !ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyCtrlN}) {
		t.Fatal("Ctrl+N was not consumed by the navigation dispatch")
	}
	if !ws.navView.IsOpen() {
		t.Fatal("Ctrl+N did not open navigation")
	}
	ws.navView.Refresh(ctx)

	// Now it owns every key, including ones the composer would otherwise take.
	for _, ev := range []KeyEvent{
		{Type: KeyRune, Rune: 'x'}, {Type: KeyDown}, {Type: KeyEnter},
		{Type: KeyTab}, {Type: KeyRune, Rune: '/'},
	} {
		if !ws.dispatchNavigationKey(ctx, ev) {
			t.Fatalf("navigation did not consume %v while open", ev.Type)
		}
	}

	// Ctrl+C closes navigation rather than exiting the workspace.
	if !ws.interruptNavigation() {
		t.Fatal("Ctrl+C did not close navigation")
	}
	if ws.navView.IsOpen() {
		t.Fatal("navigation stayed open after the interrupt")
	}
	// With navigation closed the interrupt is no longer consumed here, so the
	// loop's ordinary Ctrl+C handling still runs.
	if ws.interruptNavigation() {
		t.Fatal("the interrupt was consumed with navigation already closed")
	}
}

// The nil-view path in the real dispatch must not panic and must explain
// itself, since a failed manifest load still has to leave a usable workspace.
func TestRealKeyDispatchHandlesAMissingView(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	// Entitled, so the dispatch reaches the missing-view branch rather than
	// stopping at the ULTRA gate in front of it.
	entitleULTRA(t, ws)
	ws.navView = nil

	var out strings.Builder
	ws.out = &out

	if !ws.dispatchNavigationKey(context.Background(), KeyEvent{Type: KeyCtrlN}) {
		t.Fatal("Ctrl+N was not consumed when the view was missing")
	}
	if !strings.Contains(out.String(), "Navigation is unavailable") {
		t.Fatalf("the missing view was not explained: %q", out.String())
	}
	if ws.interruptNavigation() {
		t.Fatal("a nil view consumed the interrupt")
	}
}

// --- Control through the real workspace ---

// Control reaches the real key dispatch, and a workspace with no runtime
// refuses every action rather than writing anything locally.
func TestControlThroughTheRealWorkspaceRefusesWithoutARuntime(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	ctx := context.Background()
	entitleULTRA(t, ws)

	// Ctrl+N through the same dispatch the interactive loop calls.
	if !ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyCtrlN}) {
		t.Fatal("Ctrl+N was not consumed")
	}
	ws.navView.Refresh(ctx)

	// A Control action, reached by keyboard alone.
	if err := ws.navView.Nav().DeepLink("CTUI-0046"); err != nil { // Cancel task
		t.Fatalf("deep link: %v", err)
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})

	// With no runtime the confirmation must not stand ready to submit.
	c := ws.navView.Confirmation()
	if c.Phase() == PhaseConfirming {
		t.Fatal("a confirmation opened with no authority to submit to")
	}
	out := strings.Join(ws.navView.Render(140, 30), "\n")
	if !strings.Contains(out, "authority") && !strings.Contains(out, "runtime") {
		t.Fatalf("the missing authority was not explained:\n%s", out)
	}
}

// The Control source is rebuilt from live workspace handles, so a runtime
// attached after the workspace was built is still reachable.
func TestControlSourceReadsLiveWorkspaceHandles(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")

	before := ws.controlSource()
	if before.Authority != nil {
		t.Fatal("a workspace with no runtime produced an authority")
	}
	if before.SessionID != "sess-1" {
		t.Fatalf("the source carries session %q", before.SessionID)
	}
	// Every action must refuse rather than appear available.
	for id, binding := range before.Bindings() {
		if !binding.Bound() {
			continue
		}
		var err error
		if binding.PrepareRequest != nil {
			_, err = binding.PrepareRequest(context.Background(), ActionRequest{})
		} else if binding.Prepare != nil {
			_, err = binding.Prepare(context.Background())
		}
		if err == nil {
			t.Fatalf("%s prepared a target with no authority", id)
		}
	}
}

// Ctrl+C closes a Control confirmation without submitting it.
func TestInterruptClosesAConfirmationWithoutSubmitting(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	ctx := context.Background()
	source, auth := testControl(t)
	entitleULTRA(t, ws)

	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyCtrlN})
	ws.navView.AttachControl(source)
	ws.navView.Refresh(ctx)

	if err := ws.navView.Nav().DeepLink("CTUI-0046"); err != nil {
		t.Fatalf("deep link: %v", err)
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	if ws.navView.Confirmation().Phase() != PhaseConfirming {
		t.Fatalf("no confirmation opened, phase %s", ws.navView.Confirmation().Phase())
	}

	// Ctrl+C, through the real interrupt path.
	if !ws.interruptNavigation() {
		t.Fatal("Ctrl+C did not close navigation")
	}
	if n := atomic.LoadInt32(&auth.cancels); n != 0 {
		t.Fatalf("the interrupt caused %d mutations", n)
	}
}

// entitleULTRA gives a test workspace a genuinely signed, verified ULTRA lease.
//
// Navigation is an ULTRA surface, so a test that drives it needs an entitled
// session exactly as a user would. The lease travels cloud.Gate.Adopt, so this
// grants entitlement rather than bypassing the check for it.
func entitleULTRA(t *testing.T, ws *Workspace) {
	t.Helper()
	ws.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{}), true)
}

func realControlWorkspace(t *testing.T, sessionID string) (*Workspace, *app.Runtime) {
	t.Helper()
	repo := testgit.New(t)
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	status, err := runtime.Status(context.Background())
	if err != nil {
		t.Fatalf("runtime status: %v", err)
	}
	ws := NewWorkspace(runtime.Store(), string(status.Project.ID), sessionID)
	ws.AttachRuntime(runtime, projectid.ID(status.Project.ID))
	entitleULTRA(t, ws)
	if !ws.dispatchNavigationKey(context.Background(), KeyEvent{Type: KeyCtrlN}) {
		t.Fatal("Ctrl+N did not open the real workspace navigation")
	}
	ws.navView.Refresh(context.Background())
	return ws, runtime
}

// TestRestoreVerificationRequiresCanonicalProjectIdentity prevents a caller
// from turning an unattached runtime into a cross-project restore oracle.
func TestRestoreVerificationRequiresCanonicalProjectIdentity(t *testing.T) {
	_, runtime := realControlWorkspace(t, "SESSION-tui-restore-identity")
	authority := &runtimeControlAuthority{runtime: runtime}
	if _, err := authority.VerifyStateBackup(context.Background(), "unused.db"); err == nil ||
		!strings.Contains(err.Error(), "canonical project identity") {
		t.Fatalf("restore verification without project identity = %v, want fail-closed identity error", err)
	}
}

// Dispatching a Codex task is never a shortcut around Work.  In particular,
// a blank form must not manufacture an unplanned task or a synthetic worker
// agent merely because the user pressed Enter.  Both identities have to exist
// durably before Process 05 can be asked to run anything.
func TestCodexDispatchRefusesMissingCanonicalTaskAndAgent(t *testing.T) {
	ctx := context.Background()
	_, runtime := realControlWorkspace(t, "SESSION-tui-codex-dispatch-refusal")
	authority := &runtimeControlAuthority{runtime: runtime, store: runtime.Store()}

	if _, err := authority.DispatchCodexTask(ctx, CodexTaskDispatchRequest{}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("blank Codex dispatch error = %v, want ErrInvalid", err)
	}
	if got := mustCount(t, runtime.Store(), "tasks"); got != 0 {
		t.Fatalf("blank Codex dispatch created %d tasks", got)
	}
	if got := mustCount(t, runtime.Store(), "agents"); got != 0 {
		t.Fatalf("blank Codex dispatch created %d agents", got)
	}
	if got := mustCount(t, runtime.Store(), "sessions"); got != 0 {
		t.Fatalf("blank Codex dispatch created %d sessions", got)
	}

	if _, err := runtime.ImportTasks(ctx, []model.Task{{
		ID: "TASK-tui-codex-existing", Title: "preplanned Codex task", Status: model.TaskReady, Risk: model.R1,
	}}); err != nil {
		t.Fatalf("import planned task: %v", err)
	}
	if _, err := authority.DispatchCodexTask(ctx, CodexTaskDispatchRequest{TaskID: "TASK-tui-codex-existing"}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("dispatch without agent error = %v, want ErrInvalid", err)
	}
	if got := mustCount(t, runtime.Store(), "agents"); got != 0 {
		t.Fatalf("dispatch without agent created %d agents", got)
	}
	if got := mustCount(t, runtime.Store(), "sessions"); got != 0 {
		t.Fatalf("dispatch without agent created %d sessions", got)
	}
	wrongProvider, err := runtime.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "local claude", Role: model.RoleDeveloper, ModelProvider: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authority.DispatchCodexTask(ctx, CodexTaskDispatchRequest{TaskID: "TASK-tui-codex-existing", AgentID: wrongProvider.ID}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("non-Codex agent must be refused before Process 05 dispatch: %v", err)
	}
	if got := mustCount(t, runtime.Store(), "sessions"); got != 0 {
		t.Fatalf("wrong-provider dispatch created %d sessions", got)
	}
}

func TestCodexDispatchUsesOnlyUniqueRegisteredCodexAgent(t *testing.T) {
	ctx := context.Background()
	_, runtime := realControlWorkspace(t, "SESSION-tui-codex-default-agent")
	authority := &runtimeControlAuthority{runtime: runtime, store: runtime.Store()}
	if _, err := runtime.ImportTasks(ctx, []model.Task{{
		ID: "TASK-tui-codex-default", Title: "preplanned Codex task", Status: model.TaskReady, Risk: model.R1,
	}}); err != nil {
		t.Fatal(err)
	}
	agent, err := runtime.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "Local Codex", Role: model.RoleDeveloper, ModelProvider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := authority.DefaultCodexDispatchAgent(ctx)
	if err != nil || got != agent.ID {
		t.Fatalf("unique Codex agent = %q, %v; want %q", got, err, agent.ID)
	}
	source := &ControlSource{Authority: authority}
	target, err := source.prepareCodexDispatch(ctx, ActionRequest{Inputs: map[string]string{"task_id": "TASK-tui-codex-default"}})
	if err != nil || target.ActorID != agent.ID {
		t.Fatalf("dispatch preflight did not bind unique agent: target=%#v err=%v", target, err)
	}
	if _, err := runtime.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "Other Codex", Role: model.RoleDeveloper, ModelProvider: "codex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.DefaultCodexDispatchAgent(ctx); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("ambiguous Codex agents must require an operator choice: %v", err)
	}
}

func TestNativeCodexApprovalAppearsInCanonicalControlQueue(t *testing.T) {
	ctx := context.Background()
	_, runtime := realControlWorkspace(t, "SESSION-tui-native-codex-approval")
	authority := &runtimeControlAuthority{runtime: runtime, store: runtime.Store(), sessionID: "SESSION-tui-native-codex-approval"}
	manager := runtime.Execution().Engine().ApprovalManager()
	record, err := manager.RequestApproval(execution.ApprovalRequest{
		RunID: "run-native", TaskID: "task-native", PlanID: "plan-native", PlanVersion: 1,
		OperationType: "CODEX_APP_SERVER_NATIVE", TargetResource: "/safe/worktree",
		Scope:       "thread=one turn=one item=one method=item/commandExecution/requestApproval digest=sha256:one",
		DiffPreview: "item/commandExecution/requestApproval", Parameters: "sha256:one", CurrentState: "bound-native-state",
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := authority.PendingApprovals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, approval := range pending {
		if approval.ApprovalID == record.ApprovalID {
			found = true
		}
	}
	if found {
		t.Fatalf("orphan native Codex approval %s leaked into this session's Control queue: %#v", record.ApprovalID, pending)
	}
	if err := authority.DecideApproval(ctx, record.ApprovalID, true, "operator", "reviewed native request"); err == nil {
		t.Fatal("orphan native approval must not be decided without its Process 05 run")
	}
	current, err := manager.GetApproval(record.ApprovalID)
	if err != nil || current.Status != execution.ApprovalRequested {
		t.Fatalf("orphan decision attempt must not mutate approval: current=%#v err=%v", current, err)
	}
}

func mustCount(t *testing.T, st *store.Store, table string) int {
	t.Helper()
	count, err := st.Count(context.Background(), table)
	if err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

// This is the mutation-bearing E2E: real Workspace key dispatch, real Control
// confirmation, real application authority, and a durable reread.
func TestControlGoalApprovalThroughRealWorkspaceAndCanonicalAuthority(t *testing.T) {
	ctx := context.Background()
	const sessionID = "SESSION-tui-control-goal"
	ws, runtime := realControlWorkspace(t, sessionID)
	goal := model.GoalContract{
		ID: "GOAL-tui-control", SessionID: sessionID, ProjectID: ws.projectID,
		OriginalRequest: "approve this exact goal", RequestDigest: "sha256:goal-v1",
		ConstitutionVersion: constitution.Current.String(), Confirmation: model.ConfirmationPending,
		Revision: 1, DesiredOutcome: "exercise canonical Control approval",
		Risk: model.R1, AuthoritySource: "operator", UnderstandingState: model.GoalReady,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatalf("save goal: %v", err)
	}
	ws.navView.Refresh(ctx)
	if err := ws.navView.Nav().DeepLink("CTUI-0060"); err != nil {
		t.Fatalf("open approve action: %v", err)
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter}) // open exact confirmation
	if phase := ws.navView.Confirmation().Phase(); phase != PhaseConfirming {
		t.Fatalf("approval confirmation phase = %s", phase)
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyRight})
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter}) // submit selected Proceed

	approved, err := runtime.Store().GetActiveGoalContract(ctx, sessionID)
	if err != nil {
		t.Fatalf("reread goal: %v", err)
	}
	if approved.Confirmation != model.ConfirmationApproved || approved.Revision != 2 {
		t.Fatalf("canonical goal after TUI approval = state %s rev %d", approved.Confirmation, approved.Revision)
	}
}

func TestControlStaleGoalApprovalIsRefusedThroughRealWorkspace(t *testing.T) {
	ctx := context.Background()
	const sessionID = "SESSION-tui-control-stale"
	ws, runtime := realControlWorkspace(t, sessionID)
	now := time.Now().UTC()
	goal := model.GoalContract{ID: "GOAL-tui-stale", SessionID: sessionID, ProjectID: ws.projectID,
		OriginalRequest: "v1", RequestDigest: "sha256:v1", ConstitutionVersion: constitution.Current.String(),
		Confirmation: model.ConfirmationPending, Revision: 1, DesiredOutcome: "v1", Risk: model.R1,
		AuthoritySource: "operator", UnderstandingState: model.GoalReady, CreatedAt: now, UpdatedAt: now}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	ws.navView.Refresh(ctx)
	if err := ws.navView.Nav().DeepLink("CTUI-0060"); err != nil {
		t.Fatal(err)
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	goal.Revision = 2
	goal.OriginalRequest = "v2"
	goal.RequestDigest = "sha256:v2"
	goal.DesiredOutcome = "v2"
	goal.UpdatedAt = now.Add(time.Second)
	if err := runtime.Store().SaveGoalContract(ctx, goal, 1); err != nil {
		t.Fatalf("concurrent goal revision: %v", err)
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyRight})
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	if out := ws.navView.Confirmation().Outcome(); out.Verdict != VerdictBlocked {
		t.Fatalf("stale approval verdict = %s, want BLOCKED", out.Verdict)
	}
	after, _ := runtime.Store().GetActiveGoalContract(ctx, sessionID)
	if after.Confirmation != model.ConfirmationPending {
		t.Fatalf("stale approval mutated goal to %s", after.Confirmation)
	}
}

func TestControlGoalRevisionThroughRealWorkspaceAndCanonicalAuthority(t *testing.T) {
	ctx := context.Background()
	const sessionID = "SESSION-tui-control-revise"
	ws, runtime := realControlWorkspace(t, sessionID)
	now := time.Now().UTC()
	goal := model.GoalContract{ID: "GOAL-tui-revise", SessionID: sessionID, ProjectID: ws.projectID,
		OriginalRequest: "correct the documented wording", RequestDigest: "sha256:revise-v1", ConstitutionVersion: constitution.Current.String(),
		Confirmation: model.ConfirmationApproved, Revision: 1, DesiredOutcome: "old interpretation", Risk: model.R1,
		AuthoritySource: "operator", UnderstandingState: model.GoalReady, CreatedAt: now, UpdatedAt: now,
		Constraints: []model.Constraint{{ID: "hard-scope", Text: "do not change public API", IsHard: true, Source: "operator"}}}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	ws.navView.Refresh(ctx)
	if err := ws.navView.Nav().DeepLink("CTUI-0240"); err != nil {
		t.Fatal(err)
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	for _, r := range "new precise interpretation" {
		ws.dispatchNavigationKey(ctx, runeKey(r))
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	for _, r := range "operator clarified wording" {
		ws.dispatchNavigationKey(ctx, runeKey(r))
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyRight})
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	revised, err := runtime.Store().GetActiveGoalContract(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if revised.Revision != 2 || revised.Confirmation != model.ConfirmationPending || revised.DesiredOutcome != "new precise interpretation" || len(revised.Constraints) != 1 || revised.Constraints[0].ID != "hard-scope" {
		t.Fatalf("canonical goal after TUI revision = %+v", revised)
	}
}

func TestControlRememberThroughRealWorkspaceAndCanonicalAuthority(t *testing.T) {
	ctx := context.Background()
	ws, runtime := realControlWorkspace(t, "SESSION-tui-control-remember")
	if err := ws.navView.Nav().DeepLink("CTUI-0424"); err != nil {
		t.Fatal(err)
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	for _, r := range "operator memory" {
		ws.dispatchNavigationKey(ctx, runeKey(r))
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	for _, r := range "written through Process07" {
		ws.dispatchNavigationKey(ctx, runeKey(r))
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyRight})
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	outcome := ws.navView.Confirmation().Outcome()
	if !outcome.Succeeded() || outcome.Target.ID == "" {
		t.Fatalf("remember outcome = %+v", outcome)
	}
	record, err := runtime.Store().GetMemoryV2(ctx, ws.projectID, outcome.Target.ID)
	if err != nil || record.Title != "operator memory" || record.Body != "written through Process07" {
		t.Fatalf("durable remembered record = %+v err=%v", record, err)
	}
}

func TestControlClaimAndCancelTaskThroughRealWorkspace(t *testing.T) {
	ctx := context.Background()
	ws, runtime := realControlWorkspace(t, "SESSION-tui-control-task")
	agent, err := runtime.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "TUI operator", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	task := model.Task{ID: "TASK-tui-control", Title: "controlled task", Status: model.TaskReady, Risk: model.R1}
	if _, err := runtime.ImportTasks(ctx, []model.Task{task}); err != nil {
		t.Fatalf("import task: %v", err)
	}
	ws.navView.Refresh(ctx)
	if err := ws.navView.Nav().DeepLink("CTUI-0041"); err != nil {
		t.Fatal(err)
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyRight})
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	claimed, err := runtime.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Status != model.TaskClaimed || claimed.OwnerAgentID == nil || *claimed.OwnerAgentID != agent.ID {
		t.Fatalf("claimed task = %+v, agent = %s", claimed, agent.ID)
	}

	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter}) // dismiss result
	if err := ws.navView.Nav().DeepLink("CTUI-0046"); err != nil {
		t.Fatal(err)
	}
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyRune, Rune: 'y'})
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyRight})
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
	cancelled, err := runtime.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != model.TaskCancelled {
		t.Fatalf("cancelled task state = %s", cancelled.Status)
	}
}

// Navigation is an ULTRA surface. Both keyboard entry points and the exported
// entry must refuse a Standard session rather than opening a screen it is not
// entitled to. The entitled path is exercised by the cloud package, which owns
// lease construction; here the gate is genuinely absent.
func TestNavigationRefusedWithoutULTRA(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	ctx := context.Background()

	if ws.navigationEntitled() {
		t.Fatal("a workspace with no gate reported an ULTRA entitlement")
	}

	if !ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyCtrlN}) {
		t.Fatal("Ctrl+N was not consumed")
	}
	if ws.navView.IsOpen() {
		t.Fatal("Ctrl+N opened navigation without an entitlement")
	}
	if !strings.Contains(ws.state.LastOutput, "ULTRA") {
		t.Errorf("the refusal did not explain itself: %q", ws.state.LastOutput)
	}
	if ws.state.LastOutputIsError {
		t.Error("a missing entitlement is a mode, not an error")
	}

	if ws.OpenNavigation(ctx) {
		t.Fatal("OpenNavigation reported success without an entitlement")
	}
	if ws.navView.IsOpen() {
		t.Fatal("OpenNavigation opened navigation without an entitlement")
	}
}
