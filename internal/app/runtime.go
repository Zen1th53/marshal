package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/adapter/gemini"
	"github.com/Zen1th53/marshal/internal/adapter/opencode"
	artifactstore "github.com/Zen1th53/marshal/internal/artifact"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/worker"
	"github.com/Zen1th53/marshal/internal/worktree"
	"go.yaml.in/yaml/v3"
)

const localProjectID = "PROJECT-local"

type Runtime struct {
	layout            project.Layout
	store             *store.Store
	policy            *policy.Engine
	adapters          map[string]adapter.Adapter
	evidenceSanitizer evidence.Sanitizer
	runtimeInstanceID string
	runtimePolicy     RuntimePolicyConfig
	policyConfigured  bool
	capabilityBroker  capability.Broker
}

type Options struct {
	Adapters           map[string]adapter.Adapter
	EvidenceAuthorizer evidence.Authorizer
	EvidenceSanitizer  evidence.Sanitizer
	Metrics            *evidence.MetricsRecorder
	RuntimePolicy      *RuntimePolicyConfig
	CapabilityBroker   capability.Broker
}

type Status struct {
	Project       model.Project `json:"project"`
	SchemaVersion int           `json:"schema_version"`
	AgentCount    int           `json:"agent_count"`
	SessionCount  int           `json:"session_count"`
	TaskCount     int           `json:"task_count"`
	LeaseCount    int           `json:"lease_count"`
}

type RegisterAgentRequest struct {
	Name          string     `json:"name"`
	Role          model.Role `json:"role"`
	ModelProvider string     `json:"model_provider,omitempty"`
	ModelName     string     `json:"model_name,omitempty"`
	Capabilities  []string   `json:"capabilities,omitempty"`
}

