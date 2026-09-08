package tui

import (
	"strings"
	"testing"
)

func TestProbeHarnesses(t *testing.T) {
	probes := ProbeHarnesses()
	if len(probes) != 4 {
		t.Fatalf("expected 4 probed harnesses, got %d", len(probes))
	}

	foundMap := make(map[string]HarnessDiscoveryResult)
	for _, p := range probes {
		foundMap[p.HarnessName] = p
	}

	// Detection is host-dependent, but discovery must never execute the binary
	// or invent a version/model.
	agy, ok := foundMap["antigravity"]
	if !ok {
		t.Fatalf("antigravity probe missing")
	}
	if agy.Installed && (agy.State != StateAvailable || agy.Version != "NOT_PROBED") {
		t.Errorf("installed harness must remain unexecuted: %#v", agy)
	}
	if !agy.Installed && (agy.State != StateUnavailable || !strings.Contains(agy.Reason, "not found")) {
		t.Errorf("unavailable harness result is inconsistent: %#v", agy)
	}
	if len(agy.Models) != 0 {
		t.Errorf("expected 0 models for unavailable agy (never fabricate models), got %v", agy.Models)
	}

	// DiscoverTeamParticipants
	participants := DiscoverTeamParticipants(nil)
	if len(participants) != 4 {
		t.Fatalf("expected 4 participants, got %d", len(participants))
	}
	for _, p := range participants {
		if p.AgentID == "antigravity" {
			if p.IsActive != agy.Installed {
				t.Errorf("participant activity must match non-executing discovery")
			}
			if p.Model != "UNKNOWN" && p.Model != "UNAVAILABLE" {
				t.Errorf("expected antigravity model to be UNKNOWN or UNAVAILABLE, got %q", p.Model)
			}
		}
	}
}

func TestProbeGitStatus(t *testing.T) {
	gitStatus := ProbeGitStatus("")
	if gitStatus.Branch == "" || gitStatus.Branch == "unknown" {
		t.Errorf("expected valid git branch, got %q", gitStatus.Branch)
	}
	if gitStatus.Commit == "" || gitStatus.Commit == "unknown" {
		t.Errorf("expected valid git commit hash, got %q", gitStatus.Commit)
	}
	t.Logf("Probed Git Status: branch=%s commit=%s clean=%v changed=%d",
		gitStatus.Branch, gitStatus.Commit, gitStatus.Clean, gitStatus.ChangedCount)
}
