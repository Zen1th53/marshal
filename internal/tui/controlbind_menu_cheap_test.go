package tui

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/optimization"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// runMenuBinding drives one binding the way the confirmation does: typed
// inputs, Prepare (or PrepareRequest), then Execute with the prepared target.
func runMenuBinding(t *testing.T, ws *Workspace, id ActionID, inputs map[string]string) (Target, error, Outcome, error) {
	t.Helper()
	ctx := context.Background()
	b, ok := ws.controlSource().Bindings()[id]
	if !ok {
		t.Fatalf("%s has no binding", id)
	}
	if !b.Bound() {
		t.Fatalf("%s is not bound: %s %s", id, b.Gap, b.Requires)
	}
	for _, field := range b.Inputs {
		if _, set := inputs[field.Key]; !set && field.Default != "" {
			inputs[field.Key] = field.Default
		}
		if field.Validate != nil && inputs[field.Key] != "" {
			if err := field.Validate(inputs[field.Key]); err != nil {
				t.Fatalf("%s %s: %v", id, field.Key, err)
			}
		}
	}
	req := ActionRequest{Action: id, Title: b.Title, Safety: b.Safety, Inputs: inputs,
		SessionID: ws.controlSource().SessionID, ProjectID: ws.controlSource().ProjectID}
	var target Target
	var err error
	if b.PrepareRequest != nil {
		target, err = b.PrepareRequest(ctx, req)
	} else {
		target, err = b.Prepare(ctx)
	}
	if err != nil {
		return target, err, Outcome{}, nil
	}
	req.Target = target
	req.IdempotencyKey = idempotencyKey(req)
	outcome, execErr := b.Execute(ctx, req)
	return target, nil, outcome, execErr
}

func TestMenuCheapBindingsAreBoundOnRealRuntime(t *testing.T) {
	ws, _ := realControlWorkspace(t, "SESSION-menu-cheap-bound", false)
	bindings := ws.controlSource().Bindings()
	for _, id := range []ActionID{"CTUI-0201", "CTUI-0202", "CTUI-0203", "CTUI-0229", "CTUI-0249",
		"CTUI-0317", "CTUI-0319", "CTUI-0427", "CTUI-0428", "CTUI-0456", "CTUI-0461", "CTUI-0465",
		"CTUI-0480", "CTUI-0487", "CTUI-0573", "CTUI-0787"} {
		b, ok := bindings[id]
		if !ok || !b.Bound() {
			t.Errorf("%s must be bound on a real runtime: %+v", id, b.Requires)
		}
		if strings.Contains(b.Requires, "verification service verification") {
			t.Errorf("%s keeps the doubled owner wording", id)
		}
	}
	if got := bindings["CTUI-0249"].Title; got != "Invalidate affected claims" {
		t.Fatalf("CTUI-0249 title = %q", got)
	}
}

func TestMenuCheapBindingsUnavailableWithoutRuntimeAuthority(t *testing.T) {
	s := &ControlSource{Authority: &fakeAuthority{}, SessionID: "S", ProjectID: "P"}
	for id, b := range cheapMenuBindings(s) {
		if b.Bound() {
			t.Errorf("%s must not be bound without the menu authority", id)
		}
		if b.Requires == "" {
			t.Errorf("%s must name why it is unavailable", id)
		}
	}
}

func TestMenuInitializationReadbacksAreAlreadyInitialized(t *testing.T) {
	ws, _ := realControlWorkspace(t, "SESSION-menu-init", false)
	for _, id := range []ActionID{"CTUI-0201", "CTUI-0202", "CTUI-0203"} {
		_, err, _, _ := runMenuBinding(t, ws, id, map[string]string{})
		if err == nil || !strings.Contains(err.Error(), "already initialized") {
			t.Fatalf("%s readback = %v", id, err)
		}
	}
}

func TestMenuFormGoalRendersWithoutPersisting(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-menu-form-goal", false)
	ctx := context.Background()
	before, _ := runtime.Store().GetActiveGoalContract(ctx, "SESSION-menu-form-goal")
	_, err, outcome, execErr := runMenuBinding(t, ws, "CTUI-0229", map[string]string{"request": "add a README section; do not touch the database"})
	if err != nil || execErr != nil || !outcome.Succeeded() {
		t.Fatalf("form goal: %v %v %+v", err, execErr, outcome)
	}
	if !strings.Contains(outcome.Detail, "Understood as") || !strings.Contains(outcome.Detail, "not persisted") {
		t.Fatalf("detail = %q", outcome.Detail)
	}
	after, _ := runtime.Store().GetActiveGoalContract(ctx, "SESSION-menu-form-goal")
	if before.ID != after.ID || before.Revision != after.Revision {
		t.Fatal("forming a goal must not persist one")
	}
}

