package tui

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// HarnessDiscoveryResult represents the probed state of an agent harness.
type HarnessDiscoveryResult struct {
	HarnessName string
	BinaryPath  string
	Installed   bool
	Version     string
	State       string // "AVAILABLE", "UNAVAILABLE", "NOT_CONFIGURED"
	Reason      string
	Models      []string
}

// ProbeHarnesses performs live detection of external harnesses on the host system.
// Absolute Non-Negotiable Rule: Never fabricate or hardcode availability or versions.
func ProbeHarnesses() []HarnessDiscoveryResult {
	targets := []struct {
		name       string
		binaryName string
		models     []string
	}{
		{name: "claude", binaryName: "claude", models: []string{"claude-3-7-sonnet", "claude-3-5-sonnet"}},
		{name: "codex", binaryName: "codex", models: []string{"gpt-4o", "o3-mini", "o1"}},
		{name: "opencode", binaryName: "opencode", models: []string{"deepseek-coder", "claude-3-7-sonnet"}},
		{name: "antigravity", binaryName: "agy", models: []string{"gemini-2.5-pro", "gemini-2.5-flash"}},
	}

	var results []HarnessDiscoveryResult
	for _, tgt := range targets {
		path, err := exec.LookPath(tgt.binaryName)
		if err != nil {
			results = append(results, HarnessDiscoveryResult{
				HarnessName: tgt.name,
				BinaryPath:  "",
				Installed:   false,
				Version:     "UNKNOWN",
				State:       "UNAVAILABLE",
				Reason:      fmt.Sprintf("Required harness executable %q not found on PATH", tgt.binaryName),
				Models:      nil, // Do not fabricate models if binary is unavailable
			})
			continue
		}

		// Probe version if possible
		version := probeBinaryVersion(path)
		results = append(results, HarnessDiscoveryResult{
			HarnessName: tgt.name,
			BinaryPath:  path,
			Installed:   true,
			Version:     version,
			State:       "AVAILABLE",
			Reason:      "Installed and executable",
			Models:      tgt.models,
		})
	}

	return results
}

func probeBinaryVersion(binPath string) string {
	cmd := exec.Command(binPath, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil {
		firstLine := strings.TrimSpace(strings.Split(out.String(), "\n")[0])
		if firstLine != "" {
			return firstLine
		}
	}
	return "DETECTED"
}

// GitStatusResult tracks the current working tree state.
type GitStatusResult struct {
	Branch       string
	Commit       string
	ChangedCount int
	Clean        bool
}

// ProbeGitStatus queries git for branch, commit, and change status.
func ProbeGitStatus(workDir string) GitStatusResult {
	res := GitStatusResult{
		Branch: "unknown",
		Commit: "unknown",
		Clean:  true,
	}

	// 1. Branch
	cmdBranch := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	if workDir != "" {
		cmdBranch.Dir = workDir
	}
	var outBranch bytes.Buffer
	cmdBranch.Stdout = &outBranch
	if err := cmdBranch.Run(); err == nil {
		res.Branch = strings.TrimSpace(outBranch.String())
	}

	// 2. Commit
	cmdCommit := exec.Command("git", "rev-parse", "--short", "HEAD")
	if workDir != "" {
		cmdCommit.Dir = workDir
	}
	var outCommit bytes.Buffer
	cmdCommit.Stdout = &outCommit
	if err := cmdCommit.Run(); err == nil {
		res.Commit = strings.TrimSpace(outCommit.String())
	}

	// 3. Status
	cmdStatus := exec.Command("git", "status", "--porcelain")
	if workDir != "" {
		cmdStatus.Dir = workDir
	}
	var outStatus bytes.Buffer
	cmdStatus.Stdout = &outStatus
	if err := cmdStatus.Run(); err == nil {
		lines := strings.Split(strings.TrimSpace(outStatus.String()), "\n")
		count := 0
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				count++
			}
		}
		res.ChangedCount = count
		res.Clean = count == 0
	}

	return res
}

// DiscoverTeamParticipants reconciles session participants with actual live probes.
// If an agent's harness is not found, its status reflects real unavailability.
func DiscoverTeamParticipants(existing []model.Participant) []model.Participant {
	probes := ProbeHarnesses()
	probeMap := make(map[string]HarnessDiscoveryResult)
	for _, p := range probes {
		probeMap[p.HarnessName] = p
	}

	if len(existing) == 0 {
		roles := []struct {
			agent   string
			role    model.Role
			harness string
		}{
			{"claude", model.RoleArchitect, "claude"},
			{"codex", model.RoleDeveloper, "codex"},
			{"opencode", model.RoleQA, "opencode"},
			{"antigravity", model.RoleAppSec, "antigravity"},
		}

		for _, r := range roles {
			pr, found := probeMap[r.harness]
			modelName := "UNKNOWN"
			isActive := false
			if found && pr.Installed {
				isActive = true
				if len(pr.Models) > 0 {
					modelName = pr.Models[0]
				}
			}
			existing = append(existing, model.Participant{
				AgentID:  r.agent,
				Role:     r.role,
				Harness:  r.harness,
				Model:    modelName,
				IsActive: isActive,
			})
		}
		return existing
	}

	// If existing participants exist, update their active status based on live harness probe
	for i := range existing {
		p := &existing[i]
		if pr, found := probeMap[strings.ToLower(p.Harness)]; found {
			if !pr.Installed {
				p.IsActive = false
				p.Model = "UNAVAILABLE"
			}
		}
	}

	return existing
}
