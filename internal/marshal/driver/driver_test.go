package driver

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// claudeStream follows the stream-json shape Claude Code writes, as recorded
// in internal/tui/native_claude_test.go and internal/memory/importer tests.
// The Edit names a file the worker never actually changes.
const claudeStream = `{"type":"system","subtype":"init","session_id":"s-1","model":"claude-opus-4-1-20250805"}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t-1","name":"Edit","input":{"file_path":"claimed.txt","old_string":"a","new_string":"b"}}]}}
{"type":"result","subtype":"success","result":"all tests pass"}
`

// fakeCLI stands in for a worker CLI. It refuses to run on a terminal, may
// write a file, may sleep, then prints a recorded stream.
const fakeCLI = `#!/bin/sh
if [ -t 0 ]; then echo "stdin is a terminal" >&2; exit 3; fi
if head -c 1 | od -An -c | grep -q .; then echo "stdin was not at EOF" >&2; exit 4; fi
if [ -n "$FAKE_WRITE" ]; then printf 'changed\n' > "$FAKE_WRITE"; fi
if [ -n "$FAKE_LATE" ]; then (printf 'started\n' > "$FAKE_LATE.started"; sleep 1; printf 'late\n' > "$FAKE_LATE") & fi
if [ -n "$FAKE_SLEEP" ]; then sleep "$FAKE_SLEEP"; fi
if [ -n "$FAKE_STREAM" ]; then cat "$FAKE_STREAM"; fi
exit 0
`

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newTask makes a repository with one commit and a task based on it. The
// repository doubles as the task worktree.
func newTask(t *testing.T) (marshal.Task, string) {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init", "-q")
	run(t, dir, "git", "config", "user.name", "Test Author")
	run(t, dir, "git", "config", "user.email", "author@example.invalid")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "add", "README")
	run(t, dir, "git", "commit", "-q", "-m", "base")
	base := run(t, dir, "git", "rev-parse", "HEAD")
	return marshal.Task{PlanTaskID: "T1", Worker: "worker-1", BaseCommit: base, Files: []string{"real.txt"}}, dir
}

func fakeBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-cli")
	if err := os.WriteFile(path, []byte(fakeCLI), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func streamFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stream.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func handIn(t *testing.T, d Driver, req Request) marshal.HandIn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	h, err := d.Launch(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Wait(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Criterion 1: every driver runs with no terminal on stdin, and stdin is
// already at EOF: the fake exits non-zero on a terminal or on any input.
func TestM05DriversRunWithoutTTY(t *testing.T) {
	bin := fakeBinary(t)
	for _, d := range []Native{Codex(bin), Claude(bin), Agy(bin), OpenCode(bin)} {
		t.Run(d.Provider, func(t *testing.T) {
			task, wt := newTask(t)
			t.Setenv("FAKE_WRITE", filepath.Join(wt, "real.txt"))
			out := handIn(t, d, Request{Task: task, Worktree: wt, Brief: "do the task"})
			if got := out.RuntimeObserved[0].ExitCode; got != 0 {
				t.Fatalf("worker exit %d, output %q", got, out.RuntimeObserved[0].Output)
			}
			if out.Mode != marshal.Native || out.Provider != d.Provider {
				t.Fatalf("identity %s/%s", out.Mode, out.Provider)
			}
		})
	}
}

// Criterion 2: the diff and the files come from git, not from the worker.
func TestM05HandInFromGitNotWorkerText(t *testing.T) {
	task, wt := newTask(t)
	t.Setenv("FAKE_WRITE", filepath.Join(wt, "real.txt"))
	t.Setenv("FAKE_STREAM", streamFile(t, claudeStream))
	out := handIn(t, Claude(fakeBinary(t)), Request{Task: task, Worktree: wt, Brief: "edit"})
	if len(out.FilesTouched) != 1 || out.FilesTouched[0] != "real.txt" {
		t.Fatalf("files touched = %v, want [real.txt]", out.FilesTouched)
	}
	if !strings.Contains(out.Diff, "real.txt") || strings.Contains(out.Diff, "claimed.txt") {
		t.Fatalf("diff does not reflect git:\n%s", out.Diff)
	}
	if out.ResultCommit == task.BaseCommit {
		t.Fatal("the worker's change was not recorded as a result commit")
	}
	if author := run(t, wt, "git", "log", "-1", "--format=%an <%ae>"); author != "Test Author <author@example.invalid>" {
		t.Fatalf("result commit author = %q, want the repository identity", author)
	}
}

func TestM05FakeCLIRejectsNewlineStdin(t *testing.T) {
	cmd := exec.Command(fakeBinary(t))
	cmd.Stdin = strings.NewReader("\n")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 4 {
		t.Fatalf("exit = %v, output = %q; want exit 4", err, out)
	}
}

func TestM05GitHooksDisabledDuringAssembly(t *testing.T) {
	task, wt := newTask(t)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hooks := t.TempDir()
	for _, name := range []string{"prepare-commit-msg", "post-commit"} {
		path := filepath.Join(hooks, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf ran > '"+marker+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	run(t, wt, "git", "config", "core.hooksPath", hooks)
	t.Setenv("FAKE_WRITE", filepath.Join(wt, "real.txt"))
	_ = handIn(t, Codex(fakeBinary(t)), Request{Task: task, Worktree: wt, Brief: "edit"})
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("git hook ran during assembly: %v", err)
	}
}

func TestM05ExternalDiffAndTextconvDisabled(t *testing.T) {
	task, wt := newTask(t)
	marker := filepath.Join(t.TempDir(), "diff-ran")
	script := filepath.Join(t.TempDir(), "diff-driver")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf ran > '"+marker+"'\nprintf 'worker diff output\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, wt, "git", "config", "diff.external", script)
	run(t, wt, "git", "config", "diff.worker.textconv", script)
	if err := os.WriteFile(filepath.Join(wt, ".gitattributes"), []byte("real.txt diff=worker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_WRITE", filepath.Join(wt, "real.txt"))
	out := handIn(t, Codex(fakeBinary(t)), Request{Task: task, Worktree: wt, Brief: "edit"})
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("external diff or textconv ran: %v", err)
	}
	if !strings.Contains(out.Diff, "+changed") || strings.Contains(out.Diff, "worker diff output") {
		t.Fatalf("hand-in diff is not git's internal diff: %q", out.Diff)
	}
}

// Criterion 3: checks are re-run; a worker claiming success with a failing
// check yields a valid hand-in whose check result is FAIL.
func TestM05ChecksRerunDespiteClaimedSuccess(t *testing.T) {
	task, wt := newTask(t)
	task.Criteria = []string{"ok file exists"}
	task.Checks = []marshal.Check{{Command: "test -f ok.txt", Criteria: []string{"ok file exists"}}}
	t.Setenv("FAKE_WRITE", filepath.Join(wt, "real.txt"))
	t.Setenv("FAKE_STREAM", streamFile(t, claudeStream))
	out := handIn(t, Claude(fakeBinary(t)), Request{Task: task, Worktree: wt, Brief: "edit"})
	if len(out.CheckResults) != 1 || out.CheckResults[0].Passed {
		t.Fatalf("check results = %+v, want one failing result", out.CheckResults)
	}
	if out.CheckResults[0].ResultCommit != out.ResultCommit {
		t.Fatal("check result is not bound to the result commit")
	}
	if err := marshal.ValidateHandIn(task, out); err != nil {
		t.Fatalf("a hand-in with a failing check must still be valid: %v", err)
	}
	if met, total, failing := marshal.CriteriaMet(task, out); met != 0 || total != 1 || len(failing) != 1 {
		t.Fatalf("criteria met %d/%d failing %v", met, total, failing)
	}
}

// Criterion 4: worker-reported actions are kept apart from what the runtime
// observed and never become acceptance evidence.
func TestM05WorkerReportedKeptApart(t *testing.T) {
	task, wt := newTask(t)
	task.Checks = []marshal.Check{{Command: "true", Criteria: []string{"c"}}}
	t.Setenv("FAKE_STREAM", streamFile(t, string(fixture(t, "codex-exec.jsonl"))))
	bin := fakeBinary(t)
	out := handIn(t, Codex(bin), Request{Task: task, Worktree: wt, Brief: "run"})
	if len(out.WorkerReported) != 1 || out.WorkerReported[0].Command != "/bin/zsh -lc 'echo ready'" || out.WorkerReported[0].ExitCode != 0 {
		t.Fatalf("worker reported = %+v", out.WorkerReported)
	}
	if !strings.HasPrefix(out.RuntimeObserved[0].Command, bin+" exec --json") {
		t.Fatalf("runtime record is not the launched command: %q", out.RuntimeObserved[0].Command)
	}
	for _, c := range out.CheckResults {
		if c.Command == out.WorkerReported[0].Command {
			t.Fatal("a worker-reported command was counted as a check")
		}
	}
	if got := parseClaude([]byte(claudeStream)); len(got) != 1 || !strings.HasPrefix(got[0].Command, "Edit ") || got[0].ExitCode != -1 {
		t.Fatalf("claude reported = %+v", got)
	}
	if got := parseAgy(fixture(t, "agy-print.jsonl")); len(got) != 0 {
		t.Fatalf("agy text-only run reported actions: %+v", got)
	}
}

// Criterion 5: cancel stops the worker and its children and leaves the
// worktree intact. The fake starts a child that writes a second file after
// a second; if cancellation stopped only the parent, that write would land.
func TestM05CancelStopsWorkerKeepsWorktree(t *testing.T) {
	task, wt := newTask(t)
	written := filepath.Join(wt, "real.txt")
	late := filepath.Join(wt, "late.txt")
	t.Setenv("FAKE_WRITE", written)
	t.Setenv("FAKE_LATE", late)
	t.Setenv("FAKE_SLEEP", "60")
	d := Codex(fakeBinary(t))
	h, err := d.Launch(context.Background(), Request{Task: task, Worktree: wt, Brief: "slow"})
	if err != nil {
		t.Fatal(err)
	}
	// Cancel only once the child has signalled that it is running, so the
	// test proves the child is stopped rather than never started.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(late + ".started"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker child never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	start := time.Now()
	if err := d.Cancel(h); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancel took %s; the worker was not stopped", elapsed)
	}
	if data, err := os.ReadFile(written); err != nil || string(data) != "changed\n" {
		t.Fatalf("the worktree was not left intact: %q %v", data, err)
	}
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(late); !os.IsNotExist(err) {
		t.Fatalf("a child of the cancelled worker kept running and wrote %s", late)
	}
}

// Checks run in isolated checkouts of the result: a check that changes files
// cannot change what the next check sees, and the task worktree stays clean.
func TestM05ChecksAreIsolatedFromEachOther(t *testing.T) {
	task, wt := newTask(t)
	task.Checks = []marshal.Check{
		{Command: "printf tampered > README", Criteria: []string{"a"}},
		{Command: "grep -qx base README", Criteria: []string{"b"}},
	}
	out := handIn(t, Codex(fakeBinary(t)), Request{Task: task, Worktree: wt, Brief: "nothing"})
	if len(out.CheckResults) != 2 || !out.CheckResults[1].Passed {
		t.Fatalf("second check saw the first check's change: %+v", out.CheckResults)
	}
	if status := run(t, wt, "git", "status", "--porcelain"); status != "" {
		t.Fatalf("a check changed the task worktree: %q", status)
	}
	if list := run(t, wt, "git", "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Fatalf("check checkouts were not removed:\n%s", list)
	}
}

// A diff past the git output limit fails the hand-in instead of shortening
// its evidence.
func TestM05OversizedGitOutputFails(t *testing.T) {
	_, wt := newTask(t)
	big := strings.Repeat("x", maxGitOutput+1)
	if err := os.WriteFile(filepath.Join(wt, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := git(context.Background(), wt, "show", "HEAD:README"); err != nil || out != "base\n" {
		t.Fatalf("small output = %q, %v", out, err)
	}
	blob := run(t, wt, "git", "hash-object", "-w", "big.txt")
	if _, err := git(context.Background(), wt, "cat-file", "-p", blob); !errors.Is(err, ErrHandInTooLarge) {
		t.Fatalf("err = %v, want ErrHandInTooLarge", err)
	}
}

// Output is bounded while the worker runs, not after it exits.
func TestM05OutputBoundedWhileProduced(t *testing.T) {
	b := &capBuffer{limit: 8}
	for i := 0; i < 1000; i++ {
		if n, err := b.Write([]byte("0123456789")); n != 10 || err != nil {
			t.Fatalf("write = %d, %v", n, err)
		}
	}
	if len(b.Bytes()) != 8 || !strings.Contains(b.String(), "9992 bytes dropped") {
		t.Fatalf("kept %d bytes: %q", len(b.Bytes()), b.String())
	}
}

// Criterion 6: nothing in this package can reach Claude model discovery,
// which opens a billed session. The discovery code lives in the Claude
// adapter; this package must not import it, and the Claude command line is
// the task brief, never the discovery prompt.
func TestM05NoClaudeDiscoveryPath(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if strings.Contains(imp.Path.Value, "internal/adapter") {
				t.Fatalf("%s imports %s", name, imp.Path.Value)
			}
		}
	}
	args := Claude("").Args(Request{Brief: "the task brief", Model: "opus"})
	joined := strings.Join(args, " ")
	if !strings.HasSuffix(joined, "the task brief") || strings.Contains(joined, "respond with the single word") {
		t.Fatalf("claude args = %q", joined)
	}
}

// The runtime never invents an author for the result commit.
func TestM05MissingIdentityFailsInsteadOfInventing(t *testing.T) {
	task, wt := newTask(t)
	run(t, wt, "git", "config", "--unset", "user.name")
	run(t, wt, "git", "config", "--unset", "user.email")
	empty := filepath.Join(t.TempDir(), "empty-gitconfig")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_SYSTEM", empty)
	t.Setenv("FAKE_WRITE", filepath.Join(wt, "real.txt"))
	d := Codex(fakeBinary(t))
	h, err := d.Launch(context.Background(), Request{Task: task, Worktree: wt, Brief: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Wait(context.Background(), h); err == nil || !strings.Contains(err.Error(), "user.name") {
		t.Fatalf("err = %v, want a missing-identity error", err)
	}
}

// A governed run is assembled the same way and marked governed.
func TestM05GovernedHandIn(t *testing.T) {
	task, wt := newTask(t)
	g := Governed{Provider: "codex", Run: func(_ context.Context, req Request) ([]marshal.CommandRecord, error) {
		err := os.WriteFile(filepath.Join(req.Worktree, "real.txt"), []byte("governed\n"), 0o644)
		return []marshal.CommandRecord{{Command: "write real.txt"}}, err
	}}
	out := handIn(t, g, Request{Task: task, Worktree: wt, Brief: "edit"})
	if out.Mode != marshal.Governed || len(out.FilesTouched) != 1 || len(out.WorkerReported) != 1 {
		t.Fatalf("governed hand-in = %+v", out)
	}
}

func TestGovernedRunnerFailureDoesNotCreateHandIn(t *testing.T) {
	task, wt := newTask(t)
	g := Governed{Provider: "codex", Run: func(context.Context, Request) ([]marshal.CommandRecord, error) {
		return nil, errors.New("Process 05 requires approval")
	}}
	h, err := g.Launch(t.Context(), Request{Task: task, Worktree: wt, Brief: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Wait(t.Context(), h); err == nil || !strings.Contains(err.Error(), "requires approval") {
		t.Fatalf("governed failure became a hand-in: %v", err)
	}
}

func TestM05InvalidRequestRejected(t *testing.T) {
	_, err := Codex("").Launch(context.Background(), Request{Brief: "x"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}
