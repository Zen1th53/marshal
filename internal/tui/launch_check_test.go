package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/doctor"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// newLaunchRepo makes a committed Git repository in a temp dir and returns its
// symlink-resolved path, so project.Discover (which resolves symlinks) and the
// test agree on the runtime directory.
func newLaunchRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.invalid")
	git("config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "init")
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// readyLaunch marks the per-start project check as already passed, the state in
// which InitTmux starts the Marshal chat. Tests that exercise the chat auto-start
// without driving the real doctor establish that precondition with this.
func readyLaunch(w *Workspace) {
	w.launch.mu.Lock()
	w.launch.phase = launchReady
	w.launch.mu.Unlock()
}

func passReport() doctor.Report {
	return doctor.Report{Verdict: doctor.Pass, Results: []doctor.Result{{Name: "git", Verdict: doctor.Pass}}}
}

func failReport(name string) doctor.Report {
	return doctor.Report{Verdict: doctor.Fail, Results: []doctor.Result{{Name: name, Verdict: doctor.Fail, Detail: name + " broke"}}}
}

func TestLaunchCheckRunsDoctorAtEveryStart(t *testing.T) {
	root := newLaunchRepo(t)
	calls := 0
	orig := launchDoctor
	launchDoctor = func(context.Context, string, doctor.Options) doctor.Report {
		calls++
		return passReport()
	}
	defer func() { launchDoctor = orig }()

	w := &Workspace{workDir: root}
	w.runLaunchCheck(t.Context())

	if calls != 1 {
		t.Fatalf("doctor called %d times, want 1", calls)
	}
	if w.launchBlocked() {
		t.Fatalf("phase is blocked, want ready")
	}
	w.launch.mu.Lock()
	phase := w.launch.phase
	w.launch.mu.Unlock()
	if phase != launchReady {
		t.Fatalf("phase %d, want ready", phase)
	}
}

func TestLaunchCheckBlocksChatOnFail(t *testing.T) {
	root := newLaunchRepo(t)
	orig := launchDoctor
	launchDoctor = func(context.Context, string, doctor.Options) doctor.Report {
		return failReport("worktree")
	}
	defer func() { launchDoctor = orig }()

	w := &Workspace{workDir: root}
	called := false
	w.afterLaunchCheck(func() { called = true })
	w.runLaunchCheck(t.Context())

	if called {
		t.Fatalf("chat-start callback ran while blocked")
	}
	if !w.launchBlocked() {
		t.Fatalf("phase is not blocked")
	}
	blockers := w.launchBlockers()
	want := "I can't fix this safely: Git worktrees don't work in this repository. Run git worktree prune, then press r."
	if len(blockers) != 1 || blockers[0] != want {
		t.Fatalf("blockers = %#v, want [%q]", blockers, want)
	}
}

func TestLaunchRetryStartsChatOnce(t *testing.T) {
	root := newLaunchRepo(t)
	report := failReport("worktree")
	orig := launchDoctor
	launchDoctor = func(context.Context, string, doctor.Options) doctor.Report { return report }
	defer func() { launchDoctor = orig }()

	w := &Workspace{workDir: root}
	count := 0
	w.afterLaunchCheck(func() { count++ })

	w.runLaunchCheck(t.Context())
	if count != 0 || !w.launchBlocked() {
		t.Fatalf("after first check: count=%d blocked=%v, want 0 and blocked", count, w.launchBlocked())
	}

	// The owner fixed it; the retry passes.
	report = passReport()
	w.runLaunchCheck(t.Context())
	if w.launchBlocked() {
		t.Fatalf("still blocked after retry")
	}
	if count != 1 {
		t.Fatalf("chat-start callback ran %d times, want exactly 1", count)
	}
}

func TestLaunchSafeFixPermissions(t *testing.T) {
	root := newLaunchRepo(t)
	marshalDir := filepath.Join(root, ".marshal")
	if err := os.MkdirAll(marshalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(marshalDir, 0o755); err != nil {
		t.Fatal(err)
	}

	calls := 0
	orig := launchDoctor
	launchDoctor = func(_ context.Context, r string, _ doctor.Options) doctor.Report {
		calls++
		verdict := doctor.Pass
		if info, err := os.Stat(filepath.Join(r, ".marshal")); err != nil || info.Mode().Perm() != 0o700 {
			verdict = doctor.Fail
		}
		return doctor.Report{Verdict: verdict, Results: []doctor.Result{{Name: "permissions", Verdict: verdict, Detail: "runtime directory must have mode 0700"}}}
	}
	defer func() { launchDoctor = orig }()

	w := &Workspace{workDir: root}
	w.runLaunchCheck(t.Context())

	if calls != 2 {
		t.Fatalf("doctor called %d times, want 2 (once before and once after the fix)", calls)
	}
	info, err := os.Stat(marshalDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf(".marshal mode = %o, want 700", info.Mode().Perm())
	}
	if w.launchBlocked() {
		t.Fatalf("still blocked after the fix passed")
	}
}

func TestLaunchSafeFixRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	lay := project.Layout{RuntimeDir: link, Artifacts: filepath.Join(link, "artifacts")}
	report := doctor.Report{Verdict: doctor.Fail, Results: []doctor.Result{{Name: "permissions", Verdict: doctor.Fail}}}

	if fixed := launchSafeFix(lay, report); len(fixed) != 0 {
		t.Fatalf("launchSafeFix touched a symlink: %#v", fixed)
	}
	if info, err := os.Stat(real); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o755 {
		t.Fatalf("symlink target mode = %o, want 755 (unchanged)", info.Mode().Perm())
	}
	blockers, _ := launchLines(report, lay)
	if len(blockers) != 1 || !strings.Contains(blockers[0], "chmod 700 "+link) {
		t.Fatalf("blockers = %#v, want a chmod 700 %s line", blockers, link)
	}
}

func TestLaunchLinesCoverEveryDoctorFailure(t *testing.T) {
	lay := project.Layout{
		RuntimeDir: "/p/.marshal",
		Artifacts:  "/p/.marshal/artifacts",
		Socket:     "/p/.marshal/runtime.sock",
	}
	// Every name doctor.Check can FAIL with today (every failure(...) call in
	// doctor.go outside finalizeMissing). Re-grep failure(" when a check moves.
	names := []string{"git", "repository", "pack", "runtime_version", "sqlite", "security_subsystems", "permissions", "socket", "worktree", "artifacts", "policy"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			report := doctor.Report{Verdict: doctor.Fail, Results: []doctor.Result{{Name: name, Verdict: doctor.Fail, Detail: "DISTINCT-DETAIL-" + name}}}
			blockers, _ := launchLines(report, lay)
			if len(blockers) != 1 {
				t.Fatalf("%s: %d blockers, want 1: %#v", name, len(blockers), blockers)
			}
			fallback := "I can't fix this safely: DISTINCT-DETAIL-" + name + ". Run marshal doctor, then press r."
			if blockers[0] == fallback {
				t.Fatalf("%s fell through to the fallback line: %q", name, blockers[0])
			}
			if strings.Contains(blockers[0], "DISTINCT-DETAIL-"+name) {
				t.Fatalf("%s reused its raw detail instead of a table line: %q", name, blockers[0])
			}
		})
	}
}

