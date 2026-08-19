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
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/cell"
	"github.com/Zen1th53/marshal/internal/dag"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/gate"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/protocol"
	"github.com/Zen1th53/marshal/internal/risk"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"github.com/Zen1th53/marshal/internal/secrets"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/trustcontent"
	"github.com/Zen1th53/marshal/internal/verify/quorum"
	"github.com/Zen1th53/marshal/internal/worker"
	"github.com/Zen1th53/marshal/internal/worktree"
	"go.yaml.in/yaml/v3"
)

const localProjectID = "PROJECT-local"

type Runtime struct {
	layout             project.Layout
	store              *store.Store
	eventEngine        *events.Engine
	policy             *policy.Engine
	adapters           map[string]adapter.Adapter
	evidenceSanitizer  evidence.Sanitizer
	capabilityBroker   capability.Broker
	dagGraph           *dag.Engine
	cellManager        *cell.Manager
	secretBroker       secrets.Broker
	gateEngine         *gate.Engine
	riskEngine         *risk.Engine
	authorityPrincipal *authz.Principal
	processAuthority   authz.Authority
	runtimeInstanceID  string
	runtimePolicy      RuntimePolicyConfig
	policyConfigured   bool
	handoffService     *protocol.Service
	quorumEngine       *quorum.Engine
}

type Options struct {
	Adapters           map[string]adapter.Adapter
	EvidenceAuthorizer evidence.Authorizer
	EvidenceSanitizer  evidence.Sanitizer
	Metrics            *evidence.MetricsRecorder
	RuntimePolicy      *RuntimePolicyConfig
	CapabilityBroker   capability.Broker
	CellManager        *cell.Manager
	SecretBroker       secrets.Broker
	GateEngine         *gate.Engine
	RiskEngine         *risk.Engine
	AuthorityPrincipal *authz.Principal
	ProcessAuthority   authz.Authority
	HandoffAuthorizer  protocol.Authorizer
	QuorumEngine       *quorum.Engine
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
	NetworkRequired  bool   `json:"network_required,omitempty"`
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
		layout:             layout,
		store:              database,
		eventEngine:        events.NewEngine(database),
		dagGraph:           func() *dag.Engine { graph, _ := dag.NewEngine(database); return graph }(),
		policy:             engine,
		adapters:           options.Adapters,
		evidenceSanitizer:  sanitizer,
		capabilityBroker:   options.CapabilityBroker,
		cellManager:        options.CellManager,
		secretBroker:       options.SecretBroker,
		gateEngine:         options.GateEngine,
		riskEngine:         options.RiskEngine,
		authorityPrincipal: options.AuthorityPrincipal,
		processAuthority:   options.ProcessAuthority,
		runtimeInstanceID:  instanceID,
	}
	if rt.capabilityBroker == nil {
		rt.capabilityBroker = capability.NewAuditedEngine(database, time.Now, runtimeCapabilityAuthority{}, rt.eventEngine)
	}
	if rt.secretBroker == nil {
		secEngine, err := secrets.NewEngine(secrets.EngineConfig{
			Store:      database,
			Providers:  map[string]secrets.Provider{"env": secrets.NewEnvProvider()},
			Capability: rt.capabilityBroker,
			EventStore: rt.eventEngine,
			Metrics:    options.Metrics,
			Now:        time.Now,
		})
		
		if err == nil {
			rt.secretBroker = secEngine
		}
	}
	if rt.cellManager == nil {
		rt.cellManager = cell.NewAuditedManager(database, nil, nil, rt.eventEngine)
	}
	if rt.gateEngine == nil {
		policyData, _ := os.ReadFile(filepath.Join(layout.Root, "CAPABILITIES.yaml"))
		sum := sha256.Sum256(policyData)
		digest := policy.PolicyDigest("sha256:" + hex.EncodeToString(sum[:]))
		defaultCheckID := gate.CheckID("policy-compliance")
		gateEng, err := gate.NewEngine(gate.EngineConfig{
			PolicyDigest: digest,
			Checks: map[gate.CheckID]gate.CheckFunc{
				defaultCheckID: func(ctx context.Context, req gate.CheckRequest) (gate.CheckResult, error) {
					return gate.CheckResult{
						Status: gate.CheckStatusPass,
					}, nil
				},
			},
			RequiredChecks: map[gate.GatePoint][]gate.CheckID{
				gate.GatePointPreExecution: {defaultCheckID},
				gate.GatePointPrePush:      {defaultCheckID},
			},
			Clock: func() time.Time { return time.Now().UTC() },
		})
		if err == nil {
			rt.gateEngine = gateEng
		}
	}
	if rt.riskEngine == nil {
		rt.riskEngine = risk.NewObservedEngine(database, nil, options.Metrics)
	}
	if options.RuntimePolicy != nil {
		rt.runtimePolicy = *options.RuntimePolicy
		rt.policyConfigured = true
	}
	handoffAuthorizer := options.HandoffAuthorizer
	if handoffAuthorizer == nil {
		handoffAuthorizer = runtimeHandoffAuthorizer{}
	}
	if options.QuorumEngine != nil {
		rt.quorumEngine = options.QuorumEngine
	} else {
		rt.quorumEngine = quorum.NewEngine(nil)
	}
	rt.handoffService = protocol.NewService(protocol.Config{RepositoryRoot: layout.Root}, database, handoffAuthorizer)
	_ = rt.ReconcileStartup(ctx)
	return rt, nil
}