type ClaimRequest struct {
	TaskID           string `json:"task_id"`
	AgentID          string `json:"agent_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type ClaimResult struct {
	Lease   model.Lease   `json:"lease"`
	Session model.Session `json:"session"`
}

type ReleaseRequest struct {
	TaskID        string `json:"task_id"`
	BlockedReason string `json:"blocked_reason,omitempty"`
}

type RunRequest struct {
	TaskID           string `json:"task_id"`
	AgentID          string `json:"agent_id"`
	Adapter          string `json:"adapter"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type RunResult struct {
	RunID          string                    `json:"run_id"`
	TaskID         string                    `json:"task_id"`
	Status         string                    `json:"status"`
	BaseCommit     string                    `json:"base_commit"`
	ResultCommit   string                    `json:"result_commit"`
	ExitStatus     int                       `json:"exit_status"`
	Isolation      model.IsolationCapability `json:"isolation"`
	StdoutArtifact model.Artifact            `json:"stdout_artifact"`
	StderrArtifact model.Artifact            `json:"stderr_artifact"`
}

type VerifyRequest struct {
	Command []string `json:"command"`
	AgentID string   `json:"agent_id,omitempty"`
}

type VerifyResult struct {
	Command      []string `json:"command"`
	ExitStatus   int      `json:"exit_status"`
	OutputDigest string   `json:"output_digest"`
	Stdout       string   `json:"stdout"`
	Stderr       string   `json:"stderr"`
	Commit       string   `json:"commit"`
}

type versionDocument struct {
	SchemaVersion int    `yaml:"schema_version"`
	PackVersion   string `yaml:"pack_version"`
}

func Bootstrap(ctx context.Context, root string) (project.Layout, error) {
	layout, err := project.Discover(root)
	if err != nil {
		return project.Layout{}, err
	}
	version, err := loadPackVersion(filepath.Join(layout.Root, "PACK-VERSION.yaml"))
	if err != nil {
		return project.Layout{}, err
	}
	if _, err := os.Stat(filepath.Join(layout.Root, "RUNTIME-VERSION.yaml")); err != nil {
		return project.Layout{}, fmt.Errorf("read runtime version: %w", err)
	}
	if err := layout.Ensure(); err != nil {
		return project.Layout{}, err
	}
	database, err := store.Open(ctx, layout.Database)
	if err != nil {
		return project.Layout{}, err
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		return project.Layout{}, err
	}
	if err := database.InitProject(ctx, model.Project{
		ID: localProjectID, Repository: layout.Root, DefaultBranch: layout.Branch,
		PackVersion: version,
	}); err != nil {
		return project.Layout{}, err
	}
	return layout, nil
}

func Open(ctx context.Context, root string) (*Runtime, error) {
	return OpenWithOptions(ctx, root, Options{})
}

func OpenWithOptions(ctx context.Context, root string, options Options) (*Runtime, error) {
	layout, err := project.Discover(root)
	if err != nil {
		return nil, err
	}
	sanitizer := options.EvidenceSanitizer
	if sanitizer == nil {
		sanitizer = evidence.NewStrictSanitizer(evidence.SanitizerConfig{})
	}
	database, err := store.OpenWithObservability(ctx, layout.Database, sanitizer, options.EvidenceAuthorizer, options.Metrics)
	if err != nil {
		return nil, err
	}
	if err := database.Migrate(ctx); err != nil {
		database.Close()
		return nil, err
	}
	identity, err := database.Project(ctx)
	if err != nil {
		database.Close()
		return nil, fmt.Errorf("runtime is not initialized: %w", err)
	}
	if identity.Repository != layout.Root {
		database.Close()
		return nil, fmt.Errorf("%w: runtime repository identity differs", model.ErrConflict)
	}
	engine, err := policy.Load(filepath.Join(layout.Root, "CAPABILITIES.yaml"))
	if err != nil {
		database.Close()
		return nil, err
	}
	instanceID, err := model.NewID("INSTANCE-")
	if err != nil {
		database.Close()
		return nil, err
	}
	rt := &Runtime{
		layout:            layout,
		store:             database,
		policy:            engine,
		adapters:          options.Adapters,
		evidenceSanitizer: sanitizer,
		runtimeInstanceID: instanceID,
		capabilityBroker:  options.CapabilityBroker,
	}
	if options.RuntimePolicy != nil {
		rt.runtimePolicy = *options.RuntimePolicy
		rt.policyConfigured = true
	}
	_ = rt.ReconcileStartup(ctx)
	return rt, nil
}

func (r *Runtime) InstanceID() string { return r.runtimeInstanceID }

func (r *Runtime) Close() error { return r.store.Close() }

func (r *Runtime) ReconcileStartup(ctx context.Context) error {
	tasks, err := r.store.ListTasks(ctx)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.Status == model.TaskWorking || task.Status == model.TaskClaimed {
			active, activeErr := r.store.ActiveLease(ctx, task.ID)
			if activeErr == nil && active.Lease.ExpiresAt.Before(time.Now().UTC()) {
				_ = r.store.ReleaseTask(ctx, model.ReleaseRequest{
					TaskID:           task.ID,
					LeaseID:          active.Lease.ID,
					SessionID:        active.Lease.SessionID,
					AgentID:          active.AgentID,
					ExpectedRevision: active.TaskRevision,
					BlockedReason:    "reconciled stale lease from previous daemon instance",
				})
			}
		}
	}
	return nil
}

func (r *Runtime) CancelTask(ctx context.Context, taskID string) error {
	task, err := r.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Status == model.TaskCancelled {
		return nil
	}
	active, activeErr := r.store.ActiveLease(ctx, taskID)
	if activeErr == nil {
		_ = r.store.ReleaseTask(ctx, model.ReleaseRequest{
			TaskID:           taskID,
			LeaseID:          active.Lease.ID,
			SessionID:        active.Lease.SessionID,
			AgentID:          active.AgentID,
			ExpectedRevision: active.TaskRevision,
			BlockedReason:    "task cancelled by supervisor",
		})
	}
	return nil
}