func TestLaunchLinesRepositoryFailureIsOneLine(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	report := doctor.Check(t.Context(), dir, doctor.Options{})
	lay := project.Layout{RuntimeDir: filepath.Join(dir, ".marshal")}
	blockers, _ := launchLines(report, lay)
	want := "I can't fix this safely: this folder is not a Git repository with a first commit. Run git init && git add -A && git commit -m \"first commit\", then press r."
	if len(blockers) != 1 || blockers[0] != want {
		t.Fatalf("blockers = %#v, want exactly [%q]", blockers, want)
	}
}

func TestBlockedScreenHasNoJargon(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, false)
	w := &Workspace{homeBoard: true}
	w.launch.phase = launchBlocked
	// A spread of blockers, including the one case that must keep the literal
	// "marshal doctor --json" command.
	lay := project.Layout{RuntimeDir: "/p/.marshal", Artifacts: "/p/.marshal/artifacts", Socket: "/p/.marshal/runtime.sock"}
	for _, name := range []string{"worktree", "permissions", "sqlite", "pack"} {
		report := doctor.Report{Verdict: doctor.Fail, Results: []doctor.Result{{Name: name, Verdict: doctor.Fail, Detail: name}}}
		lines, _ := launchLines(report, lay)
		w.launch.blockers = append(w.launch.blockers, lines...)
	}

	var hits []boardHit
	v := boardVisual{theme: th, hits: &hits}
	l := layout.ComputeFor(100, 30, layout.Home, v.layoutCounts())
	frame := w.paintBlocked(l, th, v)
	body := StripANSI(strings.Join(frame, "\n"))

	// These words never belong on the Blocked screen, in a <what> or a command.
	for _, jargon := range []string{"PASS", "FAIL", "DEGRADED", "sqlite", "verdict"} {
		if strings.Contains(body, jargon) {
			t.Fatalf("Blocked screen shows jargon %q:\n%s", jargon, body)
		}
	}
	// "doctor" is allowed inside the sqlite case's bug-report command, so the
	// no-jargon rule is asserted on the <what> parts (what each line names as
	// the problem, before ". Run <command>").
	for _, blocker := range w.launchBlockers() {
		what := strings.TrimPrefix(blocker, "I can't fix this safely: ")
		if i := strings.Index(what, ". Run "); i >= 0 {
			what = what[:i]
		}
		for _, jargon := range []string{"PASS", "FAIL", "DEGRADED", "doctor", "sqlite", "verdict"} {
			if strings.Contains(what, jargon) {
				t.Fatalf("Blocked line's <what> shows jargon %q: %q", jargon, what)
			}
		}
	}
	if !strings.Contains(body, "[r] Retry") || !strings.Contains(body, "[q] Quit") {
		t.Fatalf("Blocked screen missing the retry/quit footer:\n%s", body)
	}
}