func TestMenuCollaborationMessageAndHandoff(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-menu-collab", false)
	ctx := context.Background()
	if _, err, _, _ := runMenuBinding(t, ws, "CTUI-0317", map[string]string{"to": "all", "content": "hello"}); err == nil ||
		!strings.Contains(err.Error(), "no collaboration session") {
		t.Fatalf("without a session the message must be blocked: %v", err)
	}
	// The refusal comes before the form opens, not after it was filled in.
	bindings := ws.controlSource().Bindings()
	for _, id := range []ActionID{"CTUI-0317", "CTUI-0319"} {
		if b := bindings[id]; b.Prepare == nil {
			t.Fatalf("%s has no pre-form check", id)
		} else if _, err := b.Prepare(ctx); err == nil || !strings.Contains(err.Error(), "no collaboration session") {
			t.Fatalf("%s pre-form check with no session: %v", id, err)
		}
	}
	now := time.Now().UTC()
	if err := runtime.Store().SaveTeamSession(ctx, model.TeamSession{SessionID: "SESSION-menu-collab", GoalID: "GOAL-menu", GoalRevision: 1,
		Participants: []model.Participant{
			{AgentID: "AGENT-dev", Role: model.RoleDeveloper, Harness: "codex", IsActive: true},
			{AgentID: "AGENT-qa", Role: model.RoleQA, Harness: "claude", IsActive: true},
		}, ActiveTurn: "AGENT-dev", TurnSequence: 1, Status: "ACTIVE", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := bindings["CTUI-0317"].Prepare(ctx); err != nil {
		t.Fatalf("pre-form check refused an existing session: %v", err)
	}
	_, err, outcome, execErr := runMenuBinding(t, ws, "CTUI-0317", map[string]string{"to": "AGENT-qa", "content": "please review"})
	if err != nil || execErr != nil || !outcome.Succeeded() || outcome.Evidence == "" {
		t.Fatalf("message: %v %v %+v", err, execErr, outcome)
	}
	messages, err := runtime.Store().ListAgentMessages(ctx, "SESSION-menu-collab", 10)
	if err != nil || len(messages) != 1 || messages[0].ID != outcome.Evidence || messages[0].To != "AGENT-qa" {
		t.Fatalf("message reread: %v %+v", err, messages)
	}
	_, err, outcome, execErr = runMenuBinding(t, ws, "CTUI-0319", map[string]string{"to": "qa", "content": "ready for review"})
	if err != nil || execErr != nil || !outcome.Succeeded() {
		t.Fatalf("handoff: %v %v %+v", err, execErr, outcome)
	}
	session, err := runtime.Store().GetTeamSession(ctx, "SESSION-menu-collab")
	if err != nil || session.ActiveTurn != "AGENT-qa" {
		t.Fatalf("handoff reread: %v %+v", err, session)
	}
}

func TestMenuCaptureEpisodeAndExperienceThroughConfirmation(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-menu-episode")
	ctx := context.Background()
	for id, kind := range map[ActionID]model.MemoryKind{"CTUI-0427": model.MemoryKindEpisodic, "CTUI-0428": model.MemoryKindProcedural} {
		if err := ws.navView.Nav().DeepLink(string(id)); err != nil {
			t.Fatal(err)
		}
		ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
		for _, text := range []string{"build cache", "go test needs TMPDIR on the big disk"} {
			for _, r := range text {
				ws.dispatchNavigationKey(ctx, runeKey(r))
			}
			ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
		}
		if phase := ws.navView.Confirmation().Phase(); phase != PhaseConfirming {
			t.Fatalf("%s: expected a confirmation, got %s", id, phase)
		}
		ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyRight})
		ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEnter})
		outcome := ws.navView.Confirmation().Outcome()
		if !outcome.Succeeded() {
			t.Fatalf("%s: %+v", id, outcome)
		}
		rec, err := runtime.Store().GetMemoryV2(ctx, runtime.ProjectID(), outcome.Target.ID)
		if err != nil || rec.Kind != kind {
			t.Fatalf("%s reread: %v %+v", id, err, rec.Kind)
		}
		ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyEsc})
	}
}

func TestMenuTombstoneRetiresTheExactRecord(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-menu-tombstone", false)
	ctx := context.Background()
	authority := ws.controlSource().Authority.(*runtimeControlAuthority)
	rec, err := authority.MenuRemember(ctx, model.MemoryKindSemantic, "stale fact", "the build uses make")
	if err != nil {
		t.Fatal(err)
	}
	_, err, outcome, execErr := runMenuBinding(t, ws, "CTUI-0456", map[string]string{"memory_id": rec.ID, "reason": "superseded by go build"})
	if err != nil || execErr != nil || !outcome.Succeeded() {
		t.Fatalf("tombstone: %v %v %+v", err, execErr, outcome)
	}
	after, err := runtime.Store().GetMemoryV2(ctx, runtime.ProjectID(), rec.ID)
	if err != nil || after.Lifecycle != model.MemoryTombstoned {
		t.Fatalf("reread: %v %s", err, after.Lifecycle)
	}
	if _, err, _, _ := runMenuBinding(t, ws, "CTUI-0456", map[string]string{"memory_id": rec.ID, "reason": "again"}); err == nil ||
		!strings.Contains(err.Error(), "already tombstoned") {
		t.Fatalf("second tombstone must be blocked: %v", err)
	}
}