func (r *Runtime) Status(ctx context.Context) (Status, error) {
	projectIdentity, err := r.store.Project(ctx)
	if err != nil {
		return Status{}, err
	}
	version, err := r.store.SchemaVersion(ctx)
	if err != nil {
		return Status{}, err
	}
	status := Status{Project: projectIdentity, SchemaVersion: version}
	counts := []struct {
		table string
		value *int
	}{{"agents", &status.AgentCount}, {"sessions", &status.SessionCount}, {"tasks", &status.TaskCount}, {"leases", &status.LeaseCount}}
	for _, count := range counts {
		*count.value, err = r.store.Count(ctx, count.table)
		if err != nil {
			return Status{}, err
		}
	}
	return status, nil
}

func (r *Runtime) RegisterAgent(ctx context.Context, request RegisterAgentRequest) (model.Agent, error) {
	id, err := model.NewID("AGENT-")
	if err != nil {
		return model.Agent{}, err
	}
	agent := model.Agent{ID: id, ProjectID: localProjectID, DisplayName: request.Name,
		Role: request.Role, ModelProvider: request.ModelProvider, ModelName: request.ModelName,
		Capabilities: request.Capabilities, Status: model.AgentRegistered}
	if err := r.store.RegisterAgent(ctx, agent); err != nil {
		return model.Agent{}, err
	}
	return agent, nil
}

func (r *Runtime) Agents(ctx context.Context) ([]model.Agent, error) { return r.store.ListAgents(ctx) }

func (r *Runtime) ImportTasks(ctx context.Context, tasks []model.Task) (model.ImportResult, error) {
	return r.store.ImportTasks(ctx, tasks)
}

func (r *Runtime) Tasks(ctx context.Context) ([]model.Task, error) { return r.store.ListTasks(ctx) }

func (r *Runtime) Task(ctx context.Context, taskID string) (model.Task, error) {
	return r.store.GetTask(ctx, taskID)
}

func (r *Runtime) Claim(ctx context.Context, request ClaimRequest) (ClaimResult, error) {
	sessionID, err := model.NewID("SESSION-")
	if err != nil {
		return ClaimResult{}, err
	}
	session, err := r.store.StartSession(ctx, model.SessionStart{
		ID: sessionID, AgentID: request.AgentID, ProjectID: localProjectID,
		Branch: r.layout.Branch, Worktree: r.layout.Root,
	})
	if err != nil {
		return ClaimResult{}, err
	}
	lease, err := r.store.ClaimTask(ctx, model.ClaimRequest{
		TaskID: request.TaskID, AgentID: request.AgentID, SessionID: session.ID,
		ExpectedRevision: request.ExpectedRevision, ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
	})
	if err != nil {
		_ = r.store.TerminateSession(ctx, session.ID, model.SessionTerminated, session.Revision)
		return ClaimResult{}, err
	}
	session, err = r.store.GetSession(ctx, session.ID)
	if err != nil {
		return ClaimResult{}, err
	}
	return ClaimResult{Lease: lease, Session: session}, nil
}

func (r *Runtime) Release(ctx context.Context, request ReleaseRequest) error {
	active, err := r.store.ActiveLease(ctx, request.TaskID)
	if err != nil {
		return err
	}
	if err := r.store.ReleaseTask(ctx, model.ReleaseRequest{
		TaskID: request.TaskID, LeaseID: active.Lease.ID, SessionID: active.Lease.SessionID,
		AgentID: active.AgentID, ExpectedRevision: active.TaskRevision, BlockedReason: request.BlockedReason,
	}); err != nil {
		return err
	}
	session, err := r.store.GetSession(ctx, active.Lease.SessionID)
	if err != nil {
		return err
	}
	return r.store.TerminateSession(ctx, session.ID, model.SessionTerminated, session.Revision)
}

func (r *Runtime) Events(ctx context.Context) ([]model.Event, error) {
	return r.store.ListEvents(ctx)
}

func (r *Runtime) Artifacts(ctx context.Context) ([]model.Artifact, error) {
	return r.store.ListArtifacts(ctx)
}

