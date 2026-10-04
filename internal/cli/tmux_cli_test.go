package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestTmuxMissingRefusal(t *testing.T) {
	// Simulate tmux missing
	tmux.SetBinaryMissing(true)
	defer tmux.ResetBinaryPath()

	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer

	// 1. Initialize project so runtime is valid
	if code := Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("init failed: %s", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	// 2. Running tui without tmux must refuse with install hint
	code := Execute(context.Background(), repo.Path(), []string{"tui"}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit code when tmux is missing, got 0")
	}
	errStr := stderr.String()
	if !strings.Contains(errStr, "tmux is required") {
		t.Fatalf("expected 'tmux is required' in stderr, got:\n%s", errStr)
	}
	hint := tmux.DetectInstallCommand()
	if !strings.Contains(errStr, hint) {
		t.Fatalf("expected install hint %q in stderr, got:\n%s", hint, errStr)
	}

	// 3. Plain CLI subcommand `marshal status` does not require tmux!
	stdout.Reset()
	stderr.Reset()
	_ = Execute(context.Background(), repo.Path(), []string{"status"}, strings.NewReader(""), &stdout, &stderr)
	if strings.Contains(stderr.String(), "tmux is required") {
		t.Fatalf("marshal status refused on tmux when it should not require it: %s", stderr.String())
	}

	// 4. Plain CLI subcommand `marshal doctor` works without tmux!
	stdout.Reset()
	stderr.Reset()
	doctorCode := Execute(context.Background(), repo.Path(), []string{"doctor"}, strings.NewReader(""), &stdout, &stderr)
	if doctorCode != 0 {
		t.Fatalf("expected marshal doctor to work without tmux (code 0), got code=%d: %s", doctorCode, stderr.String())
	}
}

func TestStartOutsideTmuxCreatesAndAttachesSession(t *testing.T) {
	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "tmux_argv.log")
	fakeTmux := filepath.Join(tempDir, "tmux")

	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
case "$1" in
  has-session)
    exit 1 # Session does not exist yet
    ;;
  *)
    exit 0
    ;;
esac
`, logFile)

	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	tmux.SetBinaryPath(fakeTmux)
	defer tmux.ResetBinaryPath()

	t.Setenv("TMUX", "")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX_LAUNCH", "1")

	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer

	// Initialize project
	if code := Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("init failed: %s", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	// Run tui outside tmux
	code := Execute(context.Background(), repo.Path(), []string{"tui"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("tui failed: code=%d stderr=%s", code, stderr.String())
	}

	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logData)

	expectedSession := tmux.SessionName(repo.Path())

	if !strings.Contains(logStr, "has-session -t "+expectedSession) {
		t.Fatalf("missing has-session call for %s:\n%s", expectedSession, logStr)
	}
	if !strings.Contains(logStr, "new-session -d -s "+expectedSession) {
		t.Fatalf("missing new-session call for %s:\n%s", expectedSession, logStr)
	}
	if !strings.Contains(logStr, "attach-session -t "+expectedSession) {
		t.Fatalf("missing attach-session call for %s:\n%s", expectedSession, logStr)
	}
}

func TestStartInsideTmuxUsesCurrentSessionWithoutNesting(t *testing.T) {
	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "tmux_argv.log")
	fakeTmux := filepath.Join(tempDir, "tmux")

	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
case "$1" in
  display-message)
    printf 'existing-session main-win @1\n'
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`, logFile)

	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	tmux.SetBinaryPath(fakeTmux)
	defer tmux.ResetBinaryPath()

	t.Setenv("TMUX", "/tmp/tmux-1000/default,12345,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX_LAUNCH", "1")

	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer

	// Initialize project
	if code := Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("init failed: %s", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	// Run tui inside tmux with /quit
	in := strings.NewReader("/quit\n")
	code := Execute(context.Background(), repo.Path(), []string{"tui"}, in, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("tui inside tmux failed: code=%d stderr=%s", code, stderr.String())
	}

	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logData)

	if strings.Contains(logStr, "new-session") {
		t.Fatalf("nested new-session was called inside tmux:\n%s", logStr)
	}
	if strings.Contains(logStr, "attach-session") {
		t.Fatalf("nested attach-session was called inside tmux:\n%s", logStr)
	}
}

