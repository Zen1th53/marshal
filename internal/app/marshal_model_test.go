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
	if err := json.Unmarshal([]byte(`{"tasks":[{"id":"a","title":"write a","criteria":["a exists"],"paths":["a.txt"],"depends_on":[],"worker":"codex","checks":["test -f a.txt"]}]}`), &proposal); err != nil {
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
