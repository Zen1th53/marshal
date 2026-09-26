package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/harness"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

// MarshalWiring is what a surface supplies to run a real Marshal plan.
type MarshalWiring struct {
	// Provider is the Marshal model's CLI: "codex", "claude" or "agy".
	Provider string
	// Gate is the session's ULTRA gate; nil means Standard.
	Gate marshal.CapabilityGate
	// Approver returns the person who approved purpose ("plan", "close",
	// "task") for runID. It must answer only from an approval the person
	// actually gave on this surface, and fail otherwise.
	Approver func(ctx context.Context, runID, purpose string) (string, error)
}

// MarshalWired returns the project Marshal service connected to real
// workers, the Marshal model, the constitutional gate and verification.
//
// Every input the gate sees is something the runtime observed. Native CLI
// workers run under the user's own subscription, which MARSHAL does not
// govern step by step, so harness governance is reported as unverified and
// the gate operates in its reduced mode instead of assuming control it does
// not have.
func (r *Runtime) MarshalWired(w MarshalWiring) (*MarshalService, error) {
	s := r.Marshal()
	if s == nil {
		return nil, errors.New("marshal: no project is open")
	}
	switch w.Provider {
	case "codex", "claude", "agy":
	default:
		return nil, fmt.Errorf("marshal: unsupported Marshal model provider %q", w.Provider)
	}
	if w.Approver == nil {
		return nil, errors.New("marshal: an approval source is required")
	}
	s.Model = &MarshalCLI{Provider: w.Provider, Dir: s.Repository}
	s.ModelProvider = w.Provider
	s.Reviewer = "marshal:" + w.Provider
	s.Gate = w.Gate
	s.ApprovalActor = w.Approver
	s.InstalledVersion = installedCLIVersion
	// ULTRA cross-review comes from a model of a provider other than both the
	// worker's and the Marshal's, so no model family reviews its own work.
	s.CrossReview = func(ctx context.Context, task marshal.Task, handin marshal.HandIn) (marshal.Review, string, error) {
		provider := otherMarshalProvider(w.Provider, handin.Provider)
		if provider == "" {
			return marshal.Review{}, "", errors.New("marshal: no independent provider is available for cross-review")
		}
		review, err := (&MarshalCLI{Provider: provider, Dir: s.Repository}).Review(ctx, task, handin)
		review.Reviewer = "cross-review:" + provider
		return review, provider, err
	}
	// Runtime check reruns are mandatory, but they are not an independent
	// verifier agent. Until one is wired, ULTRA verification fails closed.
	s.VerifierProvider = nil
	s.Drivers = map[string]driver.Driver{
		"codex":    driver.Codex(""),
		"claude":   driver.Claude(""),
		"agy":      driver.Agy(""),
		"opencode": driver.OpenCode(""),
	}
	s.GateState = s.observedGateState
	s.Verify = s.verifyByChecks
	return s, nil
}

// observedGateState reports what the runtime knows at decision time.
//
// AuthorizedActor derives from the user's plan approval: the runtime acts
// only on a run the person approved. Evidence for a task decision is filled
// in by the acceptance evaluation from the hand-in itself; for a close
// (no task) it is present and fresh because Close verifies the integrated
// head immediately before asking the gate. Sandbox and network enforcement
// are reported false because MARSHAL does not provide them to native
// workers; the Marshal decision domains do not require them.
func (s *MarshalService) observedGateState(ctx context.Context, runID, taskID string) (constitution.RuntimeState, error) {
	run, _, err := s.load(ctx, runID)
	if err != nil {
		return constitution.RuntimeState{}, err
	}
	approved := run.State != marshal.Drafting && run.PlanVersion > 0
	state := constitution.RuntimeState{AuthorizedActor: approved}
	if taskID == "" || taskID == "close" {
		// Closing is the runtime's own git operation; no harness is involved.
		// Close asks the gate with the pseudo task "close".
		state.EvidencePresent = true
		state.EvidenceFresh = true
		return state, nil
	}
	i := taskIndex(run, taskID)
	if i < 0 {
		return state, fmt.Errorf("marshal: task %s is not in run %s", taskID, runID)
	}
	state.HarnessGovernance = s.workerGovernance(ctx, run.Tasks[i].Worker).State
	return state, nil
}

