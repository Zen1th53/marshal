//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/verification"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// The legacy imported-task entry point is deliberately unavailable in the
// private edition. Keep its old success coverage in Community and assert the
// actual earlier refusal and absence of claim/session/provider effects here.
func assertLegacyRunFixtureRefused(t *testing.T, r *Runtime, before model.Task, result RunResult, err error) bool {
	t.Helper()
	if !errors.Is(err, projectcontext.ErrReviewPending) || result.RunID != "" {
		t.Fatal("legacy Run reached an unsupported private execution route", err, result)
	}
	after, readErr := r.Task(t.Context(), before.ID)
	if readErr != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("refused legacy Run changed the imported task", readErr)
	}
	if n, countErr := r.store.Count(t.Context(), "sessions"); countErr != nil || n != 0 {
		t.Fatal("refused legacy Run created a session", n, countErr)
	}
	return true
}

func privateLegacyRunFixture() bool { return true }

func assertLegacyRunFixtureRefusedWithExistingLease(t *testing.T, r *Runtime, before model.Task, result RunResult, err error) {
	t.Helper()
	if !errors.Is(err, projectcontext.ErrReviewPending) || result.RunID != "" {
		t.Fatal("assigned legacy task bypassed required context", err)
	}
	after, readErr := r.Task(t.Context(), before.ID)
	if readErr != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("refusal changed an existing assignment", readErr)
	}
	lease, leaseErr := r.store.ActiveLease(t.Context(), before.ID)
	if leaseErr != nil || before.OwnerAgentID == nil || lease.AgentID != *before.OwnerAgentID || lease.TaskRevision != before.Revision {
		t.Fatal("refusal consumed or replaced the existing assignment lease", leaseErr)
	}
	if runs, readErr := r.store.WorkerRunsByAdapter(t.Context(), "codex", 10); readErr != nil || len(runs) != 0 {
		t.Fatal("refused assignment created a worker run", len(runs), readErr)
	}
}