func (r *Runtime) Verify(ctx context.Context, request VerifyRequest) (VerifyResult, error) {
	if len(request.Command) == 0 {
		request.Command = []string{"python", "conformance/runner.py", "validate-pack"}
	}
	if err := r.authorizeRuntime(ctx, "verification", "", "", policy.Action("verify"), policy.Resource(request.Command[0])); err != nil {
		return VerifyResult{}, err
	}
	var process adapter.ProcessRunner = worker.New(15*time.Minute, 3*time.Second, 8<<20)
	if r.capabilityBroker != nil {
		process = adapter.NewCapabilityRunner(process, r.capabilityBroker, request.AgentID, request.Command[0])
	}
	result, err := process.Run(ctx, adapter.Command{Path: request.Command[0], Args: request.Command[1:], Dir: r.layout.Root})
	if err != nil {
		return VerifyResult{}, err
	}
	digest := sha256.Sum256(append(append([]byte(nil), result.Stdout...), result.Stderr...))
	verification := VerifyResult{Command: request.Command, ExitStatus: result.ExitCode,
		OutputDigest: "sha256:" + hex.EncodeToString(digest[:]), Stdout: string(result.Stdout),
		Stderr: string(result.Stderr), Commit: r.layout.HEAD}
	if result.ExitCode != 0 || result.TimedOut || result.Cancelled || result.OutputTruncated {
		return verification, fmt.Errorf("verification failed with exit status %d", result.ExitCode)
	}
	return verification, nil
}

