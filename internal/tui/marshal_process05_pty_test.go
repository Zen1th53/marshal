//go:build linux

package tui

import "testing"

// The Process 05 entry point must be reachable through the shipped terminal
// binary and refuse execution until a canonical approved plan exists.
func TestPTYMarshalUsePlanRequiresApprovedPlan(t *testing.T) {
	s := startCommandTUI(t, 40, 160)
	s.sendLine("/marshal use-plan")
	s.mustSee("execution plan not found")
}