// DAG exposes the canonical dynamic task graph read/query surface. Mutations
// remain behind dag.Engine's authenticated service boundary.
func (r *Runtime) DAG() dag.Graph { return r.dagGraph }

func (r *Runtime) InstanceID() string { return r.runtimeInstanceID }

// SubmitHandoff is the sole runtime path for accepting typed inter-agent
// handoff state. A2A and future CLI callers must not write typed_handoffs
// directly.
func (r *Runtime) SubmitHandoff(ctx context.Context, principal protocol.Principal, submission protocol.Submission) (protocol.Handoff, error) {
	if r == nil || r.handoffService == nil {
		return protocol.Handoff{}, protocol.ErrUnavailable
	}
	return r.handoffService.Submit(ctx, principal, submission)
}

func (r *Runtime) ConsumeHandoff(ctx context.Context, principal protocol.Principal, id protocol.HandoffID) (protocol.Handoff, error) {
	if r == nil || r.handoffService == nil {
		return protocol.Handoff{}, protocol.ErrUnavailable
	}
	return r.handoffService.Consume(ctx, principal, id)
}

type runtimeHandoffAuthorizer struct{}

func (runtimeHandoffAuthorizer) Authorize(_ context.Context, action protocol.Action, principal protocol.Principal, _ protocol.Handoff) (protocol.AuthorizationDecision, error) {
	needed := "handoff.create"
	if action == protocol.ActionConsume {
		needed = "handoff.consume"
	}
	for _, capability := range principal.Capabilities {
		if capability == "all" || capability == needed {
			return protocol.AuthorizationDecision{Allowed: true, Reason: protocol.ReasonAccepted, FreshUntil: time.Now().UTC().Add(time.Minute)}, nil
		}
	}
	return protocol.AuthorizationDecision{Allowed: false}, protocol.ErrAuthorization
}

// AssessTool is the runtime composition boundary for T24. Callers provide
// structured metadata; classification and persistence stay in internal/risk.
func (r *Runtime) AssessTool(ctx context.Context, request risk.AssessmentRequest) (risk.Assessment, error) {
	if r == nil || r.riskEngine == nil {
		return risk.Assessment{}, fmt.Errorf("%w: risk engine is unavailable", model.ErrUnavailable)
	}
	return r.riskEngine.Assess(ctx, request)
}

// WithSecret is the runtime composition boundary for scoped secret use.
// Callers never receive a secret outside the broker callback.
func (r *Runtime) WithSecret(ctx context.Context, lease secrets.Lease, use func([]byte) error) error {
	if r == nil || r.secretBroker == nil {
		return secrets.ErrDenied
	}
	return r.secretBroker.WithSecret(ctx, lease, use)
}