// workerGovernance assesses how far MARSHAL governs a worker's CLI, from the
// recorded capability probe and the installed version. A worker with no
// probe evidence is unverified: the gate will not accept its work until the
// harness has been probed.
func (s *MarshalService) workerGovernance(ctx context.Context, worker string) harness.GovernanceAssessment {
	installed := ""
	if s.InstalledVersion != nil {
		installed = s.InstalledVersion(ctx, worker)
	}
	profile, err := s.Store.GetHarnessProfile(ctx, marshalHarnessName(worker))
	if err != nil || profile == nil {
		p := model.HarnessProfile{Harness: marshalHarnessName(worker)}
		return harness.AssessGovernance(p, installed, s.clock())
	}
	return harness.AssessGovernance(*profile, installed, s.clock())
}

// marshalHarnessName maps a worker to its harness profile name.
func marshalHarnessName(worker string) string {
	switch worker {
	case "claude":
		return "claude-code"
	case "agy":
		return "antigravity"
	default:
		return worker
	}
}

// installedCLIVersion asks a worker's CLI for its version. `--version` is
// answered locally by every supported CLI and opens no provider session.
func installedCLIVersion(ctx context.Context, worker string) string {
	versionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(versionCtx, worker, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

// otherMarshalProvider picks a model provider different from every one given.
func otherMarshalProvider(exclude ...string) string {
	for _, candidate := range []string{"codex", "claude", "agy"} {
		taken := false
		for _, e := range exclude {
			if strings.EqualFold(candidate, e) {
				taken = true
			}
		}
		if !taken {
			return candidate
		}
	}
	return ""
}

// verifyByChecks verifies the integrated head by re-running every task's
// approved checks there. A check that fails, cannot run, or changes the
// tree fails verification.
func (s *MarshalService) verifyByChecks(ctx context.Context, run marshal.Run, head string) (verification.Session, verification.Binding, error) {
	runID := marshalRunID(run)
	dir := filepath.Join(s.Worktrees, "TASK-"+runID+"-integration")
	binding := verification.Binding{ProjectID: s.ProjectID, GoalID: run.GoalBinding, PlanID: run.PlanID, RunID: runID, GoalRevision: 1, PlanVersion: run.PlanVersion, RunVersion: 1, TreeDigest: head, EnvironmentDigest: "marshal-local"}
	checks := map[string]verification.Status{}
	for _, t := range run.Tasks {
		for i, c := range t.Checks {
			status := verification.StatusPass
			if err := runIntegrationCheck(ctx, dir, head, c.Command); err != nil {
				status = verification.StatusFail
			}
			checks[fmt.Sprintf("%s#%d", t.PlanTaskID, i)] = status
		}
	}
	if len(checks) == 0 {
		return verification.Session{}, binding, errors.New("marshal: no approved checks to verify the integrated result")
	}
	now := s.clock()
	return verification.Session{ID: "marshal-" + head, Version: 1, Binding: binding, RequiredChecks: checks, CreatedAt: now, UpdatedAt: now}, binding, nil
}

// marshalRunID recovers the run ID from the task branch naming the service
// uses: marshal/<run>/<task>.
func marshalRunID(run marshal.Run) string {
	for _, t := range run.Tasks {
		if parts := strings.Split(t.Branch, "/"); len(parts) == 3 && parts[0] == "marshal" {
			return parts[1]
		}
	}
	return ""
}

// runIntegrationCheck runs one approved check in the integration worktree at
// head. A check that leaves the tree changed, or moves HEAD even with a clean
// tree, fails: later checks and the close would otherwise judge a commit
// that is not the one verified.
func runIntegrationCheck(ctx context.Context, dir, head, command string) error {
	checkCtx, cancel := context.WithTimeout(ctx, driver.DefaultCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, "sh", "-c", command)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return err
	}
	status, err := gitMarshal(ctx, dir, "status", "--porcelain")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("check %q changed the integrated tree", command)
	}
	current, err := gitMarshal(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if current != head {
		return fmt.Errorf("check %q moved HEAD from %s to %s", command, head, current)
	}
	return nil
}
