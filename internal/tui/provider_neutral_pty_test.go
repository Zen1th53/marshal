//go:build linux

package tui

import (
	"os"
	"strings"
	"testing"
)

// The real binary asks which provider to use while several are installed,
// launches nothing, and after /provider use sends the same command to the
// chosen provider only.
func TestPTYProviderNeutralCommandsFollowTheChosenProvider(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	logPath := sweepAgentCodexDouble(t)
	root := initProject(t, bin)

	s := startFrozenTUIInProject(t, 50, 200, bin, root, "tui")
	_ = os.Remove(logPath)
	run := func(line string) {
		s.send(line)
		s.send("\x1b")
		s.send("\r")
	}

	run("/mcp list")
	s.mustSee("Several AI agents are installed")
	if data, err := os.ReadFile(logPath); !os.IsNotExist(err) {
		t.Fatalf("a provider ran before one was chosen: %q (%v)", data, err)
	}

	run("/provider use claude")
	s.mustSee("Default provider: Claude")
	run("/mcp list")
	s.mustSee("Claude exited.")
	data, err := os.ReadFile(logPath)
	if err != nil || !strings.HasSuffix(string(data), "mcp\nlist\n") {
		t.Fatalf("argv=%q (%v), want Claude's mcp list", data, err)
	}
	if got := loadDefaultProvider(root); got != "claude" {
		t.Fatalf("saved default = %q, want claude", got)
	}
}
