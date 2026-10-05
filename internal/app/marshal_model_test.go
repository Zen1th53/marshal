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
	if err := json.Unmarshal([]byte(`{"tasks":[{"id":"a","title":"write a","criteria":["a exists"],"paths":["a.txt"],"depends_on":[],"worker":"codex","checks":[{"command":"test -f a.txt","criteria":["a exists"]}]}]}`), &proposal); err != nil {
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
case "$prompt" in *"copy each criterion string verbatim"*"runtime-assigned worktree"*) ;; *) exit 99 ;; esac
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"tasks\":[{\"id\":\"a\",\"title\":\"write a\",\"criteria\":[\"a exists\"],\"paths\":[\"a.txt\"],\"depends_on\":[],\"worker\":\"codex\",\"checks\":[{\"command\":\"test -f a.txt\",\"criteria\":[\"a exists\"]}],\"instructions\":\"Use assigned worktree\",\"expected_output\":\"a.txt\"}]}"}}'
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	m := MarshalCLI{Provider: "codex", Binary: binary, Dir: root, ProjectID: "PROJECT-0123456789abcdef0123456789abcdef"}
	if _, err := m.Draft(t.Context(), "write a.txt"); err != nil {
		t.Fatal(err)
	}
}
