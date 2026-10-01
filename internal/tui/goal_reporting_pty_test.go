//go:build linux

package tui

import "testing"

func TestPTYGoalHistoricalReports(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	terminal := startFrozenTUIInProject(t, 40, 180, bin, project, "tui", "SESSION-goal-report-pty")
	terminal.sendLine("/goal create Fix the parser typo")
	terminal.mustSee("[rev 1]")
	terminal.sendLine("/goal edit Fix only the parser typo")
	terminal.mustSee("[rev 2] PENDING")
	terminal.sendLine("/goal version 1")
	terminal.mustSee("\"revision\": 1")
	terminal.sendLine("/goal diff 1 2")
	terminal.mustSee("\"previous_revision\": 1")
	terminal.mustSee("\"new_revision\": 2")
	terminal.sendLine("/goal progress")
	terminal.mustSee("GOAL PROGRESS [rev 2]")
	terminal.sendLine("/quit")
	if err := terminal.cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}