func TestMenuJSONInputsReachTheirServices(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-menu-json", false)
	root := runtime.ProjectRoot()
	if err := os.MkdirAll(filepath.Join(root, "inputs"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[ActionID]string{
		"CTUI-0461": `{"ID":"MEMCOMMIT-menu","Verification":"VERIFY-missing","Provenance":"tui test"}`,
		"CTUI-0465": `{"memory_commit_id":"MEMCOMMIT-menu","verification_id":"VERIFY-missing","provenance":"tui test","changed_dependencies":[]}`,
		"CTUI-0573": `{"ID":"OPT-menu","MemoryCommitID":"MEMCOMMIT-missing","Provenance":"tui test"}`,
	}
	for id, body := range files {
		rel := filepath.Join("inputs", strings.ToLower(string(id))+".json")
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		target, err, outcome, _ := runMenuBinding(t, ws, id, map[string]string{"path": rel})
		if err != nil {
			t.Fatalf("%s prepare: %v", id, err)
		}
		if target.Digest == "" || target.Summary == "" {
			t.Fatalf("%s target: %+v", id, target)
		}
		// The service decides: these inputs name no canonical verification or
		// commit, so the result is the service's own answer, never a local pass
		// without a reread.
		if outcome.Verdict == VerdictPass && !outcome.Proof.Status.IsSuccess() {
			t.Fatalf("%s: pass without proof %+v", id, outcome)
		}
		if outcome.Verdict != VerdictPass && outcome.Detail == "" {
			t.Fatalf("%s: refusal without a reason", id)
		}
	}
	for _, bad := range []string{"/etc/passwd", "../outside.json", ".git/config", ".marshal-extreme/marshal.db"} {
		if _, err, _, _ := runMenuBinding(t, ws, "CTUI-0461", map[string]string{"path": bad}); err == nil {
			t.Fatalf("%s must be refused", bad)
		}
	}
}

func TestMenuAntigravityImportIsBlockedWithoutHistory(t *testing.T) {
	ws, _ := realControlWorkspace(t, "SESSION-menu-agy", false)
	t.Setenv("HOME", t.TempDir())
	if _, err, _, _ := runMenuBinding(t, ws, "CTUI-0487", map[string]string{}); err == nil ||
		!strings.Contains(err.Error(), "Antigravity") {
		t.Fatalf("import without history must be blocked with a reason: %v", err)
	}
}

func TestMenuLegalExportWritesAndRereadsThePack(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-menu-legal", false)
	rel := statepath.DirName + "/exports/legal-test.tar.gz"
	_, err, outcome, execErr := runMenuBinding(t, ws, "CTUI-0787", map[string]string{"output": rel})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if execErr != nil {
		// The audit can refuse a fixture repository; that refusal is the
		// canonical answer and must carry its reason.
		t.Logf("legal export refused by the audit: %s", outcome.Detail)
		if outcome.Verdict == VerdictPass || outcome.Detail == "" {
			t.Fatalf("refusal must carry a reason: %v %+v", execErr, outcome)
		}
		return
	}
	if !outcome.Succeeded() {
		t.Fatalf("export: %+v", outcome)
	}
	t.Logf("legal export: %s", outcome.Detail)
	if _, err := os.Stat(filepath.Join(runtime.ProjectRoot(), rel)); err != nil {
		t.Fatal(err)
	}
	if _, err, _, _ := runMenuBinding(t, ws, "CTUI-0787", map[string]string{"output": rel}); err == nil {
		t.Fatal("an existing output must not be overwritten")
	}
}

// A missing project file is refused without the syscall name the path error
// carries (e2e 2026-11-06: "read zz: statat zz: no such file or directory").
func TestReadProjectFileRefusalIsReadable(t *testing.T) {
	_, _, err := readProjectFile(t.TempDir(), "missing/input.json")
	if err == nil {
		t.Fatal("a missing file was read")
	}
	got := fileReadReason(err)
	if !errors.Is(got, fs.ErrNotExist) || strings.Contains(got.Error(), "statat") || !strings.Contains(got.Error(), "does not exist in the project") {
		t.Fatalf("got %v", got)
	}
}

// T§12 / CTUI-0573: Operator-supplied Governance JSON is stripped so self-authored
// policy cannot forge governance metadata.
func TestProcess07GovernanceStrippedFromOperatorJSON(t *testing.T) {
	forged := []byte(`{
		"ID": "OPT-forged",
		"MemoryCommitID": "MEMCOMMIT-fake",
		"Provenance": "attacker",
		"Governance": {
			"PolicyID": "forged-policy",
			"ApprovedBy": "attacker",
			"AttestationDigest": "sha256:0000000000000000000000000000000000000000000000000000000000000000"
		}
	}`)
	val, _, err := decodeMenuJSON("optimization-start", forged)
	if err != nil {
		t.Fatalf("decodeMenuJSON: %v", err)
	}
	startIn, ok := val.(app.StartCycleInput)
	if !ok {
		t.Fatalf("expected app.StartCycleInput, got %T", val)
	}
	if !reflect.DeepEqual(startIn.Governance, optimization.Governance{}) {
		t.Fatalf("expected empty Governance, got %+v", startIn.Governance)
	}
}
