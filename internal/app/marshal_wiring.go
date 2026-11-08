package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/harness"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
	"github.com/Zen1th53/marshal/internal/verification"
)

// MarshalWiring is what a surface supplies to run a real Marshal plan.
type MarshalWiring struct {
	// SessionID selects an existing canonical goal for the first planning turn.
	// It supplies no goal approval or worker execution authority.
	SessionID string
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
	if !slices.Contains(MarshalProviders(), w.Provider) {
		return nil, fmt.Errorf("marshal: unsupported Marshal model provider %q", w.Provider)
	}
	if w.Approver == nil {
		return nil, errors.New("marshal: an approval source is required")
	}
	s.Model = &MarshalCLI{Provider: w.Provider, Dir: s.Repository, ProjectID: string(s.CanonicalPlanProjectID()), runtime: r, requiredGoalSession: w.SessionID, ProjectContext: s.ProjectContext, ConfigureKnowledgeMCP: s.ConfigureKnowledgeMCP}
	s.ModelProvider = w.Provider
	s.Reviewer = "marshal:" + w.Provider
	s.Gate = w.Gate
	s.ApprovalActor = w.Approver
	s.ProbeWorker = func(ctx context.Context, worker string) error {
		_, err := r.VerifyMarshalWorker(ctx, worker, "")
		return err
	}
	s.InstalledVersion = installedCLIVersion
	s.HandInGuard = r.guardHoneypotHandIn
	// Every role runs in a session of its own. The ULTRA cross-reviewer and
	// the verifier are started fresh, never resumed from the Marshal's or a
	// worker's conversation, so none of them judges work it saw being made.
	// Verification requires a provider different from every worker provider.
	s.CrossReview = func(ctx context.Context, task marshal.Task, handin marshal.HandIn, control marshal.Control) (marshal.Review, string, error) {
		// In the private edition a reviewer that reports its own quota or
		// rate limit is skipped and the next independent provider reviews.
		tried := []string{handin.Provider}
		for {
			provider, err := crossReviewProvider(r.roleProviders(), tried, w.Provider, task.PlanTaskID)
			if err != nil {
				return marshal.Review{}, provider, err
			}
			review, err := s.knowledgeRoleCLI(provider, s.Repository).Review(ctx, task, handin, control)
			review.Reviewer = "cross-review:" + provider
			if err != nil && r.retryRoleAfterLimit(ctx, "cross-review", provider, err, tried) {
				tried = append(tried, provider)
				continue
			}
			return review, provider, err
		}
	}
	// Runtime checks and an independent model must both accept the integrated
	// commit. A new model session is used for each verification decision.
	s.VerifierProvider = func(_ context.Context, run marshal.Run) (string, error) {
		return r.independentVerifierProvider(run)
	}
	s.IndependentVerify = func(ctx context.Context, run marshal.Run, head string, session verification.Session) (marshal.VerifierEvidence, error) {
		provider, err := s.VerifierProvider(ctx, run)
		if err != nil {
			return marshal.VerifierEvidence{}, err
		}
		return s.knowledgeRoleCLI(provider, filepath.Join(s.Worktrees, integrationTaskID(marshalRunID(run), run))).VerifyEvidence(ctx, run, head, session)
	}
	s.Drivers = map[string]driver.Driver{
		"codex":    driver.Codex(""),
		"claude":   driver.Claude(""),
		"agy":      driver.Agy(""),
		"opencode": driver.OpenCode(""),
	}
	for provider, d := range s.Drivers {
		native := d.(driver.Native)
		native.ConfigureLaunch = func(ctx context.Context, req driver.Request, args, env []string) ([]string, []string, func(), error) {
			binding, revoke, err := r.openKnowledgeReadLease(ctx, taskArtifactID(req.RunID, req.Task), req.Worktree)
			if err != nil {
				return nil, nil, nil, err
			}
			leaseCtx := sessionmcp.WithWorker(ctx, r.layout.Root, binding.TaskID, binding.LeaseID)
			args, env, closeTransport, err := sessionmcp.Configure(leaseCtx, nativeKnowledgeProvider(req.Task.Worker), args, env)
			if err != nil {
				revoke()
				return nil, nil, nil, err
			}
			if req.Task.Worker == "agy" || req.Task.Worker == "antigravity" {
				for i, arg := range args {
					if arg == req.Brief {
						args[i] += "\nMARSHAL_KNOWLEDGE_MCP contains a JSON argv array for this invocation's bound stdio MCP relay. Use that relay through your shell tool to call project_overview, find_symbol, references, related_files, project_search or project_context. Keep one relay session open while querying. Its project, task and lease are fixed by the runtime.\n"
					}
				}
			}
			return args, env, func() { closeTransport(); revoke() }, nil
		}
		s.Drivers[provider] = native
	}
	governedRun := r.marshalGovernedRun(s)
	s.GovernedDrivers = map[string]driver.Driver{
		"codex":    driver.Governed{Provider: "codex", Run: governedRun, Check: r.governedHandInCheck},
		"claude":   driver.Governed{Provider: "claude", Run: governedRun, Check: r.governedHandInCheck},
		"opencode": driver.Governed{Provider: "opencode", Run: governedRun, Check: r.governedHandInCheck},
	}
	// AGY is offered only where its native OAuth source and governed adapter
	// are supported. Registration itself never supplies a verified profile.
	if privateAGYEnabled() {
		s.GovernedDrivers["agy"] = driver.Governed{Provider: "agy", Run: governedRun, Check: r.governedHandInCheck}
	}
	s.GovernedDrivers["claude-code"] = s.GovernedDrivers["claude"]
	s.GateState = s.observedGateState
	s.GovernedCheck = r.runGovernedCheck
	s.Verify = s.verifyByChecks
	r.wirePrivateProcess05Wait(s)
	r.wireCodeEvidence(s)
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
	state.HarnessGovernance = s.workerTaskGovernance(ctx, run.Tasks[i]).State
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

// A verified native capability contract cannot vouch for a governed task's
// isolation. Native tasks retain the CLI-owned controls they were approved for.
func (s *MarshalService) workerTaskGovernance(ctx context.Context, task marshal.Task) harness.GovernanceAssessment {
	g := s.workerGovernance(ctx, task.Worker)
	if task.Mode != marshal.Governed || !g.Governed() {
		return g
	}
	p, err := s.Store.GetHarnessProfile(ctx, marshalHarnessName(task.Worker))
	if err != nil || p == nil || (p.FeatureSupport["sandbox"] == model.StatusUnsupported || p.FeatureSupport["network"] == model.StatusUnsupported) && !(sandbox.HostExecutionPreferred() && p.FeatureSupport["host-execution"] == model.StatusNative) {
		g.State = constitution.GovernanceUnverified
		g.Reasons = append(g.Reasons, "Native capability evidence does not establish governed sandbox and network enforcement.")
	}
	return g
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
	if worker == "claude-code" {
		worker = "claude"
	}
	versionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(versionCtx, worker, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

// freshRoleCLI starts a role in a new model session. It carries no
// conversation to resume, which is what keeps the role from seeing any other
// role's work in progress.
func freshRoleCLI(provider, dir string) *MarshalCLI {
	return &MarshalCLI{Provider: provider, Dir: dir}
}

// installedMarshalProviders lists the model CLIs that can take a Marshal role
// on this machine.
func installedMarshalProviders() []string {
	var out []string
	for _, provider := range MarshalProviders() {
		if _, err := exec.LookPath(provider); err == nil {
			out = append(out, provider)
		}
	}
	return out
}

// roleProvider picks the provider for a role: the first installed one not in
// avoid, or failing that the first of avoid, which is the Marshal's own
// provider and so known to run here. Either way the role gets a fresh session.
func roleProvider(installed []string, avoid ...string) string {
	for _, candidate := range installed {
		taken := false
		for _, a := range avoid {
			taken = taken || strings.EqualFold(candidate, a)
		}
		if !taken {
			return candidate
		}
	}
	if len(avoid) > 0 {
		return avoid[0]
	}
	return ""
}

// pickIndependentProvider chooses the provider for an independent reviewer or
// verifier. A provider that differs from every worker is always preferred,
// and among those one that also differs from the Marshal's own (extra). Only
// when every installed provider is a worker's does the role fall back to a
// worker's provider, and sameProvider reports that so the caller can record
// the fallback in the evidence instead of leaving it silent.
func pickIndependentProvider(installed, workers []string, extra ...string) (provider string, sameProvider bool) {
	isIn := func(list []string, c string) bool {
		for _, a := range list {
			if a != "" && strings.EqualFold(a, c) {
				return true
			}
		}
		return false
	}
	for _, candidate := range installed {
		if !isIn(workers, candidate) && !isIn(extra, candidate) {
			return candidate, false
		}
	}
	for _, candidate := range installed {
		if !isIn(workers, candidate) {
			return candidate, false
		}
	}
	for _, w := range workers {
		if w != "" {
			return w, true
		}
	}
	for _, e := range extra {
		if e != "" {
			return e, false
		}
	}
	return "", false
}

// sameProviderAsWorker reports whether provider is one of the workers'.
func sameProviderAsWorker(provider string, workers ...string) bool {
	for _, w := range workers {
		if w != "" && providerHealthKey(w) == providerHealthKey(provider) {
			return true
		}
	}
	return false
}

// verifyByChecks verifies the integrated head by re-running every task's
// approved checks there. A check that fails, cannot run, or changes the
// tree fails verification.
func (s *MarshalService) verifyMarshalIntegration(ctx context.Context, id string, run marshal.Run, head string) (verification.Session, verification.Binding, error) {
	if s.requiredCanonicalVerification != nil {
		return s.requiredCanonicalVerification(ctx, id, run, head)
	}
	return s.Verify(ctx, run, head)
}

func (s *MarshalService) verifyByChecks(ctx context.Context, run marshal.Run, head string) (verification.Session, verification.Binding, error) {
	runID := marshalRunID(run)
	dir := filepath.Join(s.Worktrees, integrationTaskID(runID, run))
	tree, err := gitMarshal(ctx, dir, "rev-parse", head+"^{tree}")
	if err != nil {
		return verification.Session{}, verification.Binding{}, err
	}
	binding := verification.Binding{ProjectID: s.ProjectID, GoalID: run.GoalBinding, PlanID: run.PlanID, RunID: runID, GoalRevision: 1, PlanVersion: run.PlanVersion, RunVersion: 1, TreeDigest: tree, EnvironmentDigest: "marshal-local"}
	governed := false
	for _, task := range run.Tasks {
		if task.Mode == marshal.Governed {
			governed = true
		}
	}
	proof, err := s.beginMarshalRuntimeProof(ctx, dir, run, head, tree, governed)
	if err != nil {
		return verification.Session{}, binding, err
	}
	checks := map[string]verification.Status{}
	for _, t := range run.Tasks {
		for i, c := range t.Checks {
			status := verification.StatusPass
			var checkErr error
			var observed *marshal.CommandRecord
			if governed {
				if s.GovernedCheck == nil {
					checkErr = errors.New("governed integration sandbox runner unavailable")
				} else {
					var record marshal.CommandRecord
					record, checkErr = runIntegrationCheckObserved(ctx, dir, head, c.Command, func(ctx context.Context, dir, command string) marshal.CommandRecord {
						return s.GovernedCheck(ctx, runID, t.PlanTaskID, t.Worker, dir, command)
					})
					observed = &record
				}
			} else {
				checkErr = runIntegrationCheck(ctx, dir, head, c.Command)
			}
			if checkErr != nil {
				status = verification.StatusFail
			}
			id := fmt.Sprintf("%s#%d", t.PlanTaskID, i)
			if err := s.appendMarshalRuntimeCheck(ctx, proof, id, t, c, observed, checkErr); err != nil {
				return verification.Session{}, binding, fmt.Errorf("check %s: %w", id, err)
			}
			checks[id] = status
		}
	}
	if len(checks) == 0 {
		return verification.Session{}, binding, errors.New("marshal: no approved checks to verify the integrated result")
	}
	if err := finishMarshalRuntimeProof(ctx, dir, proof, checks); err != nil {
		return verification.Session{}, binding, err
	}
	now := s.clock()
	return verification.Session{RuntimeProof: proof, ID: "marshal-" + head, Version: 1, Binding: binding, RequiredChecks: checks, CreatedAt: now, UpdatedAt: now}, binding, nil
}

// marshalRunID recovers the run ID from the task branch naming the service
// uses: marshal/<run>/<task>.
func marshalRunID(run marshal.Run) string {
	for _, t := range run.Tasks {
		if parts := strings.Split(t.Branch, "/"); (len(parts) == 3 || len(parts) == 4) && parts[0] == statepath.RefName {
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
	return runIntegrationCheckWithRunner(ctx, dir, head, command, nil)
}
func runIntegrationCheckWithRunner(ctx context.Context, dir, head, command string, runner driver.CheckRunner) error {
	_, err := runIntegrationCheckObserved(ctx, dir, head, command, runner)
	return err
}
func runIntegrationCheckObserved(ctx context.Context, dir, head, command string, runner driver.CheckRunner) (marshal.CommandRecord, error) {

	checkCtx := ctx
	if runner != nil {
		checkCtx = driver.WithCheckRunner(ctx, runner)
	}
	result := driver.RunCheck(checkCtx, dir, head, command, driver.DefaultCheckTimeout)
	if result.ExitCode != 0 {
		return result, fmt.Errorf("integration check failed: %s", result.Output)
	}

	status, err := gitMarshal(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return result, err
	}
	if len(hostgit.SourceStatusChanges(status)) != 0 {
		return result, fmt.Errorf("check %q changed the integrated tree", command)
	}
	current, err := gitMarshal(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return result, err
	}
	if current != head {
		return result, fmt.Errorf("check %q moved HEAD from %s to %s", command, head, current)
	}
	return result, nil
}

func (s *MarshalService) knowledgeRoleCLI(provider, root string) *MarshalCLI {
	cli := freshRoleCLI(provider, root)
	cli.ProjectContext = s.ProjectContext
	cli.ConfigureKnowledgeMCP = s.ConfigureKnowledgeMCP
	cli.runtime = s.runtime
	if s.runtime != nil {
		cli.ProjectID = s.runtime.ProjectIdentity()
	}
	return cli
}

// crossReviewProvider picks the provider for an independent cross-review.
// tried holds the worker's provider and any provider skipped for its limit.
// The private edition never falls back to one of them.
func crossReviewProvider(installed, tried []string, marshalProvider, taskID string) (string, error) {
	provider, sameProvider := pickIndependentProvider(installed, tried, marshalProvider)
	if sameProvider && independentCrossReviewRequired {
		return provider, fmt.Errorf("cross-review: no provider other than %s is available to review task %s", strings.Join(tried, ", "), taskID)
	}
	return provider, nil
}