// These Process05 tests retain their real canonical planning/approval/engine
// flow and actual owned callback with simulated adapter transport. Give that Runtime a separately accepted full contract
// through the existing proposal/Decision fixture, rather than inventing a pin
// on a store-only Runtime or bypassing the production admission callback.
func requiredProcess05TestRuntime(t *testing.T, fixture *MarshalService) *Runtime {
	t.Helper()
	settings, err := fixture.Store.GetMarshalSettings(t.Context(), fixture.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	installRequiredHostVerifierFixture(t)
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	clients := map[string]adapter.Adapter{}
	for _, name := range []string{"codex", "agy"} {
		clients[name] = &requiredOwnedFixtureAdapter{provider: name}
	}
	r, err := OpenWithOptions(t.Context(), repo.Path(), Options{Adapters: clients})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	_, _, rec, _ := requiredContextFixtureForRuntime(t, r)
	for _, client := range clients {
		client.(*requiredOwnedFixtureAdapter).required = rec.Body
	}
	current, err := r.store.GetMarshalSettings(t.Context(), r.ProjectID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.SetMarshalSettings(t.Context(), r.ProjectID(), settings.Value, current.Revision); err != nil {
		t.Fatal(err)
	}
	fixture.Store, fixture.ProjectID = r.Store(), r.ProjectID()
	fixture.Repository, fixture.Worktrees = r.ProjectRoot(), r.layout.Worktrees
	for _, provider := range []string{"codex", "agy"} {
		if _, err := r.RegisterAgent(t.Context(), RegisterAgentRequest{Name: "owned Process05 fixture " + provider, Role: model.RoleDeveloper, ModelProvider: provider}); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func requiredProcess05TestProjectID(r *Runtime, _ string) string { return r.ProjectIdentity() }

func requiredTestOwnerContext(t *testing.T, r *Runtime, ctx context.Context) context.Context {
	t.Helper()
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return control.Context(ctx)
}

func requiredMarshalTestPrompt(t *testing.T, _ *MarshalCLI, original, observed string) string {
	t.Helper()
	for _, required := range []string{original, "Preserve original accepted history and evidence.", "Preserve reliable project execution"} {
		if !strings.Contains(observed, required) {
			t.Fatalf("actual owner model command omitted required context %q", required)
		}
	}
	return observed
}

func requiredTestAcceptedProjectContract(t *testing.T, r *Runtime) {
	t.Helper()
	requiredAcceptedContextFixture(t, r)
}

// Report statuses use an actual retained host integration source. They do not
// synthesize an accepted completion or bypass the private verifier boundary.
func requiredGoalReportFixture(t *testing.T) (*Runtime, context.Context, model.GoalContract, *execution.ExecutionRun) {
	criteria := []string{"verified", "failed", "not run", "unknown", "missing", "expired", "unmapped"}
	r, ctx, service, id, run, head, root := integrationSourceFixtureForCriteria(t, criteria)
	if err := r.observeRequiredMarshalIntegrationSource(ctx, service, id, run, head); err != nil {
		t.Fatal(err)
	}
	goal, err := r.store.GetGoalContract(ctx, root.GoalID, root.GoalRevision)
	if err != nil {
		t.Fatal(err)
	}
	return r, ctx, goal, &root
}

// A conventional mock-only run without retained integration and required
// context cannot be represented as an authoritative Process06 handoff.
func assertUnqualifiedHandoffFixtureRefused(t *testing.T, r *Runtime, runID string, err error) bool {
	t.Helper()
	if err == nil {
		t.Fatal("unqualified host mock acquired a private completion binding")
	}
	if _, readErr := r.Verification().CurrentForRun(t.Context(), runID); !errors.Is(readErr, verification.ErrNotFound) {
		t.Fatal("refused handoff minted verification", readErr)
	}
	if _, readErr := r.Verification().BindingForRun(t.Context(), runID); readErr == nil {
		t.Fatal("unqualified run acquired authoritative source")
	}
	return true
}

// Pin all discoverable native roles to an owned model-free executable before
// exercising the canonical verifier. Only explicit local tool symlinks are in
// PATH: an accidental real native CLI fallback is impossible.
func installRequiredHostVerifierFixture(t *testing.T) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!" + python + `
import json,sys,uuid
if '--version' in sys.argv: print('1.0.0');sys.exit(0)
if '--help' in sys.argv: print('owned-host-fixture --output-format --json-schema --permission-mode --restricted --sandbox');sys.exit(0)
prompt=sys.argv[sys.argv.index('-p')+1]
if 'Exact runtime input: ' in prompt:
 data,_=json.JSONDecoder().raw_decode(prompt.split('Exact runtime input: ',1)[1])
 verdict={'head':data['Head'],'tree':data['Tree'],'contract_digest':data['ContractDigest'],'assessments':[{'obligation_id':x,'status':'PASS','reason':'Explicit isolated host architecture fixture','evidence_refs':['obligation:'+x,'workspace:'+data['Tree']]} for x in data['Obligations']]}
else:
 data,_=json.JSONDecoder().raw_decode(prompt.split('Native model identity comes from actual CLI metadata, never your answer.\n',1)[1])
 verdict={'head':data['head'],'assessments':[{'id':x['id'],'status':'PASS','reason':'Explicit isolated host verifier fixture (runtime-proof.json:1)','check_ids':x['check_ids']} for x in data['criteria']]}
print(json.dumps({'session_id':'host-'+str(uuid.uuid4()),'structured_output':verdict,'modelUsage':{'host-fixture':{'canonicalModel':'host-fixture'}}}))
`
	for _, provider := range []string{"claude", "codex", "opencode", "agy"} {
		if err := os.WriteFile(filepath.Join(bin, provider), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"git", "sh", "grep"} {
		tool, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(tool, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

func requiredProcess05TestHarnessName(string) string { return "codex" }
func requiredProcess05PlanRequest(request CreatePlanRequest) CreatePlanRequest {
	request.HarnessCandidates[0].Profile.Harness = "codex"
	request.HarnessCandidates[0].Provider = "codex"
	request.HarnessCandidates[0].Capacity = goalintake.UnknownCapacity("codex", true)
	request.Candidates[0].Provider = "codex"
	request.Candidates[0].Capacity = goalintake.UnknownCapacity("codex", true)
	for i := range request.Tasks {
		if request.Tasks[i].Instructions == "" {
			request.Tasks[i].Instructions = "Complete the approved task within its exact owned files"
		}
	}
	return request
}

// Replace only the simulated transport installed before Runtime construction.
// Canonical Engine admission, required memory and original task approval stay live.
func requiredRegisterProcess05TestHarness(t *testing.T, r *Runtime, name string, handle func(context.Context, execution.TaskExecution, execution.ConstraintPackage, string) (execution.TaskResult, error)) {
	t.Helper()
	client, ok := r.adapters[name].(*requiredOwnedFixtureAdapter)
	if !ok {
		t.Fatal("owned adapter was not installed before Runtime construction")
	}
	client.handle = func(ctx context.Context, req adapter.Request) (adapter.Result, error) {
		// The owned fixture is a launched backend: acknowledge launch so the
		// dispatch barrier and run lock are released before canonical reads.
		execution.AcknowledgeWorkerLaunch(ctx)
		runs, err := r.Execution().ListRuns(ctx)
		if err != nil {
			return adapter.Result{}, err
		}
		var selected execution.TaskExecution
		count := 0
		for _, run := range runs {
			for _, task := range run.Tasks {
				if task.State == execution.TaskRunning && task.CanonicalTaskID == req.TaskID && task.WorktreePath == req.Worktree {
					selected = task
					count++
				}
			}
		}
		if count != 1 {
			return adapter.Result{}, fmt.Errorf("owned fixture current task selection: %d", count)
		}
		started := time.Now().UTC()
		result, err := handle(ctx, selected, execution.ConstraintPackage{}, req.Worktree)
		if err != nil {
			return adapter.Result{}, err
		}
		status := adapter.StatusFailure
		if result.Success {
			status = adapter.StatusSuccess
		}
		return adapter.Result{Adapter: name, AdapterVersion: "owned-host-adapter-fixture", Model: req.Model, SessionID: "mock-session", Status: status, Stdout: []byte("owned host fixture completed"), StartedAt: started, EndedAt: time.Now().UTC()}, nil
	}
}

func requiredProcess05TestModelProvider(original string) string {
	if original == "test-harness" {
		return "codex"
	}
	return "claude"
}
