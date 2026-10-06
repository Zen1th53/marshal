package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestMarshalImportPreservesShellCheck(t *testing.T) {
	check := `test "a  b" = 'a  b' && test "$(printf '%s' 'hello world')" = "hello world"`
	for _, prefix := range []string{"/marshal import TASK-one ", " \t/marshal\timport  TASK-one\t"} {
		got := marshalImportArgs(prefix + check + "  ")
		if !reflect.DeepEqual(got, []string{"TASK-one", check + "  "}) {
			t.Fatalf("check bytes changed: %#v", got)
		}
		if out, err := exec.Command("/bin/sh", "-c", got[1]).CombinedOutput(); err != nil {
			t.Fatalf("check failed: %s %v", out, err)
		}
	}
	for _, line := range []string{"/marshal import", "/marshal import TASK-one", "/marshal import TASK-one  "} {
		if args := marshalImportArgs(line); len(args) > 1 {
			t.Fatalf("missing check accepted: %#v", args)
		}
	}
}

func TestMarshalImportCommandStoresRawCheck(t *testing.T) {
	st, w, ctx := acceptanceWorkspace(t)
	t.Setenv("TMUX", "")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "")
	root := w.runtime.ProjectRoot()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	base := git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello  world\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "hello.txt")
	git("commit", "-m", "finished task")
	head := git("rev-parse", "HEAD")
	if _, err := w.runtime.ImportTasks(ctx, []model.Task{{ID: "TASK-finished", Title: "write greeting", Status: model.TaskReview, Risk: model.R1, BaseCommit: &base, HeadCommit: &head}}); err != nil {
		t.Fatal(err)
	}
	agent, err := w.runtime.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "worker", Role: model.RoleDeveloper, ModelProvider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.StartSession(ctx, model.SessionStart{ID: "SESSION-finished", AgentID: agent.ID, ProjectID: agent.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.StartRun(ctx, model.WorkerRun{ID: "RUN-finished", TaskID: "TASK-finished", SessionID: session.ID, Adapter: "codex", AdapterVersion: "test", StartedAt: time.Now().UTC(), BaseCommit: base, Status: "running"}); err != nil {
		t.Fatal(err)
	}
	zero := 0
	if err := st.FinishRun(ctx, model.RunFinish{ID: "RUN-finished", Status: "success", EndedAt: time.Now().UTC(), ResultCommit: head, ExitStatus: &zero}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ExecuteCommand(ctx, "/marshal model codex"); err != nil {
		t.Fatal(err)
	}
	check := `test "$(cat hello.txt)" = 'hello  world' && test -f hello.txt`
	if _, err := w.ExecuteCommand(ctx, "/marshal import TASK-finished "+check); err != nil {
		t.Fatal(err)
	}
	m := w.marshalSession()
	run, err := m.service.Snapshot(ctx, m.runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Tasks) != 1 || len(run.Tasks[0].Checks) != 1 || run.Tasks[0].Checks[0].Command != check {
		t.Fatalf("stored check changed: %+v", run.Tasks)
	}
	command := exec.Command("/bin/sh", "-c", run.Tasks[0].Checks[0].Command)
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("stored check fails: %s %v", out, err)
	}
	for _, quote := range []string{"'", `"`} {
		quotedCheck := `test -f hello.txt && test 13 = $(wc -c < hello.txt)`
		if _, err := w.ExecuteCommand(ctx, "/marshal import TASK-finished "+quote+quotedCheck+quote); err != nil {
			t.Fatal(err)
		}
		run, err := m.service.Snapshot(ctx, m.runID)
		if err != nil || run.Tasks[0].Checks[0].Command != quotedCheck {
			t.Fatalf("quoted import stored wrong source: %+v %v", run.Tasks, err)
		}
		command := exec.Command("/bin/sh", "-c", run.Tasks[0].Checks[0].Command)
		command.Dir = root
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("quoted imported check fails: %s %v", out, err)
		}
	}
}

func TestMarshalImportUnwrapsWholeCheck(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`'test -f hello.txt && test hi = $(cat hello.txt)'`, `test -f hello.txt && test hi = $(cat hello.txt)`},
		{`"test -f hello.txt && test hi = $(cat hello.txt)"`, `test -f hello.txt && test hi = $(cat hello.txt)`},
		{`'printf hi'  `, `printf hi`},
		{`"printf hi"`, `printf hi`},
		{`'printf hi"`, `'printf hi"`},
		{`test "hi" = 'hi'`, `test "hi" = 'hi'`},
		{`'printf' hi`, `'printf' hi`},
		{`'printf' 'hi'`, `'printf' 'hi'`},
		{`"printf" "hi"`, `"printf" "hi"`},
	} {
		got := marshalImportArgs("/marshal import TASK-one " + tc.input)
		if !reflect.DeepEqual(got, []string{"TASK-one", tc.want}) {
			t.Errorf("input %q: got %#v, want %q", tc.input, got, tc.want)
		}
	}
	for _, quote := range []string{"'", `"`} {
		args := marshalImportArgs("/marshal import TASK-one " + quote + "test hi = $(printf hi)" + quote)
		if out, err := exec.Command("/bin/sh", "-c", args[1]).CombinedOutput(); err != nil {
			t.Fatalf("quoted shell check failed: %s %v", out, err)
		}
	}
}
