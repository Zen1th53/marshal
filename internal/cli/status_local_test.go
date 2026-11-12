package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// holdProject takes the project's lifecycle lock as a Marshal window does,
// until the test ends.
func holdProject(t *testing.T, root string) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(root, statepath.DirName, "runtime-owner.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
}

func initProject(t *testing.T) string {
	t.Helper()
	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	return repo.Path()
}

// TestStatusAndTasksReadTheStoreWhileTheWindowHoldsTheRun: with the Marshal
// window holding the project and no background service, `marshal status`
// and `marshal tasks` read the same store the window does, say the window
// holds the project, show the run's tasks, and never tell the user to start
// a daemon.
func TestStatusAndTasksReadTheStoreWhileTheWindowHoldsTheRun(t *testing.T) {
	root := initProject(t)
	ctx := context.Background()
	runtime, err := app.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	project, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run := marshal.Run{PlanID: "PLAN-1", PlanVersion: 1, State: marshal.Dispatching, Settings: marshal.DefaultSettings(),
		Tasks: []marshal.Task{{PlanTaskID: "t1", Title: "Add add(a, b)", State: marshal.Dispatched}, {PlanTaskID: "t2", Title: "Write its test", State: marshal.Merged}}}
	if _, err := runtime.Store().SetMarshalRun(ctx, project.ID, "RUN-1", run, 0); err != nil {
		t.Fatal(err)
	}
	runtime.Close()
	holdProject(t, root)

	for _, args := range [][]string{{"status"}, {"tasks"}} {
		stdout, stderr, code := runCLI(t, root, args...)
		if code != 0 || strings.Contains(stderr, "daemon") || strings.Contains(stdout, "daemon") {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
		for _, want := range []string{"open for this project in another window", "Marshal: working · 2 tasks, 1 done"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("%v: stdout %q lacks %q", args, stdout, want)
			}
		}
		if strings.Contains(stdout, "RUN-1") || strings.Contains(stdout, "PLAN-1") {
			t.Fatalf("%v shows an internal ID: %q", args, stdout)
		}
	}
	stdout, _, _ := runCLI(t, root, "tasks")
	for _, want := range []string{"Add add(a, b) — in progress", "Write its test — done"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("tasks stdout %q lacks %q", stdout, want)
		}
	}
	// A command that only the background service can answer points at the
	// open window instead of telling the user to start a daemon.
	_, stderr, code := runCLI(t, root, "agents")
	if code == 0 || strings.Contains(stderr, "marshal daemon") || !strings.Contains(stderr, "another window") {
		t.Fatalf("agents: code=%d stderr=%q", code, stderr)
	}
}

// TestStatusWithoutDaemonOrWindowReadsTheStore: with nothing running,
// `marshal status` still shows what the project has saved.
func TestStatusWithoutDaemonOrWindowReadsTheStore(t *testing.T) {
	root := initProject(t)
	stdout, stderr, code := runCLI(t, root, "status")
	if code != 0 || !strings.Contains(stdout, "schema=") || strings.Contains(stdout+stderr, "daemon") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
