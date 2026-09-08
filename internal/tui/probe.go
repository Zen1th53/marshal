package tui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// HarnessDiscoveryResult represents the probed state of an agent harness.
type HarnessDiscoveryResult struct {
	HarnessName string
	BinaryPath  string
	Installed   bool
	Version     string
	State       string // StateAvailable, StateUnavailable, "NOT_CONFIGURED"
	Reason      string
	Models      []string
}

// Honest state labels. The TUI renders these verbatim rather than inventing a
// plausible-looking value, so an operator can always tell the difference
// between a fact MARSHAL established and one it could not.
const (
	// UnknownModel is shown when a harness is present but the model it will
	// serve has not been established from configuration or routing state.
	UnknownModel = "UNKNOWN"
	// UnavailableModel is shown when the harness itself is not installed.
	UnavailableModel = "UNAVAILABLE"

	StateAvailable   = "AVAILABLE"
	StateUnavailable = "UNAVAILABLE"
)

var (
	probeHarnessMu   sync.RWMutex
	cachedHarnesses  []HarnessDiscoveryResult
	harnessCacheTime time.Time

	probeGitMu      sync.RWMutex
	cachedGitStatus = make(map[string]GitStatusResult)
	gitCacheTime    = make(map[string]time.Time)
)

// InvalidateProbeCache forces the next probe calls to run live without using cached results.
func InvalidateProbeCache() {
	probeHarnessMu.Lock()
	cachedHarnesses = nil
	harnessCacheTime = time.Time{}
	probeHarnessMu.Unlock()

	probeGitMu.Lock()
	cachedGitStatus = make(map[string]GitStatusResult)
	gitCacheTime = make(map[string]time.Time)
	probeGitMu.Unlock()
}

// ProbeHarnesses performs live detection of external harnesses on the host system.
// Absolute Non-Negotiable Rule: Never fabricate or hardcode availability or versions.
func ProbeHarnesses() []HarnessDiscoveryResult {
	probeHarnessMu.RLock()
	if cachedHarnesses != nil && time.Since(harnessCacheTime) < 5*time.Second {
		res := make([]HarnessDiscoveryResult, len(cachedHarnesses))
		copy(res, cachedHarnesses)
		probeHarnessMu.RUnlock()
		return res
	}
	probeHarnessMu.RUnlock()

	probeHarnessMu.Lock()
	defer probeHarnessMu.Unlock()
	if cachedHarnesses != nil && time.Since(harnessCacheTime) < 5*time.Second {
		res := make([]HarnessDiscoveryResult, len(cachedHarnesses))
		copy(res, cachedHarnesses)
		return res
	}

	targets := []struct {
		name       string
		binaryName string
	}{
		{name: "claude", binaryName: "claude"},
		{name: "codex", binaryName: "codex"},
		{name: "opencode", binaryName: "opencode"},
		{name: "antigravity", binaryName: "agy"},
	}

	var results []HarnessDiscoveryResult
	for _, tgt := range targets {
		path, err := exec.LookPath(tgt.binaryName)
		if err != nil {
			results = append(results, HarnessDiscoveryResult{
				HarnessName: tgt.name,
				BinaryPath:  "",
				Installed:   false,
				Version:     UnknownModel,
				State:       StateUnavailable,
				Reason:      fmt.Sprintf("Required harness executable %q not found on PATH", tgt.binaryName),
				Models:      nil, // Do not fabricate models if binary is unavailable
			})
			continue
		}

		results = append(results, HarnessDiscoveryResult{
			HarnessName: tgt.name,
			BinaryPath:  path,
			Installed:   true,
			Version:     "NOT_PROBED",
			State:       StateAvailable,
			Reason:      "Executable detected; version probe deferred to governed runtime",
			Models:      nil, // The probe cannot read the harness's configured model.
		})
	}

	cachedHarnesses = results
	harnessCacheTime = time.Now()
	res := make([]HarnessDiscoveryResult, len(results))
	copy(res, results)
	return res
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
	probeGitMu.RLock()
	if res, ok := cachedGitStatus[workDir]; ok && time.Since(gitCacheTime[workDir]) < 3*time.Second {
		probeGitMu.RUnlock()
		return res
	}
	probeGitMu.RUnlock()

	probeGitMu.Lock()
	defer probeGitMu.Unlock()
	if res, ok := cachedGitStatus[workDir]; ok && time.Since(gitCacheTime[workDir]) < 3*time.Second {
		return res
	}

	res := GitStatusResult{
		Branch: "unknown",
		Commit: "unknown",
		Clean:  true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 1. Branch
	cmdBranch := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if workDir != "" {
		cmdBranch.Dir = workDir
	}
	var outBranch bytes.Buffer
	cmdBranch.Stdout = &outBranch
	if err := cmdBranch.Run(); err == nil {
		res.Branch = strings.TrimSpace(outBranch.String())
	}

	// 2. Commit
	cmdCommit := exec.CommandContext(ctx, "git", "rev-parse", "--short", "HEAD")
	if workDir != "" {
		cmdCommit.Dir = workDir
	}
	var outCommit bytes.Buffer
	cmdCommit.Stdout = &outCommit
	if err := cmdCommit.Run(); err == nil {
		res.Commit = strings.TrimSpace(outCommit.String())
	}

	// 3. Status
	cmdStatus := exec.CommandContext(ctx, "git", "status", "--porcelain")
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

	cachedGitStatus[workDir] = res
	gitCacheTime[workDir] = time.Now()
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
			modelName := UnknownModel
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
				p.Model = UnavailableModel
			}
		}
	}

	return existing
}
