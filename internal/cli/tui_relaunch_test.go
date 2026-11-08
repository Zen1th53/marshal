package cli

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tui"
)

func TestRelaunchArgsKeepDisplayOptionsOnly(t *testing.T) {
	for _, c := range []struct {
		args   []string
		inside bool
		want   []string
	}{
		{nil, true, []string{"/bin/m", "tui", telemetryChildFlag, "S-2"}},
		{[]string{"S-1", "--no-animation", "--theme", "dark"}, false, []string{"/bin/m", "tui", "--no-animation", "--theme", "dark", "S-2"}},
		{[]string{"--theme=light", "--no-color", "S-1"}, true, []string{"/bin/m", "tui", telemetryChildFlag, "--theme=light", "--no-color", "S-2"}},
		// A native launch's arguments belonged to the session left.
		{[]string{"--codex", "--no-color", "exec", "x"}, false, []string{"/bin/m", "tui", "S-2"}},
	} {
		if got := relaunchArgs("/bin/m", c.args, "S-2", c.inside); !slices.Equal(got, c.want) {
			t.Errorf("relaunchArgs(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestRelaunchSessionExecsOnlyForARelaunch(t *testing.T) {
	old := relaunchExec
	defer func() { relaunchExec = old }()
	var argv0 string
	var argv []string
	relaunchExec = func(path string, args []string, env []string) error {
		argv0, argv = path, args
		return errors.New("exec refused in test")
	}
	stopped := 0
	c := &command{stopTelemetry: func() { stopped++ }}
	plain := errors.New("some failure")
	if err := c.relaunchSession(plain, nil); err != plain || argv != nil || stopped != 0 {
		t.Fatalf("a plain error was handled as a relaunch: %v %q", err, argv)
	}
	if err := c.relaunchSession(nil, nil); err != nil || argv != nil {
		t.Fatal("a clean exit relaunched")
	}
	err := c.relaunchSession(&tui.SessionRelaunch{SessionID: "S-2"}, []string{"S-1", "--no-color"})
	exe, _ := os.Executable()
	if argv0 != exe || argv[len(argv)-1] != "S-2" || !slices.Contains(argv, "--no-color") || slices.Contains(argv, "S-1") {
		t.Fatalf("exec %s %q", argv0, argv)
	}
	if stopped != 1 {
		t.Fatalf("telemetry stopped %d times before the exec, want 1", stopped)
	}
	// A failed exec says how to open the session by hand.
	if err == nil || !strings.Contains(err.Error(), "run `marshal tui S-2`") {
		t.Fatalf("failed exec: %v", err)
	}
}