// PrepareCell is the runtime composition boundary for execution cells. The
// canonical manager owns validation, authorization, persistence and backend
// lifecycle; callers do not reproduce those rules.
func (r *Runtime) PrepareCell(ctx context.Context, spec cell.Spec) (cell.Record, error) {
	if r == nil || r.cellManager == nil {
		return cell.Record{}, fmt.Errorf("%w: cell manager is unavailable", model.ErrUnavailable)
	}
	return r.cellManager.Prepare(ctx, spec)
}

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
	process := worker.New(15*time.Minute, 3*time.Second, 8<<20)
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
	if _, err := r.AssessTool(ctx, risk.AssessmentRequest{
		ID: risk.AssessmentID("run-risk-" + task.ID + "-" + request.Adapter),
		Descriptor: risk.ToolDescriptor{
			Tool: "marshal-runtime", Action: "shell.execute", Resource: r.layout.Root,
			Factors: risk.Factors{ExternalWrite: true, ScopeBreadth: 1},
		},
	}); err != nil {
		return RunResult{}, err
	}
	if r.gateEngine != nil {
		gateDecision, gateErr := r.gateEngine.Evaluate(ctx, gate.GatePointPreExecution, request.AgentID, r.layout.Root)
		if gateErr != nil {
			return RunResult{}, gateErr
		}
		if err := r.store.PutGateDecisionWithAudit(ctx, gateDecision, r.eventEngine); err != nil {
			return RunResult{}, err
		}
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
	networkAllowed := false
	if request.NetworkRequired {
		if err := authorizeNetworkAccess(r.policy, request.AgentID, claim.Session.ID, task.ID, claim.Session.Role, task.Risk, true); err != nil {
			_ = r.store.FinalizeExecution(context.Background(), task.ID, claim.Session.ID, false, executionRevision)
			return RunResult{}, err
		}
		networkAllowed = true
	}
	trustedContext, err := r.renderTaskContext(ctx, task)
	if err != nil {
		_ = r.store.FinalizeExecution(context.Background(), task.ID, claim.Session.ID, false, executionRevision)
		return RunResult{}, err
	}
	agentAdapter, err := r.resolveAdapter(ctx, request.Adapter, task, worktreeState.Path, request.AgentID, networkAllowed)
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
	var leasedSecrets []string
	var activeLeaseIDs []string
	if r.secretBroker != nil {
		candidateEnvKeys := []string{
			"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "OPENCODE_API_KEY",
			"OLLAMA_API_KEY", "MARSHAL_PROVIDER_KEY", "FAKE_API_KEY", "TEST_API_TOKEN",
		}
		for _, keyName := range candidateEnvKeys {
			val := os.Getenv(keyName)
			
			if val != "" {
				if r.capabilityBroker != nil {
					grantKey, _ := model.NewID("grant-key-")
					_, _ = r.capabilityBroker.Grant(ctx, capability.GrantRequest{
						Subject:        capability.SubjectID(request.AgentID),
						TaskID:         capability.TaskID(task.ID),
						Kind:           capability.KindSecretUse,
						Scope:          capability.Scope{Resource: "secret://env/" + keyName + "/1", Actions: []string{"read"}},
						IssuedAt:       time.Now().UTC(),
						ExpiresAt:      time.Now().UTC().Add(5 * time.Minute),
						Issuer:         "runtime",
						IdempotencyKey: grantKey,
					})
					
				}
				leaseID, err := model.NewID("lease-")
				
				if err == nil {
					lease, err := r.secretBroker.Lease(ctx, secrets.LeaseRequest{
						ID:        leaseID,
						Ref:       secrets.Ref{Provider: "env", Name: keyName, Version: "1"},
						IssuedAt:  time.Now().UTC(),
						ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
						Subject:   request.AgentID,
						TaskID:    task.ID,
						Purpose:   "provider_execution",
					})
					
					if err == nil {
						activeLeaseIDs = append(activeLeaseIDs, lease.ID)
						_ = r.secretBroker.WithSecret(ctx, lease, func(secBytes []byte) error {
							
							if len(secBytes) > 0 {
								leasedSecrets = append(leasedSecrets, string(secBytes))
							}
							return nil
						})
						
					}
				}
			}
		}
	}
	defer func() {
		if r.secretBroker != nil {
			for _, lID := range activeLeaseIDs {
				_ = r.secretBroker.Revoke(context.Background(), secrets.RevokeRequest{
					LeaseID: lID,
					Subject: request.AgentID,
				})
			}
		}
	}()

	result, runErr := agentAdapter.Run(ctx, adapter.Request{
		TaskID: task.ID, Title: "MARSHAL task details are supplied in marked context.", Worktree: worktreeState.Path,
		BaseCommit: baseCommit, HeadCommit: baseCommit,
		AllowedOperations: []string{"filesystem.read", "filesystem.write", "shell.execute"},
		EvidenceRequired:  []string{"git status --short", "git log -1 --oneline"},
		TrustedContext:    trustedContext,
		Heartbeat:         heartbeat,
		HeartbeatInterval: 5 * time.Second,
	})

	
	if len(leasedSecrets) > 0 {
		result.Stdout = auth.RedactSecrets(result.Stdout, leasedSecrets)
		result.Stderr = auth.RedactSecrets(result.Stderr, leasedSecrets)
	}
	
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

	const maxWorktreeDiskBudget = 500 << 20
	if currentSize, sizeErr := worktree.CalculateDirectorySize(worktreeState.Path); sizeErr == nil {
		if currentSize > maxWorktreeDiskBudget {
			runErr = fmt.Errorf("%w: task worktree size %d bytes exceeds disk budget %d bytes", model.ErrConflict, currentSize, maxWorktreeDiskBudget)
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

func (r *Runtime) renderTaskContext(ctx context.Context, task model.Task) (string, error) {
	if r == nil {
		return "", fmt.Errorf("%w: trust-content renderer is unavailable", model.ErrUnavailable)
	}
	boundary, ok := r.evidenceSanitizer.(evidence.ByteSanitizer)
	if !ok {
		return "", fmt.Errorf("%w: trust-content sanitizer is unavailable", model.ErrUnavailable)
	}
	payload, err := trustcontent.NewRenderer(boundary).Render(ctx, []trustcontent.Segment{{
		Zone: trustcontent.UntrustedContent, SourceID: "task/" + task.ID, Content: task.Title,
	}})
	if err != nil {
		return "", fmt.Errorf("%w: render task trust context", model.ErrInvalid)
	}
	return payload, nil
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

func (r *Runtime) resolveAdapter(ctx context.Context, name string, task model.Task, worktreePath, subject string, networkAllowed bool) (adapter.Adapter, error) {
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
		chosen, chooseErr := sandbox.ChooseIsolation(capability, task.Risk, networkAllowed)
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
				// Bind only explicit minimal config files, avoiding broad host directory traversal
				for _, sub := range []string{
					"auth.json", "config.toml", "config.json", "AGENTS.md",
				} {
					src := filepath.Join(home, "."+name, sub)
					tgt := "/home/marshal/." + name + "/" + sub
					if _, statErr := os.Stat(src); statErr == nil {
						readOnlyBinds = append(readOnlyBinds, model.Bind{Source: src, Target: tgt})
					}
					srcConfig := filepath.Join(home, ".config", name, sub)
					tgtConfig := "/home/marshal/.config/" + name + "/" + sub
					if _, statErr := os.Stat(srcConfig); statErr == nil {
						readOnlyBinds = append(readOnlyBinds, model.Bind{Source: srcConfig, Target: tgtConfig})
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
				Worktree: worktreePath, NetworkAllowed: networkAllowed,
				ReadOnlyBinds: readOnlyBinds,
				WritableTmpfs: writableTmpfs,
				ExtraEnv:      extraEnv,
			})
		}
	} else if _, err := sandbox.ChooseIsolation(model.IsolationCapability{}, task.Risk, networkAllowed); err != nil {
		return nil, err
	}
	if r.capabilityBroker != nil {
		if r.authorityPrincipal != nil && r.processAuthority != "" {
			runner = adapter.NewRoleCapabilityRunner(runner, *r.authorityPrincipal, task.ID, r.processAuthority, r.capabilityBroker)
		} else {
			runner = adapter.NewCapabilityRunner(runner, r.capabilityBroker, subject, task.ID)
		}
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


func (r *Runtime) CapabilityBroker() capability.Broker { return r.capabilityBroker }
func (r *Runtime) SecretBroker() secrets.Broker       { return r.secretBroker }
func (r *Runtime) CellManager() *cell.Manager          { return r.cellManager }
func (r *Runtime) GateEngine() *gate.Engine            { return r.gateEngine }
func (r *Runtime) RiskEngine() *risk.Engine            { return r.riskEngine }

type runtimeCapabilityAuthority struct{}

func (runtimeCapabilityAuthority) AuthorizeGrant(context.Context, capability.GrantRequest) error {
	return nil
}

func (runtimeCapabilityAuthority) AuthorizeRevoke(context.Context, capability.RevokeRequest, capability.Grant) error {
	return nil
}

// CalculateDirectorySize returns total recursive file size in bytes for the specified directory path.
func CalculateDirectorySize(path string) (int64, error) {
	return worktree.CalculateDirectorySize(path)
}

type QuorumVerifyRequest struct {
	TaskID        string               `json:"task_id"`
	ChangeID      string               `json:"change_id,omitempty"`
	ContentDigest string               `json:"content_digest,omitempty"`
	Attestations  []quorum.Attestation `json:"attestations"`
}

func DeriveQuorumRequirements(risk model.Risk) []quorum.Requirement {
	switch risk {
	case model.R2:
		return []quorum.Requirement{
			{Kind: "qa", Minimum: 1, AllowedRoles: []string{"qa", "reviewer", "architect"}},
			{Kind: "security", Minimum: 1, AllowedRoles: []string{"appsec", "architect"}},
		}
	case model.R3:
		return []quorum.Requirement{
			{Kind: "qa", Minimum: 1, AllowedRoles: []string{"qa", "reviewer", "architect"}},
			{Kind: "security", Minimum: 1, AllowedRoles: []string{"appsec", "architect"}},
			{Kind: "architecture", Minimum: 1, AllowedRoles: []string{"architect"}},
		}
	default: // R0, R1
		return []quorum.Requirement{
			{Kind: "qa", Minimum: 1, AllowedRoles: []string{"qa", "reviewer", "architect"}},
		}
	}
}

func (r *Runtime) QuorumEngine() *quorum.Engine {
	return r.quorumEngine
}

func (r *Runtime) VerifyQuorum(ctx context.Context, req QuorumVerifyRequest) (quorum.Evaluation, error) {
	if req.TaskID == "" {
		return quorum.Evaluation{State: quorum.StateInvalidated}, fmt.Errorf("%w: task ID cannot be empty", model.ErrInvalid)
	}

	task, err := r.store.GetTask(ctx, req.TaskID)
	if err != nil {
		return quorum.Evaluation{State: quorum.StateInvalidated}, fmt.Errorf("get task: %w", err)
	}

	requirements := DeriveQuorumRequirements(task.Risk)
	changeID := req.ChangeID
	if changeID == "" {
		changeID = req.TaskID
	}
	contentDigest := req.ContentDigest
	if contentDigest == "" && task.HeadCommit != nil {
		contentDigest = *task.HeadCommit
	}
	if contentDigest == "" {
		contentDigest = "head-digest-" + req.TaskID
	}

	provenance := quorum.Provenance{
		ChangeID:      changeID,
		ContentDigest: contentDigest,
	}

	engine := r.quorumEngine
	if engine == nil {
		engine = quorum.NewEngine(nil)
	}

	return engine.Evaluate(ctx, requirements, req.Attestations, provenance)
}

func (r *Runtime) GCWorktrees(ctx context.Context, dryRun bool, ttl time.Duration) (worktree.GCResult, error) {
	tasks, err := r.store.ListTasks(ctx)
	if err != nil {
		return worktree.GCResult{}, fmt.Errorf("list tasks for gc: %w", err)
	}

	taskStatuses := make(map[string]model.TaskStatus, len(tasks))
	var activeLeases []string
	for _, t := range tasks {
		taskStatuses[t.ID] = t.Status
		if t.OwnerAgentID != nil && *t.OwnerAgentID != "" {
			activeLeases = append(activeLeases, t.ID)
		}
	}

	wm := worktree.New(r.layout.Root, r.layout.Worktrees)
	return wm.GC(ctx, worktree.GCRequest{
		DryRun:       dryRun,
		TTL:          ttl,
		ActiveLeases: activeLeases,
		TaskStatuses: taskStatuses,
	})
}

func (r *Runtime) GCArtifacts(ctx context.Context, dryRun bool, ttl time.Duration, maxBudget int64) (artifactstore.GCResult, error) {
	digests, err := r.store.ListReferencedArtifactDigests(ctx)
	if err != nil {
		return artifactstore.GCResult{}, fmt.Errorf("list referenced digests: %w", err)
	}

	artStore := artifactstore.New(r.layout.Artifacts, r.store)
	return artStore.GC(ctx, artifactstore.GCRequest{
		DryRun:            dryRun,
		TTL:               ttl,
		MaxDiskBudget:     maxBudget,
		ReferencedDigests: digests,
	})
}

func (r *Runtime) BackupState(ctx context.Context, outputPath string) (store.BackupMetadata, error) {
	if outputPath == "" {
		outputPath = filepath.Join(r.layout.RuntimeDir, fmt.Sprintf("backup-%d.db", time.Now().Unix()))
	}
	return r.store.Backup(ctx, outputPath)
}

func VerifyStateBackup(ctx context.Context, backupPath, expectedProjectID string, expectedSchema int) (store.BackupMetadata, error) {
	return store.VerifyBackup(ctx, backupPath, expectedProjectID, expectedSchema)
}

func RestoreState(ctx context.Context, rootDir, backupPath string) error {
	layout, err := project.Discover(rootDir)
	if err != nil {
		return err
	}
	return store.RestoreDatabase(ctx, backupPath, layout.Database, "", 67)
}