func (r *Runtime) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	if request.Adapter == "" {
		request.Adapter = "codex"
	}
	task, err := r.store.GetTask(ctx, request.TaskID)
	if err != nil {
		return RunResult{}, err
	}
	if gateErr := r.authorizeRuntime(ctx, request.AgentID, task.ID, request.Adapter,
		policy.Action("shell.execute"), policy.Resource(r.layout.Root)); gateErr != nil {
		return RunResult{}, gateErr
	}
	claim, err := r.Claim(ctx, ClaimRequest{TaskID: task.ID, AgentID: request.AgentID, ExpectedRevision: request.ExpectedRevision})
	if err != nil {
		return RunResult{}, err
	}
	claimedRevision := request.ExpectedRevision + 1
	releasePreparationFailure := func() {
		active, activeErr := r.store.ActiveLease(context.Background(), task.ID)
		if activeErr == nil {
			_ = r.store.ReleaseTask(context.Background(), model.ReleaseRequest{
				TaskID: task.ID, LeaseID: active.Lease.ID, SessionID: active.Lease.SessionID,
				AgentID: active.AgentID, ExpectedRevision: active.TaskRevision,
				BlockedReason: "worker preparation failed",
			})
		}
	}
	input := model.PolicyInput{
		AgentID: request.AgentID, SessionID: claim.Session.ID, Role: claim.Session.Role,
		TaskID: task.ID, Risk: task.Risk, Operation: model.ShellExecute,
		Target: r.layout.Root, TaskOwned: true, TargetInScope: true, Required: true,
	}
	if err := policy.Enforce(r.policy, input, func() error { return nil }); err != nil {
		releasePreparationFailure()
		return RunResult{}, err
	}
	baseCommit := r.layout.HEAD
	if task.BaseCommit != nil {
		baseCommit = *task.BaseCommit
	}
	branch := "marshal/" + task.ID
	worktreeManager := worktree.New(r.layout.Root, r.layout.Worktrees)
	worktreeState, err := worktreeManager.Prepare(ctx, model.WorktreeRequest{
		TaskID: task.ID, Branch: branch, BaseCommit: baseCommit,
	})
	if err != nil {
		releasePreparationFailure()
		return RunResult{}, err
	}
	if err := r.store.BeginExecution(ctx, task.ID, claim.Session.ID, request.AgentID,
		branch, worktreeState.Path, baseCommit, claimedRevision); err != nil {
		releasePreparationFailure()
		return RunResult{}, err
	}
	executionRevision := claimedRevision + 1
	agentAdapter, err := r.resolveAdapter(ctx, request.Adapter, task, worktreeState.Path)
	if err != nil {
		_ = r.store.FinalizeExecution(context.Background(), task.ID, claim.Session.ID, false, executionRevision)
		return RunResult{}, err
	}
	probe, err := agentAdapter.Probe(ctx)
	if err != nil {
		_ = r.store.FinalizeExecution(context.Background(), task.ID, claim.Session.ID, false, executionRevision)
		return RunResult{}, err
	}
	runID, err := model.NewID("RUN-")
	if err != nil {
		return RunResult{}, err
	}
	if err := r.store.StartRun(ctx, model.WorkerRun{
		ID: runID, TaskID: task.ID, SessionID: claim.Session.ID, Adapter: request.Adapter,
		AdapterVersion: probe.Version, BaseCommit: baseCommit, StartedAt: time.Now().UTC(), Status: "running",
	}); err != nil {
		_ = r.store.FinalizeExecution(context.Background(), task.ID, claim.Session.ID, false, executionRevision)
		return RunResult{}, err
	}
	heartbeatRevision := claim.Session.Revision
	var heartbeatMu sync.Mutex
	heartbeat := func() {
		heartbeatMu.Lock()
		defer heartbeatMu.Unlock()
		if err := r.store.Heartbeat(context.Background(), claim.Session.ID, time.Now().UTC(), heartbeatRevision); err == nil {
			heartbeatRevision++
		}
	}
	result, runErr := agentAdapter.Run(ctx, adapter.Request{
		TaskID: task.ID, Title: task.Title, Worktree: worktreeState.Path,
		BaseCommit: baseCommit, HeadCommit: baseCommit,
		AllowedOperations: []string{"filesystem.read", "filesystem.write", "shell.execute"},
		EvidenceRequired:  []string{"git status --short", "git log -1 --oneline"},
		Heartbeat:         heartbeat,
		HeartbeatInterval: 5 * time.Second,
	})
	state, inspectErr := worktreeManager.Inspect(context.Background(), worktreeState.Path)
	if runErr == nil && inspectErr == nil && result.Status == adapter.StatusSuccess && result.ExitCode == 0 {
		if state.Dirty {
			commitInput := input
			commitInput.Operation = model.GitCommit
			commitInput.Target = worktreeState.Path
			runErr = policy.Enforce(r.policy, commitInput, func() error {
				return commitTaskChanges(ctx, worktreeState.Path, task.ID)
			})
			if runErr == nil {
				state, inspectErr = worktreeManager.Inspect(context.Background(), worktreeState.Path)
			}
		}
		if runErr == nil && inspectErr == nil && state.HEAD == baseCommit {
			runErr = fmt.Errorf("%w: worker produced no commit", model.ErrConflict)
		}
	}
	resultCommit := baseCommit
	if inspectErr == nil {
		resultCommit = state.HEAD
	}
	artifacts := artifactstore.New(r.layout.Artifacts, r.store)
	stdout, stdoutErr := r.sanitizeProviderOutput(ctx, result.Stdout)
	stderr, stderrErr := r.sanitizeProviderOutput(ctx, result.Stderr)
	var stdoutArtifact, stderrArtifact model.Artifact
	if stdoutErr == nil {
		stdoutArtifact, stdoutErr = artifacts.Put(ctx, model.ArtifactInput{
			ProjectID: localProjectID, Kind: "report", SourceCommit: resultCommit,
			TaskIDs: []string{task.ID}, ProducerSession: claim.Session.ID, Data: bytes.NewReader(stdout),
		})
	}
	if stderrErr == nil && bytes.Equal(stdout, stderr) {
		stderrArtifact = stdoutArtifact
		stderrErr = stdoutErr
	} else if stderrErr == nil {
		stderrArtifact, stderrErr = artifacts.Put(ctx, model.ArtifactInput{
			ProjectID: localProjectID, Kind: "report", SourceCommit: resultCommit,
			TaskIDs: []string{task.ID}, ProducerSession: claim.Session.ID, Data: bytes.NewReader(stderr),
		})
	}
	if ctx.Err() == nil {
		if evidenceErr := r.recordRunEvidence(ctx, runID, task.ID, request.Adapter, probe.Version, baseCommit, resultCommit, result); evidenceErr != nil {
			runErr = evidenceErr
		}
	}
	success := runErr == nil && inspectErr == nil && stdoutErr == nil && stderrErr == nil &&
		result.Status == adapter.StatusSuccess && result.ExitCode == 0
	finishStatus := "failed"
	if success {
		finishStatus = "success"
	} else if result.TimedOut {
		finishStatus = "timeout"
	} else if result.Cancelled {
		finishStatus = "cancelled"
	} else if result.Status == adapter.StatusBlocked {
		finishStatus = "blocked"
	}
	exitStatus := result.ExitCode
	finishErr := r.store.FinishRun(context.Background(), model.RunFinish{
		ID: runID, Status: finishStatus, ResultCommit: resultCommit, EndedAt: time.Now().UTC(),
		ExitStatus: &exitStatus, StdoutArtifactID: stdoutArtifact.ID,
		StderrArtifactID: stderrArtifact.ID, ExpectedRevision: 0,
	})
	currentRevision := executionRevision
	if success && resultCommit != baseCommit {
		if err := r.store.ObserveHEAD(context.Background(), task.ID, resultCommit, currentRevision); err != nil {
			success = false
		} else {
			currentRevision++
		}
	}
	finalizeErr := r.store.FinalizeExecution(context.Background(), task.ID, claim.Session.ID, success, currentRevision)
	for _, candidate := range []error{runErr, inspectErr, stdoutErr, stderrErr, finishErr, finalizeErr} {
		if candidate != nil {
			return RunResult{}, candidate
		}
	}
	return RunResult{
		RunID: runID, TaskID: task.ID, Status: finishStatus, BaseCommit: baseCommit,
		ResultCommit: resultCommit, ExitStatus: result.ExitCode, Isolation: result.Isolation,
		StdoutArtifact: stdoutArtifact, StderrArtifact: stderrArtifact,
	}, nil
}

