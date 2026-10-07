package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/verification"
)

func TestMarshalCLIDefaultBinaryRunsSelectedProvider(t *testing.T) {
	root := t.TempDir()
	stub := `#!/bin/sh
printf '%s\n' '{"type":"thread.started","thread_id":"thread-test"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"ok\":true}"}}'
`
	if err := os.WriteFile(filepath.Join(root, "codex"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	cli := &MarshalCLI{Provider: "codex", Dir: root}
	var output struct {
		OK bool `json:"ok"`
	}
	if err := cli.turn(context.Background(), "test", marshalDraftSchema, &output); err != nil {
		t.Fatal(err)
	}
	if !output.OK || cli.ConversationID != "thread-test" {
		t.Fatalf("default provider binary did not return a model turn: %+v session=%q", output, cli.ConversationID)
	}
}

func TestMarshalCLIIndependentVerifierBindsVerdictToHead(t *testing.T) {
	root := t.TempDir()
	stub := `#!/bin/sh
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"head\":\"wrong\",\"verdict\":\"pass\",\"findings\":[]}"}}'
`
	path := filepath.Join(root, "codex")
	if err := os.WriteFile(path, []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	cli := &MarshalCLI{Provider: "codex", Binary: path, Dir: root}
	if err := cli.Verify(t.Context(), marshal.Run{}, "expected", verification.Session{}); err == nil {
		t.Fatal("verifier accepted a verdict for another commit")
	}
}

func TestMarshalCLIMaterializesGraphFromTaskProposal(t *testing.T) {
	m := &MarshalCLI{ProjectID: "PROJECT-0123456789abcdef0123456789abcdef"}
	var proposal marshalTaskProposal
	if err := json.Unmarshal([]byte(`{"tasks":[{"id":"a","title":"write a","mode":"native","criteria":["a exists"],"paths":["a.txt"],"depends_on":[],"worker":"codex","checks":[{"command":"test -f a.txt","criteria":["a exists"]}]}]}`), &proposal); err != nil {
		t.Fatal(err)
	}
	draft, err := m.materialize(proposal, "", 1, []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Plan.Graph.Digest == "" || draft.Plan.Tasks[0].ID != draft.Tasks[0].PlanTaskID || draft.Tasks[0].Mode != marshal.Native {
		t.Fatalf("invalid materialized draft: %+v", draft)
	}
	proposal.Tasks[0].Worker = "unknown"
	if _, err := m.materialize(proposal, "", 1, []string{"codex"}); err == nil {
		t.Fatal("unavailable worker was accepted")
	}
}

func TestMarshalCLIRealModelDraft(t *testing.T) {
	if os.Getenv("MARSHAL_REAL_MODEL") != "1" {
		t.Skip("set MARSHAL_REAL_MODEL=1 for a live provider turn")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("init test project: %v: %s", err, out)
	}
	m := &MarshalCLI{Provider: "codex", Dir: root, ProjectID: "PROJECT-0123456789abcdef0123456789abcdef"}
	draft, err := m.Draft(t.Context(), "Create hello.txt containing the word hello; verify with test -f hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDraft(draft); err != nil {
		t.Fatal(err)
	}
}

func TestMarshalCLIRealModelVerifier(t *testing.T) {
	if os.Getenv("MARSHAL_REAL_MODEL") != "1" {
		t.Skip("set MARSHAL_REAL_MODEL=1 for a live provider turn")
	}
	root := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.name", "Verifier Test"}, {"config", "user.email", "verifier@example.invalid"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "hello.txt"}, {"commit", "-m", "add hello"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	output, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(output))
	cli := &MarshalCLI{Provider: "codex", Dir: root}
	run := marshal.Run{GoalBinding: "Add hello.txt containing hello", Tasks: []marshal.Task{{PlanTaskID: "hello", Criteria: []string{"hello.txt contains hello"}, Files: []string{"hello.txt"}, Checks: []marshal.Check{{Command: "grep -qx hello hello.txt"}}}}}
	session := verification.Session{RequiredChecks: map[string]verification.Status{"hello#0": verification.StatusPass}}
	if err := cli.Verify(t.Context(), run, head, session); err != nil {
		t.Fatal(err)
	}
}

func TestMarshalCLIRealModelReview(t *testing.T) {
	if os.Getenv("MARSHAL_REAL_MODEL") != "1" {
		t.Skip("set MARSHAL_REAL_MODEL=1 for a live provider turn")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("init test project: %v: %s", err, out)
	}
	cli := &MarshalCLI{Provider: "codex", Dir: root}
	task := marshal.Task{PlanTaskID: "hello", Criteria: []string{"hello.txt contains hello"}, Files: []string{"hello.txt"}, Checks: []marshal.Check{{Command: "grep -qx hello hello.txt", Criteria: []string{"hello.txt contains hello"}}}}
	handin := marshal.HandIn{Diff: "diff --git a/hello.txt b/hello.txt\nnew file mode 100644\n+hello\n", FilesTouched: []string{"hello.txt"}, CheckResults: []marshal.CheckResult{{Command: "grep -qx hello hello.txt", Criteria: task.Criteria, Passed: true}}}
	review, err := cli.Review(t.Context(), task, handin, marshal.ControlFree)
	if err != nil {
		t.Fatal(err)
	}
	if review.Verdict == "" || review.Reviewer == "" {
		t.Fatalf("incomplete real review: %+v", review)
	}
}

func TestMarshalDraftSchemaRequiresEveryProperty(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(marshalDraftSchema), &schema); err != nil {
		t.Fatal(err)
	}
	var check func(map[string]any)
	check = func(node map[string]any) {
		if props, ok := node["properties"].(map[string]any); ok {
			required := map[string]bool{}
			for _, key := range node["required"].([]any) {
				required[key.(string)] = true
			}
			for key, prop := range props {
				if !required[key] {
					t.Errorf("property %s missing from required", key)
				}
				check(prop.(map[string]any))
			}
		}
		if items, ok := node["items"].(map[string]any); ok {
			check(items)
		}
	}
	check(schema)
}

func TestMarshalWiredDraftUsesCanonicalProjectBinding(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	service, err := runtime.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	cli := service.Model.(*MarshalCLI)
	if cli.ProjectID != string(service.CanonicalPlanProjectID()) {
		t.Fatalf("model bound to %s instead of %s", cli.ProjectID, service.CanonicalPlanProjectID())
	}
}

func TestMarshalDraftBriefBindsExactCriteriaAndRuntimeWorktree(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "codex")
	script := `#!/bin/sh
for prompt; do :; done
case "$prompt" in *"fresh checkout of the committed result"*"clean sandbox"*"no worker environment, network or temporary files"*"only repository content"*) ;; *) exit 98 ;; esac
case "$prompt" in *"rerun after an integration merge"*"never commit history, HEAD diffs or commit structure"*"MARSHAL already records changed-file scope"*) ;; *) exit 97 ;; esac
case "$prompt" in *"amended JSON"*) ;; *"opencode defaults to native and also supports governed when requested"*"copy each criterion string verbatim"*"runtime-assigned worktree"*) ;; *) exit 99 ;; esac
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"tasks\":[{\"id\":\"a\",\"title\":\"write a\",\"criteria\":[\"a exists\"],\"paths\":[\"a.txt\"],\"depends_on\":[],\"worker\":\"codex\",\"checks\":[{\"command\":\"test -f a.txt\",\"criteria\":[\"a exists\"]}],\"instructions\":\"Use assigned worktree\",\"expected_output\":\"a.txt\"}]}"}}'
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	// Drafting lists installed worker CLIs; make the result independent of the host.
	fakeWorkerOnPath(t, "codex")
	m := MarshalCLI{Provider: "codex", Binary: binary, Dir: root, ProjectID: "PROJECT-0123456789abcdef0123456789abcdef"}
	if _, err := m.Draft(t.Context(), "write a.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Amend(t.Context(), marshal.Run{PlanID: "PLAN-test", PlanVersion: 1}, "add a check"); err != nil {
		t.Fatal(err)
	}
}

func TestMarshalProposalCarriesGovernedMode(t *testing.T) {
	for _, worker := range []string{"codex", "claude", "opencode"} {
		t.Run(worker, func(t *testing.T) {
			m := &MarshalCLI{ProjectID: "PROJECT-0123456789abcdef0123456789abcdef"}
			for _, mode := range []string{"governed", "native", ""} {
				var proposal marshalTaskProposal
				data := `{"tasks":[{"id":"a","title":"write a","mode":"` + mode + `","worker":"` + worker + `","criteria":["a exists"],"paths":["a.txt"],"checks":[{"command":"test -f a.txt","criteria":["a exists"]}]}]}`
				if err := json.Unmarshal([]byte(data), &proposal); err != nil {
					t.Fatal(err)
				}
				draft, err := m.materialize(proposal, "", 1, []string{worker})
				if err != nil {
					t.Fatal(err)
				}
				want := marshal.Governed
				if mode == "native" || (mode == "" && worker == "opencode") {
					want = marshal.Native
				}
				if draft.Tasks[0].Mode != want {
					t.Fatalf("mode %q: got %s want %s", mode, draft.Tasks[0].Mode, want)
				}
			}
		})
	}
}

func TestMarshalProposalRefusesInvalidOrUnsupportedModes(t *testing.T) {
	m := &MarshalCLI{ProjectID: "PROJECT-0123456789abcdef0123456789abcdef"}
	for _, entry := range []struct{ worker, mode string }{{"codex", "other"}, {"agy", "governed"}, {"opencode", "other"}} {
		var proposal marshalTaskProposal
		data := `{"tasks":[{"id":"a","title":"write a","mode":"` + entry.mode + `","worker":"` + entry.worker + `","criteria":["a exists"],"paths":["a.txt"],"checks":[{"command":"test -f a.txt","criteria":["a exists"]}]}]}`
		if err := json.Unmarshal([]byte(data), &proposal); err != nil {
			t.Fatal(err)
		}
		if _, err := m.materialize(proposal, "", 1, []string{entry.worker}); err == nil {
			t.Fatalf("accepted %+v", entry)
		}
	}
}

func TestGovernedProposalDispatchesGovernedDriver(t *testing.T) {
	for _, worker := range []string{"codex", "opencode"} {
		t.Run(worker, func(t *testing.T) { testGovernedProposalDispatchesGovernedDriver(t, worker) })
	}
}

func testGovernedProposalDispatchesGovernedDriver(t *testing.T, worker string) {
	t.Helper()
	s, _ := marshalFixture(t, 1)
	var proposal marshalTaskProposal
	if err := json.Unmarshal([]byte(`{"tasks":[{"id":"a","title":"write a","mode":"governed","worker":"`+worker+`","criteria":["a exists"],"paths":["a.txt"],"checks":[{"command":"test -f a.txt","criteria":["a exists"]}]}]}`), &proposal); err != nil {
		t.Fatal(err)
	}
	draft, err := (&MarshalCLI{ProjectID: s.ProjectID}).materialize(proposal, "", 1, []string{worker})
	if err != nil {
		t.Fatal(err)
	}
	s.Drivers = map[string]driver.Driver{worker: driver.Codex("must-not-start-native")}
	if worker == "opencode" {
		s.Drivers[worker] = driver.OpenCode("must-not-start-native")
	}
	called := false
	s.GovernedDrivers = map[string]driver.Driver{worker: driver.Governed{Provider: worker, Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		called = true
		return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("done"), 0600)
	}}}
	if _, err = s.StartPlanningFromDraft(t.Context(), "governed", "governed goal", draft, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Dispatch(t.Context(), "governed", "a", "write a"); err == nil {
		t.Fatal("unapproved plan dispatched")
	}
	if _, err = s.Approve(t.Context(), "governed"); err != nil {
		t.Fatal(err)
	}
	dispatch, err := s.Dispatch(t.Context(), "governed", "a", "write a")
	if err != nil {
		t.Fatal(err)
	}
	handin, err := s.CollectHandIn(t.Context(), "governed", dispatch)
	if err != nil {
		t.Fatal(err)
	}
	if !called || dispatch.Driver.Mode() != marshal.Governed || handin.Mode != marshal.Governed {
		t.Fatalf("governed proposal used wrong driver: %+v", handin)
	}
}

func TestGovernedDraftRefusesUnavailableProtectionWithoutNativeFallback(t *testing.T) {
	for _, worker := range []string{"codex", "opencode"} {
		t.Run(worker, func(t *testing.T) { testGovernedDraftRefusesUnavailableProtectionWithoutNativeFallback(t, worker) })
	}
}

func testGovernedDraftRefusesUnavailableProtectionWithoutNativeFallback(t *testing.T, worker string) {
	t.Helper()
	t.Setenv("MARSHAL_OPENCODE_MODEL", "opencode/test-free")
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	bin := t.TempDir()
	marker := filepath.Join(bin, "worker-started")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf 'test-version\\n'; exit 0; fi\nprintf ran > '" + marker + "'\n"
	if err := os.WriteFile(filepath.Join(bin, worker), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	service, err := runtime.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	var proposal marshalTaskProposal
	if err := json.Unmarshal([]byte(`{"tasks":[{"id":"a","title":"write a","mode":"governed","worker":"`+worker+`","criteria":["a exists"],"paths":["a.txt"],"checks":[{"command":"test -f a.txt","criteria":["a exists"]}]}]}`), &proposal); err != nil {
		t.Fatal(err)
	}
	draft, err := (&MarshalCLI{ProjectID: string(service.CanonicalPlanProjectID())}).materialize(proposal, "", 1, []string{worker})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.StartPlanningFromDraft(t.Context(), "protected", "governed goal", draft, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Approve(t.Context(), "protected"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Execute(t.Context(), "protected", func(marshal.Task, BriefContext) string { return "write a" }, nil); err == nil {
		t.Fatal("governed execution admitted without credential/protection setup")
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("native fallback started worker: %v", err)
	}
}
