//go:build linux

package tui

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestPTYOpenBySessionIDRelaunchesInPlace drives CTUI-0219 on the real
// binary: the form, the confirmation, and then the same process, on the same
// terminal, running `marshal tui` in the other session. Nothing else is
// pressed after Proceed: the workspace leaves at once.
func TestPTYOpenBySessionIDRelaunchesInPlace(t *testing.T) {
	if !editionExtreme {
		// Community has not released Ctrl+N navigation (navigationRefusal).
		t.Skip("the advanced menu is released in the Marshal build only")
	}
	bin := buildMarshalBinary(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	t.Setenv("MARSHAL_CLOUD_ENDPOINT", "off")
	project := initProject(t, bin)
	s := startFrozenTUIInProject(t, 40, 200, bin, project, "tui", "S-first")
	s.mustSee("Marshal")
	pid := s.cmd.Process.Pid
	cmdline := func() string {
		data, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
		return strings.ReplaceAll(string(data), "\x00", " ")
	}
	if !strings.Contains(cmdline(), "tui S-first") {
		t.Fatalf("started as %q", cmdline())
	}
	s.send("\x0e")
	recoveryMenuPTYAction(s, "Open by session ID")
	s.mustSee("Session ID to open")
	s.send("S-second\r")
	s.mustSee("open session S-second")
	s.mustSee("Proceed")
	s.send("\x1b[C\r")
	deadline := time.Now().Add(ptyWait(10 * time.Second))
	for !strings.Contains(cmdline(), "tui S-second") {
		if time.Now().After(deadline) {
			t.Fatalf("the process did not relaunch: %q\n%s", cmdline(), tail(s.output(), 2000))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if strings.Contains(cmdline(), "S-first") {
		t.Fatalf("the relaunch kept the old session: %q", cmdline())
	}
	s.mustSee("Leaving session S-first for session S-second")
	// The relaunched workspace is the one on the terminal now: /quit ends it.
	before := strings.Count(s.output(), "Marshal")
	deadline = time.Now().Add(ptyWait(10 * time.Second))
	for strings.Count(s.output(), "Marshal") <= before && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	s.waitForSettle(2 * time.Second)
	s.sendLine("/quit")
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("relaunched workspace exited with %v\n%s", err, tail(s.output(), 2000))
		}
	case <-time.After(ptyWait(15 * time.Second)):
		t.Fatalf("relaunched workspace did not quit\n%s", tail(s.output(), 2000))
	}
}
