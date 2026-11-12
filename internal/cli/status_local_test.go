package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
		want := []string{"open for this project in another window", "Marshal: running · 2 tasks, 1 done"}
		if args[0] == "status" {
			// status says it in the Board's words.
			want = []string{"open for this project in another window", "Run: running · 1 of 2 tasks done"}
		}
		for _, want := range want {
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
//
// It says it plainly, in the Board's words: the project, the run, the
// Marshal provider and whether anonymous telemetry is on; --json keeps the
// counts' shape.
func TestStatusWithoutDaemonOrWindowReadsTheStore(t *testing.T) {
	root := initProject(t)
	t.Setenv("MARSHAL_CLOUD_ENDPOINT", "off")
	stdout, stderr, code := runCLI(t, root, "status")
	if code != 0 || strings.Contains(stdout+stderr, "daemon") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	want := "Project: " + filepath.Base(root) + " (branch "
	for _, want := range []string{want, "Run: none yet. Run marshal here and tell the Marshal what to build.", "Marshal provider: not chosen yet", "anonymous telemetry: off"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout %q lacks %q", stdout, want)
		}
	}
	for _, jargon := range []string{"schema=", "honeypot", "agents="} {
		if strings.Contains(stdout, jargon) {
			t.Fatalf("stdout %q shows %q", stdout, jargon)
		}
	}
	if err := os.WriteFile(statepath.Path(root, "default-provider"), []byte("claude\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if stdout, _, _ := runCLI(t, root, "status"); !strings.Contains(stdout, "Marshal provider: Claude") {
		t.Fatalf("provider: %q", stdout)
	}
	stdout, _, code = runCLI(t, root, "status", "--json")
	var value map[string]any
	if code != 0 || json.Unmarshal([]byte(stdout), &value) != nil {
		t.Fatalf("json: %d %q", code, stdout)
	}
	for _, key := range []string{"project", "schema_version", "agent_count", "session_count", "task_count", "lease_count", "honeypot"} {
		if _, ok := value[key]; !ok {
			t.Fatalf("--json lost %q: %s", key, stdout)
		}
	}
}

// Anonymous telemetry's line follows the saved choice.
func TestTelemetryLineFollowsTheChoice(t *testing.T) {
	root := initProject(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MARSHAL_CLOUD_ENDPOINT", "https://telemetry.invalid")
	if got := app.TelemetryLine(root); got != "anonymous telemetry: on (marshal telemetry disable turns it off)" {
		t.Fatalf("default: %q", got)
	}
	t.Setenv("MARSHAL_CLOUD_ENDPOINT", "off")
	if _, stderr, code := runCLI(t, root, "telemetry", "disable"); code != 0 {
		t.Fatal(stderr)
	}
	t.Setenv("MARSHAL_CLOUD_ENDPOINT", "https://telemetry.invalid")
	if got := app.TelemetryLine(root); got != "anonymous telemetry: off" {
		t.Fatalf("after disable: %q", got)
	}
}
