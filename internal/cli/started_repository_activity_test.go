package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tmux"
)

// The "Started a new Git repository …" line is printed and at once covered by
// the TUI, so the user could not read it until after quitting. The launcher
// hands the same sentence to the in-tmux control centre, whose first Activity
// line repeats it; the terminal still prints it.
func TestStartedRepositoryLineReachesTheControlCentre(t *testing.T) {
	home := fakeHome(t, true)
	dir := filepath.Join(home, "Desktop", "testM")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX_LAUNCH", "1")
	calls := filepath.Join(t.TempDir(), "calls")
	fake := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >>\""+calls+"\"\nprintf '%s\\n' ---- >>\""+calls+"\"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	_, stderr, _ := runCLI(t, dir)
	assertStartedRepository(t, dir, stderr)
	var line string
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, "Started a new Git repository") {
			line = l
		}
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	want := "--marshal-internal-started-repository=" + line
	if line == "" || !strings.Contains("\n"+string(data), "\n"+want+"\n") {
		t.Fatalf("the control centre was not given the started line %q:\n%s", line, data)
	}
}

// The child reads the line ahead of native provider arguments, never as a
// session name, and a session relaunch does not repeat it.
func TestStartedRepositoryFlagIsReadAndNotRelaunched(t *testing.T) {
	line := "Started a new Git repository for this project in ~/Desktop/x (first commit: empty)."
	argv := tuiChildArgv("marshal", line, []string{"--codex", "resume", "--last"})
	if argv[2] != telemetryChildFlag {
		t.Fatalf("child flag moved: %q", argv)
	}
	options := parseWorkspaceArgs(argv[3:])
	if options.startedRepository != line || options.sessionID != "default-session" || !reflect.DeepEqual(options.nativeCodex, []string{"resume", "--last"}) {
		t.Fatalf("options %+v", options)
	}
	if got := tuiChildArgv("marshal", "", []string{"S1"}); !reflect.DeepEqual(got, []string{"marshal", "tui", telemetryChildFlag, "S1"}) {
		t.Fatalf("no started line changed the command: %q", got)
	}
	for _, a := range relaunchArgs("marshal", argv[3:], "SESSION-2", true) {
		if strings.HasPrefix(a, startedRepositoryFlag) {
			t.Fatalf("relaunch repeats the started line: %q", a)
		}
	}
}