func TestTwoProjectsDoNotCollide(t *testing.T) {
	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "tmux_argv.log")
	fakeTmux := filepath.Join(tempDir, "tmux")

	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
case "$1" in
  has-session)
    exit 1
    ;;
  *)
    exit 0
    ;;
esac
`, logFile)

	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	tmux.SetBinaryPath(fakeTmux)
	defer tmux.ResetBinaryPath()

	t.Setenv("TMUX", "")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX_LAUNCH", "1")

	repo1 := cliRepo(t)
	repo2 := cliRepo(t)

	sess1 := tmux.SessionName(repo1.Path())
	sess2 := tmux.SessionName(repo2.Path())

	if sess1 == sess2 {
		t.Fatalf("session names collided: %s == %s", sess1, sess2)
	}

	var stdout, stderr bytes.Buffer
	_ = Execute(context.Background(), repo1.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr)
	stdout.Reset()
	stderr.Reset()
	_ = Execute(context.Background(), repo2.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr)
	stdout.Reset()
	stderr.Reset()

	code1 := Execute(context.Background(), repo1.Path(), []string{"tui"}, strings.NewReader(""), &stdout, &stderr)
	if code1 != 0 {
		t.Fatalf("repo1 tui failed: code=%d stderr=%s", code1, stderr.String())
	}

	code2 := Execute(context.Background(), repo2.Path(), []string{"tui"}, strings.NewReader(""), &stdout, &stderr)
	if code2 != 0 {
		t.Fatalf("repo2 tui failed: code=%d stderr=%s", code2, stderr.String())
	}

	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logData)

	if !strings.Contains(logStr, "new-session -d -s "+sess1) {
		t.Fatalf("expected new-session for %s:\n%s", sess1, logStr)
	}
	if !strings.Contains(logStr, "new-session -d -s "+sess2) {
		t.Fatalf("expected new-session for %s:\n%s", sess2, logStr)
	}
}

func TestSessionNameFromSubdirectoryMatchesCanonicalRoot(t *testing.T) {
	repo := cliRepo(t)
	subDir := filepath.Join(repo.Path(), "pkg", "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	c := &command{root: subDir}
	ctx := context.Background()
	canonicalRoot := c.canonicalRoot(ctx)
	if canonicalRoot != repo.Path() {
		t.Fatalf("expected canonicalRoot %q, got %q", repo.Path(), canonicalRoot)
	}

	rootSess := tmux.SessionName(repo.Path())
	subSess := tmux.SessionName(canonicalRoot)
	if rootSess != subSess {
		t.Fatalf("session mismatch: root=%s sub=%s", rootSess, subSess)
	}
}

func TestOutsideTmuxRepairsDeadMarshalWindow(t *testing.T) {
	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "tmux_argv.log")
	fakeTmux := filepath.Join(tempDir, "tmux")

	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
case "$1" in
  has-session)
    exit 0 # Session already exists
    ;;
  list-windows)
    printf 'marshal-dead\n'
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`, logFile)

	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	tmux.SetBinaryPath(fakeTmux)
	defer tmux.ResetBinaryPath()

	t.Setenv("TMUX", "")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX_LAUNCH", "1")

	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer
	_ = Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr)
	stdout.Reset()
	stderr.Reset()

	code := Execute(context.Background(), repo.Path(), []string{"tui"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("tui failed: code=%d stderr=%s", code, stderr.String())
	}

	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logData)

	sess := tmux.SessionName(repo.Path())
	// Should create a window for marshal and attach
	if !strings.Contains(logStr, "new-window -t "+sess+" -n marshal") {
		t.Fatalf("expected new-window for dead marshal:\n%s", logStr)
	}
	if !strings.Contains(logStr, "attach-session -t "+sess) {
		t.Fatalf("expected attach-session:\n%s", logStr)
	}
}