func (r *Runtime) sanitizeProviderOutput(ctx context.Context, payload []byte) ([]byte, error) {
	boundary, ok := r.evidenceSanitizer.(evidence.ByteSanitizer)
	if !ok {
		return nil, evidence.ErrSecretRejected
	}
	return boundary.SanitizeBytes(ctx, payload)
}

func commitTaskChanges(ctx context.Context, worktreePath, taskID string) error {
	for _, args := range [][]string{
		{"-C", worktreePath, "add", "--all"},
		{"-C", worktreePath, "commit", "-m", "chore(task): complete " + taskID},
	} {
		command := exec.CommandContext(ctx, "git", args...)
		if output, err := command.CombinedOutput(); err != nil {
			if len(output) > 4096 {
				output = output[:4096]
			}
			return fmt.Errorf("git task commit: %w: %s", err, bytes.TrimSpace(output))
		}
	}
	return nil
}

func (r *Runtime) resolveAdapter(ctx context.Context, name string, task model.Task, worktreePath string) (adapter.Adapter, error) {
	if candidate := r.adapters[name]; candidate != nil {
		return candidate, nil
	}
	switch name {
	case "codex", "gemini", "claude", "opencode":
	default:
		return nil, fmt.Errorf("%w: adapter %s is unavailable", model.ErrUnavailable, name)
	}
	binary, err := project.FindBinary(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %s CLI is missing", model.ErrUnavailable, name)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(binary); resolveErr == nil {
		binary = resolved
	}
	process := worker.New(30*time.Minute, 3*time.Second, 8<<20)
	var runner adapter.ProcessRunner = process
	if bwrapPath, lookupErr := exec.LookPath("bwrap"); lookupErr == nil {
		backend := sandbox.NewBwrap(bwrapPath)
		capability := backend.Probe(ctx)
		chosen, chooseErr := sandbox.ChooseIsolation(capability, task.Risk, true)
		if chooseErr != nil {
			return nil, chooseErr
		}
		if chosen.Level == model.IsolationBwrap {
			readOnlyBinds := []model.Bind{{Source: binary, Target: binary}}
			gitMetadata := filepath.Join(r.layout.Root, ".git")
			if info, statErr := os.Stat(gitMetadata); statErr == nil && info.IsDir() {
				readOnlyBinds = append(readOnlyBinds, model.Bind{Source: gitMetadata, Target: gitMetadata})
			}

			var extraEnv []string
			var writableTmpfs []string

			// Adapter runtime storage (.codex, .opencode, etc.) must be writable tmpfs for sqlite DBs / logs
			writableTmpfs = append(writableTmpfs,
				"/home/marshal/."+name,
				"/home/marshal/.local",
				"/home/marshal/.local/share",
				"/home/marshal/.local/share/"+name,
				"/home/marshal/.cache",
				"/home/marshal/.cache/"+name,
			)

			if home, homeErr := os.UserHomeDir(); homeErr == nil {
				// Bind config directories and essential files → /home/marshal/ (read-only)
				for _, sub := range []string{
					"auth.json", "config.toml", "config.json", "AGENTS.md",
				} {
					src := filepath.Join(home, "."+name, sub)
					tgt := "/home/marshal/." + name + "/" + sub
					if _, statErr := os.Stat(src); statErr == nil {
						readOnlyBinds = append(readOnlyBinds, model.Bind{Source: src, Target: tgt})
					}
				}

				srcConfigDir := filepath.Join(home, ".config", name)
				if _, statErr := os.Stat(srcConfigDir); statErr == nil {
					readOnlyBinds = append(readOnlyBinds, model.Bind{Source: srcConfigDir, Target: "/home/marshal/.config/" + name})
				}

				// For opencode: bind npm node_modules used by the opencode binary
				if name == "opencode" {
					for _, sub := range []string{
						filepath.Join(".opencode", "node_modules"),
						filepath.Join(".config", "opencode", "node_modules"),
					} {
						src := filepath.Join(home, sub)
						tgt := "/home/marshal/" + sub
						if _, statErr := os.Stat(src); statErr == nil {
							readOnlyBinds = append(readOnlyBinds, model.Bind{Source: src, Target: tgt})
						}
					}
				}

				// Forward XDG env so apps inside sandbox find config correctly
				extraEnv = append(extraEnv,
					"XDG_CONFIG_HOME=/home/marshal/.config",
					"XDG_DATA_HOME=/home/marshal/.local/share",
					"XDG_CACHE_HOME=/home/marshal/.cache",
				)
			}

			// Forward OLLAMA_HOST; default to localhost:11434 for local Ollama
			ollamaHost := os.Getenv("OLLAMA_HOST")
			if ollamaHost == "" {
				ollamaHost = "http://localhost:11434"
			}
			extraEnv = append(extraEnv, "OLLAMA_HOST="+ollamaHost)

			// Forward MARSHAL_OPENCODE_MODEL if set
			if m := os.Getenv("MARSHAL_OPENCODE_MODEL"); m != "" {
				extraEnv = append(extraEnv, "MARSHAL_OPENCODE_MODEL="+m)
			}

			runner = worker.NewSandboxed(process, backend, model.SandboxRequest{
				Worktree: worktreePath, NetworkAllowed: true,
				ReadOnlyBinds: readOnlyBinds,
				WritableTmpfs: writableTmpfs,
				ExtraEnv:      extraEnv,
			})
		}
	} else if _, err := sandbox.ChooseIsolation(model.IsolationCapability{}, task.Risk, true); err != nil {
		return nil, err
	}
	switch name {
	case "codex":
		return codex.New(binary, runner), nil
	case "gemini":
		return gemini.New(binary, runner), nil
	case "claude":
		return claude.New(binary, runner), nil
	case "opencode":
		return opencode.New(binary, runner), nil
	default:
		return nil, fmt.Errorf("%w: adapter %s is unavailable", model.ErrUnavailable, name)
	}
}

func loadPackVersion(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open pack version: %w", err)
	}
	defer file.Close()
	var doc versionDocument
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&doc); err != nil {
		return "", fmt.Errorf("decode pack version: %w", err)
	}
	if doc.SchemaVersion != 1 || doc.PackVersion == "" {
		return "", fmt.Errorf("%w: unsupported or incomplete pack version", model.ErrInvalid)
	}
	return doc.PackVersion, nil
}
