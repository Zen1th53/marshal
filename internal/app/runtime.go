package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/dag"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/gate"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/projectid"
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
	permissionMu           sync.Mutex
	readGrants             map[string]bool
	continuationCandidates map[string]model.MemoryRecordV2
	permissionSink         func(permission.Request)
	layout                 project.Layout
	store                  *store.Store
	eventEngine            *events.Engine
	policy                 *policy.Engine
	adapters               map[string]adapter.Adapter
	evidenceSanitizer      evidence.Sanitizer
	capabilityBroker       capability.Broker
	dagGraph               *dag.Engine
	cellManager            *cell.Manager
	secretBroker           secrets.Broker
	gateEngine             *gate.Engine
	riskEngine             *risk.Engine
	authorityPrincipal     *authz.Principal
	processAuthority       authz.Authority
	runtimeInstanceID      string
	runtimePolicy          RuntimePolicyConfig
	policyConfigured       bool
	handoffService         *protocol.Service
	memoryService          *MemoryService
	quorumEngine           *quorum.Engine
	allowProcessOnly       bool
	execService            *ExecutionService
	honeypotMu             sync.Mutex
	honeypots              map[string]*worker.Honeypot
	egressMu               sync.Mutex
	egressRuns             map[string]*runEgress
	egressAlert            func(EgressAlert) error
	taskMu                 sync.Mutex
	taskRuns               map[string]context.CancelFunc
	execMu                 sync.Mutex
	codexAppServerMu       sync.Mutex
	codexAppServerTurns    map[string]*liveCodexAppServerTurn
	codexAppServerNew      func(string, string) codexAppServerClient
	claudeStreamMu         sync.Mutex
	claudeStreamTurns      map[string]*liveClaudeStreamTurn
	claudeStreamNew        func(string, string) claudeStreamClient
	tokenManager           *auth.Manager

	// ultra is the canonical ULTRA authorization gate. It is nil when no Cloud
	// session is attached, and a nil gate answers "not entitled", so a runtime
	// without one evaluates every ULTRA envelope as unentitled.
	ultra *cloud.Gate

	// resumeRun restarts canonical execution of a resumed run; nil means
	// ExecuteRun. Tests replace it to observe the restart without executing.
	resumeRun func(runID string)
}

// AttachULTRA wires the canonical ULTRA gate into the runtime.
//
// The runtime derives entitlement from this gate rather than from whatever a
// caller puts in a DecideRequest, which is what stops a surface from asserting
// its own entitlement into a constitutional decision.
func (r *Runtime) AttachULTRA(gate *cloud.Gate) {
	if r == nil {
		return
	}
	r.ultra = gate
}

// ULTRAEntitled reports whether the runtime currently holds ULTRA authorization.
func (r *Runtime) ULTRAEntitled() bool {
	if r == nil {
		return false
	}
	return r.ultra.Entitled()
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
	// AllowProcessOnlyFallback is retained for source compatibility. Process-only
	// provider execution is no longer permitted because it cannot enforce the
	// filesystem or network boundary.
	AllowProcessOnlyFallback bool
}

type Status struct {
	Project       model.Project `json:"project"`
	SchemaVersion int           `json:"schema_version"`
	AgentCount    int           `json:"agent_count"`
	SessionCount  int           `json:"session_count"`
	TaskCount     int           `json:"task_count"`
	LeaseCount    int           `json:"lease_count"`
	Honeypot      string        `json:"honeypot"`
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
	TaskID           string `json:"task_id"`
	BlockedReason    string `json:"blocked_reason,omitempty"`
	ExpectedRevision int64  `json:"expected_revision,omitempty"`
	EnforceRevision  bool   `json:"enforce_revision,omitempty"`
}

type RunRequest struct {
	TaskID           string           `json:"task_id"`
	AgentID          string           `json:"agent_id"`
	Adapter          string           `json:"adapter"`
	Model            string           `json:"model,omitempty"`
	ExpectedRevision int64            `json:"expected_revision"`
	NetworkRequired  bool             `json:"network_required,omitempty"`
	EgressRules      []netpolicy.Rule `json:"egress_rules,omitempty"`
}

type RunResult struct {
	RunID          string                    `json:"run_id"`
	TaskID         string                    `json:"task_id"`
	SessionID      string                    `json:"session_id,omitempty"`
	Model          string                    `json:"model,omitempty"`
	RequestedModel string                    `json:"requested_model,omitempty"`
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
	if err := ensureProjectDefaults(layout.Root); err != nil {
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
	// Establish the project's identity at setup rather than waiting for the
	// first open. Setting a project up is the moment MARSHAL takes it on, so
	// it is the honest place to record which project this is — and it means
	// readiness can report a confirmed identity immediately rather than
	// "not recorded yet" until something happens to open the runtime.
	//
	// A failure here does not fail setup: the project is usable, and the
	// identity will be established on first open instead.
	_, _ = projectid.Adopt(ctx, nil, layout.Root, layout.RuntimeDir)
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
	// Admission is decided by project identity rather than by comparing paths.
	// A project that moved is the same project and is admitted; a directory
	// holding a different repository's state is refused even at an unchanged
	// path. Comparing paths got both of those backwards.
	admission, admitErr := admitProject(ctx, layout, identity)
	if admitErr != nil {
		database.Close()
		return nil, admitErr
	}
	if !admission.Admitted {
		database.Close()
		return nil, fmt.Errorf("%w: %s", model.ErrConflict, admission.Reason)
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
		layout:              layout,
		store:               database,
		eventEngine:         events.NewEngine(database),
		dagGraph:            func() *dag.Engine { graph, _ := dag.NewEngine(database); return graph }(),
		policy:              engine,
		adapters:            options.Adapters,
		evidenceSanitizer:   sanitizer,
		capabilityBroker:    options.CapabilityBroker,
		cellManager:         options.CellManager,
		secretBroker:        options.SecretBroker,
		gateEngine:          options.GateEngine,
		riskEngine:          options.RiskEngine,
		authorityPrincipal:  options.AuthorityPrincipal,
		processAuthority:    options.ProcessAuthority,
		runtimeInstanceID:   instanceID,
		tokenManager:        auth.NewManager(layout.RuntimeDir),
		codexAppServerTurns: make(map[string]*liveCodexAppServerTurn),
		claudeStreamTurns:   make(map[string]*liveClaudeStreamTurn),
		allowProcessOnly:    options.AllowProcessOnlyFallback,
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
	if rt.riskEngine == nil {
		rt.riskEngine = risk.NewObservedEngine(database, nil, options.Metrics)
	}
	if options.RuntimePolicy != nil {
		rt.runtimePolicy = *options.RuntimePolicy
		rt.policyConfigured = true
	} else if active, activeErr := database.GetActivePolicy(ctx); activeErr == nil {
		rt.runtimePolicy = RuntimePolicyConfig{PolicyID: active.Policy.ID, PolicyVersion: active.Policy.Version}
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
	rt.memoryService = NewMemoryService(database)
	rt.memoryService.propose = rt.proposeMemory
	if err := rt.memoryService.RebuildProjections(ctx, localProjectID); err != nil {
		return nil, err
	}
	if service := rt.Marshal(); service != nil {
		if err := service.RecoverPendingOperations(ctx); err != nil {
			database.Close()
			return nil, err
		}
	}
	_ = rt.ReconcileStartup(ctx)
	return rt, nil
}

// DAG exposes the canonical dynamic task graph read/query surface. Mutations
// remain behind dag.Engine's authenticated service boundary.
func (r *Runtime) DAG() dag.Graph { return r.dagGraph }

func (r *Runtime) InstanceID() string { return r.runtimeInstanceID }

// Adapter returns the configured adapter for the given name, if present.
func (r *Runtime) Adapter(name string) adapter.Adapter {
	if r == nil || r.adapters == nil {
		return nil
	}
	return r.adapters[name]
}

// ProjectID returns the canonical local project identifier that runtime records
// are written under. Callers that read project-scoped rows must use this rather
// than assuming a label, or they will query an identifier that holds no rows.
func (r *Runtime) ProjectID() string { return localProjectID }

// CodexModelPreference returns the durable operator selection for future
// governed Codex dispatches.  It is deliberately separate from a harness
// catalog's default model and from execution evidence for completed runs.
func (r *Runtime) CodexModelPreference(ctx context.Context) (model.ExecutionModelPreference, error) {
	if r == nil || r.store == nil {
		return model.ExecutionModelPreference{}, fmt.Errorf("runtime store is unavailable")
	}
	return r.store.GetExecutionModelPreference(ctx, localProjectID, "codex")
}

// SetCodexModelPreference validates the requested model against the current
// Codex adapter before durably applying a CAS-bound preference.  This is only
// a selection for future Process 05 runs; it cannot alter a current lease,
// task, plan, or historical evidence.
func (r *Runtime) SetCodexModelPreference(ctx context.Context, modelName string, expectedRevision int64) (model.ExecutionModelPreference, error) {
	if r == nil || r.store == nil {
		return model.ExecutionModelPreference{}, fmt.Errorf("runtime store is unavailable")
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: codex model is required", model.ErrInvalid)
	}
	if err := codex.ValidateDangerousFlags([]string{modelName}); err != nil {
		return model.ExecutionModelPreference{}, err
	}
	candidate := r.adapters["codex"]
	validator, ok := candidate.(interface {
		ValidateModel(context.Context, string) error
	})
	if !ok {
		// Model catalog discovery is read-only.  A normal runtime constructs
		// the sandboxed adapter only after a concrete task is admitted, so use a
		// short-lived local probe here rather than pretending that no model can
		// be selected until a task has already been claimed.
		binary, err := project.FindBinary("codex")
		if err != nil {
			return model.ExecutionModelPreference{}, fmt.Errorf("%w: codex CLI is missing", model.ErrUnavailable)
		}
		validator = codex.New(binary, worker.New(10*time.Second, 2*time.Second, 1<<20))
	}
	if err := validator.ValidateModel(ctx, modelName); err != nil {
		return model.ExecutionModelPreference{}, err
	}
	// A reasoning effort chosen for the previous model is kept only when the
	// new model's catalog advertises it too; otherwise it is cleared, never
	// carried onto a model that would reject it.
	effort := ""
	if current, err := r.CodexModelPreference(ctx); err == nil && current.Effort != "" {
		if effortValidator, err := r.codexEffortValidator(); err == nil && effortValidator.ValidateEffort(ctx, modelName, current.Effort) == nil {
			effort = current.Effort
		}
	}
	return r.store.SetExecutionModelPreference(ctx, model.ExecutionModelPreference{
		ProjectID: localProjectID,
		Adapter:   "codex",
		Model:     modelName,
		Effort:    effort,
	}, expectedRevision)
}

type codexEffortValidator interface {
	ValidateEffort(ctx context.Context, modelName, effort string) error
}

// codexEffortValidator reads reasoning efforts from the same catalog the model
// is validated against: the attached adapter, or a short-lived local probe.
func (r *Runtime) codexEffortValidator() (codexEffortValidator, error) {
	if validator, ok := r.adapters["codex"].(codexEffortValidator); ok {
		return validator, nil
	}
	binary, err := project.FindBinary("codex")
	if err != nil {
		return nil, fmt.Errorf("%w: codex CLI is missing", model.ErrUnavailable)
	}
	return codex.New(binary, worker.New(10*time.Second, 2*time.Second, 1<<20)), nil
}

// SetCodexEffortPreference records the reasoning effort future governed Codex
// runs request for the selected model. A model must be selected first, the
// effort must be one that model's catalog advertises, and the write is
// CAS-bound to the preference revision. An empty effort returns to the
// model's own default.
func (r *Runtime) SetCodexEffortPreference(ctx context.Context, effort string, expectedRevision int64) (model.ExecutionModelPreference, error) {
	if r == nil || r.store == nil {
		return model.ExecutionModelPreference{}, fmt.Errorf("runtime store is unavailable")
	}
	current, err := r.CodexModelPreference(ctx)
	if err != nil {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: select a Codex model with /model select codex <model> before setting its reasoning effort", model.ErrInvalid)
	}
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort != "" {
		validator, err := r.codexEffortValidator()
		if err != nil {
			return model.ExecutionModelPreference{}, err
		}
		if err := validator.ValidateEffort(ctx, current.Model, effort); err != nil {
			return model.ExecutionModelPreference{}, err
		}
	}
	current.Effort = effort
	return r.store.SetExecutionModelPreference(ctx, current, expectedRevision)
}

// ClaudeModelPreference returns the durable operator selection for future
// governed Claude dispatches. It is deliberately separate from a harness
// catalog's default model and from execution evidence for completed runs.
func (r *Runtime) ClaudeModelPreference(ctx context.Context) (model.ExecutionModelPreference, error) {
	if r == nil || r.store == nil {
		return model.ExecutionModelPreference{}, fmt.Errorf("runtime store is unavailable")
	}
	return r.store.GetExecutionModelPreference(ctx, localProjectID, "claude")
}

// SetClaudeModelPreference validates the requested model against the current
// Claude adapter before durably applying a CAS-bound preference. This is only
// a selection for future Process 05 runs; it cannot alter a current lease,
// task, plan, or historical evidence.
func (r *Runtime) SetClaudeModelPreference(ctx context.Context, modelName string, expectedRevision int64) (model.ExecutionModelPreference, error) {
	if r == nil || r.store == nil {
		return model.ExecutionModelPreference{}, fmt.Errorf("runtime store is unavailable")
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: claude model is required", model.ErrInvalid)
	}
	if err := claude.ValidateDangerousFlags([]string{modelName}); err != nil {
		return model.ExecutionModelPreference{}, err
	}
	candidate := r.adapters["claude"]
	validator, ok := candidate.(interface {
		ValidateModel(context.Context, string) error
	})
	if !ok {
		// Model discovery is read-only. A normal runtime constructs the
		// sandboxed adapter only after a concrete task is admitted, so use a
		// short-lived local probe rather than refusing every selection until a
		// task has already been claimed.
		binary, err := project.FindBinary("claude")
		if err != nil {
			return model.ExecutionModelPreference{}, fmt.Errorf("%w: claude CLI is missing", model.ErrUnavailable)
		}
		validator = claude.New(binary, worker.New(120*time.Second, 5*time.Second, 1<<20))
	}
	if err := validator.ValidateModel(ctx, modelName); err != nil {
		return model.ExecutionModelPreference{}, err
	}
	return r.store.SetExecutionModelPreference(ctx, model.ExecutionModelPreference{
		ProjectID: localProjectID,
		Adapter:   "claude",
		Model:     modelName,
	}, expectedRevision)
}

// InstallProjectCodexSkill installs one digest-bound skill from this exact
// runtime project. It is intentionally local-only: the caller cannot supply a
// URL, a filesystem source, or a command. The successful mutation is audited
// before success is returned.
func (r *Runtime) InstallProjectCodexSkill(ctx context.Context, name, expectedDigest string) (string, error) {
	if r == nil || r.store == nil {
		return "", fmt.Errorf("runtime store is unavailable")
	}
	if err := r.checkCodexSkillName(name); err != nil {
		return "", publicSkillError(err)
	}
	digest, err := codex.InstallProjectSkill(r.layout.Root, "", name, expectedDigest)
	if err != nil {
		return "", publicSkillError(err)
	}
	eventID, err := model.NewID("EVENT-")
	if err != nil {
		return "", publicSkillError(err)
	}
	if err := r.store.AppendEvent(ctx, nil, model.Event{
		ID: eventID, Type: "CODEX_PROJECT_SKILL_INSTALLED", ProjectID: localProjectID,
		Timestamp: time.Now().UTC(), AggregateRevision: 0,
		Data: map[string]any{"skill": name, "digest": digest},
	}); err != nil {
		// The filesystem mutation cannot be committed in the SQLite event
		// transaction. Compensate it before returning failure so an audit outage
		// never leaves an unaudited installed skill behind.
		if rollbackErr := codex.RemoveInstalledProjectSkill("", name, digest); rollbackErr != nil {
			return "", fmt.Errorf("audit Codex skill installation: %w (compensating removal failed: %v)", publicSkillError(err), publicSkillError(rollbackErr))
		}
		return "", fmt.Errorf("audit Codex skill installation: %w; installation was reverted", publicSkillError(err))
	}
	return digest, nil
}

func (r *Runtime) PreviewProjectCodexSkill(name string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("runtime is unavailable")
	}
	if err := r.checkCodexSkillName(name); err != nil {
		return "", publicSkillError(err)
	}
	digest, err := codex.PreviewProjectSkill(r.layout.Root, name)
	if err != nil {
		return "", publicSkillError(err)
	}
	if err := codex.CheckProjectSkillInstallDestination("", name); err != nil {
		return "", publicSkillError(err)
	}
	return digest, nil
}

// ProjectCodexSkills retains every discovered source. Only an exact project
// candidate accepted by the canonical preview service is installable.
func (r *Runtime) ProjectCodexSkills() ([]codex.SkillInfo, error) {
	if r == nil {
		return nil, fmt.Errorf("runtime is unavailable")
	}
	skills, err := codex.DiscoverLocalSkills(r.layout.Root)
	if err != nil {
		return skills, err
	}
	for i := range skills {
		skill := &skills[i]
		if skill.SourceType == "project-agents" {
			_, previewErr := r.PreviewProjectCodexSkill(skill.Name)
			skill.Installable = previewErr == nil
		}
	}
	return skills, nil
}

func (r *Runtime) checkCodexSkillName(name string) error {
	skills, err := codex.DiscoverLocalSkills(r.layout.Root)
	if err != nil {
		return err
	}
	var sources []string
	for _, skill := range skills {
		if skill.Name == name {
			sources = append(sources, skill.SourceType)
		}
	}
	if len(sources) > 1 {
		return fmt.Errorf("%w: ambiguous skill %q; sources: %s", model.ErrConflict, name, strings.Join(sources, ", "))
	}
	if len(sources) == 1 && sources[0] != "project-agents" {
		return fmt.Errorf("%w: skill %q from %s is not installable", model.ErrInvalid, name, sources[0])
	}
	return nil
}

// publicSkillError preserves admission semantics without exporting local paths.
func publicSkillError(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return fmt.Errorf("project-agents skill %s failed: %w", pathErr.Op, pathErr.Err)
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return fmt.Errorf("project-agents skill %s failed: %w", linkErr.Op, linkErr.Err)
	}
	return err
}

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

// CompileAndSubmitHandoff compiles governed canonical task memory and routes
// the resulting provider-neutral reference packet through the existing typed,
// authenticated, durable handoff service. Raw transcript/context content is
// deliberately not duplicated into the handoff row.
func (r *Runtime) CompileAndSubmitHandoff(ctx context.Context, principal authz.Principal, request HandoffCompileRequest) (protocol.Handoff, error) {
	if r == nil || r.memoryService == nil || r.handoffService == nil {
		return protocol.Handoff{}, protocol.ErrUnavailable
	}
	bundle, err := r.memoryService.CompileHandoff(ctx, principal, request)
	if err != nil {
		return protocol.Handoff{}, err
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		return protocol.Handoff{}, protocol.ErrInvalid
	}
	sum := sha256.Sum256(raw)
	claims := map[string]string{
		"goal":               bundle.TaskDefinition.Title,
		"status":             string(bundle.TaskDefinition.Status),
		"memory_ids":         strings.Join(bundle.MemoryIDs, ","),
		"working_slot_count": fmt.Sprintf("%d", len(bundle.WorkingSlots)),
	}
	for key, value := range map[string]string{
		"repository_head":   request.CurrentHead,
		"repository_branch": request.CurrentBranch,
		"diff_digest":       request.DiffHash,
	} {
		if strings.TrimSpace(value) != "" {
			claims[key] = value
		}
	}
	for key, value := range claims {
		if strings.TrimSpace(value) == "" {
			delete(claims, key)
		}
	}
	evidenceIDs := make([]protocol.EvidenceID, 0, len(bundle.EvidenceIDs))
	for _, id := range bundle.EvidenceIDs {
		evidenceIDs = append(evidenceIDs, protocol.EvidenceID(id))
	}
	handoffPrincipal := protocol.Principal{
		ID: principal.ID, Role: protocol.Role(strings.ToLower(principal.Role.Name)),
		Capabilities: append([]string(nil), principal.Role.Capabilities...),
	}
	handoff := protocol.Handoff{
		ID: protocol.HandoffID(bundle.BundleID), Version: protocol.Version1,
		TaskID: protocol.TaskID(bundle.TaskID), FromAgent: principal.ID,
		ToRole: protocol.Role(request.TargetRole), Claims: claims,
		EvidenceIDs: evidenceIDs, ChangedFiles: append([]string(nil), bundle.ChangedFiles...),
		ContextDigest: "sha256:" + hex.EncodeToString(sum[:]),
	}
	return r.handoffService.Submit(ctx, handoffPrincipal, protocol.Submission{
		IdempotencyKey: bundle.BundleID, Handoff: handoff,
	})
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

func (r *Runtime) Close() error {
	if r != nil {
		r.egressMu.Lock()
		for id, scope := range r.egressRuns {
			if scope.stopWatch != nil {
				scope.stopWatch()
				<-scope.watchDone
			}
			if scope.proxy != nil {
				_ = scope.proxy.Close()
			}
			if scope.socket != "" {
				_ = os.RemoveAll(filepath.Dir(scope.socket))
			}
			delete(r.egressRuns, id)
		}
		r.egressMu.Unlock()
		r.codexAppServerMu.Lock()
		for key, turn := range r.codexAppServerTurns {
			if turn != nil && turn.client != nil {
				if turn.cancel != nil {
					turn.cancel()
				}
				_ = turn.client.Close()
			}
			delete(r.codexAppServerTurns, key)
		}
		r.codexAppServerMu.Unlock()
		// A live Claude turn owns a local child process; leaving it running
		// after the runtime closes would orphan a governed provider session.
		r.claudeStreamMu.Lock()
		for key, turn := range r.claudeStreamTurns {
			if turn != nil && turn.client != nil {
				if turn.cancel != nil {
					turn.cancel()
				}
				_ = turn.client.Close()
			}
			delete(r.claudeStreamTurns, key)
		}
		r.claudeStreamMu.Unlock()
		r.honeypotMu.Lock()
		for _, trap := range r.honeypots {
			_ = trap.Close()
		}
		r.honeypotMu.Unlock()
		if r.store != nil {
			return r.store.Close()
		}
	}
	return nil
}

func (r *Runtime) ReconcileStartup(ctx context.Context) error {
	// 1. Reconcile database orphans (dead worker runs, stale sessions, expired leases)
	if _, err := r.store.ReconcileStartupOrphans(ctx); err != nil {
		return err
	}

	// 2. Reconcile remaining tasks
	_, err := r.releaseStaleLeases(ctx)
	return err
}

// releaseStaleLeases returns tasks whose lease has expired to the pool and
// reports how many were reclaimed.
//
// The operation is idempotent: a task whose lease has already been released no
// longer has an active one, so a repeated or crashed startup reclaims it once
// and then finds nothing to do. That is what makes it safe to run on every
// open, and what stops a restart from double-counting the recovery it reports.
func (r *Runtime) releaseStaleLeases(ctx context.Context) (int, error) {
	tasks, err := r.store.ListTasks(ctx)
	if err != nil {
		return 0, err
	}
	reclaimed := 0
	for _, task := range tasks {
		if task.Status != model.TaskWorking && task.Status != model.TaskClaimed {
			continue
		}
		active, activeErr := r.store.ActiveLease(ctx, task.ID)
		if activeErr != nil || !active.Lease.ExpiresAt.Before(time.Now().UTC()) {
			continue
		}
		if releaseErr := r.store.ReleaseTask(ctx, model.ReleaseRequest{
			TaskID:           task.ID,
			LeaseID:          active.Lease.ID,
			SessionID:        active.Lease.SessionID,
			AgentID:          active.AgentID,
			ExpectedRevision: active.TaskRevision,
			BlockedReason:    "reconciled stale lease from previous daemon instance",
		}); releaseErr == nil {
			reclaimed++
		}
	}
	return reclaimed, nil
}

func (r *Runtime) CancelTask(ctx context.Context, taskID string) error {
	return r.cancelTask(ctx, taskID, -1)
}

// CancelTaskExpected cancels only the exact task revision the caller reviewed.
func (r *Runtime) CancelTaskExpected(ctx context.Context, taskID string, expectedRevision int64) error {
	if expectedRevision < 0 {
		return fmt.Errorf("%w: expected task revision is required", model.ErrInvalid)
	}
	return r.cancelTask(ctx, taskID, expectedRevision)
}

func (r *Runtime) cancelTask(ctx context.Context, taskID string, expectedRevision int64) error {
	task, err := r.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	// A retry of the same cancellation after a crash may carry the revision
	// that existed before the first attempt completed.  A terminal cancellation
	// is already the requested durable outcome, so acknowledge it before the
	// freshness check.  For every non-terminal state the revision remains an
	// exact CAS precondition.
	if task.Status == model.TaskCancelled {
		return nil
	}
	if expectedRevision >= 0 && task.Revision != expectedRevision {
		return fmt.Errorf("%w: task %s moved from revision %d to %d",
			model.ErrConflict, taskID, expectedRevision, task.Revision)
	}
	active, activeErr := r.store.ActiveLease(ctx, taskID)
	if activeErr == nil {
		if err := r.store.ReleaseTask(ctx, model.ReleaseRequest{
			TaskID:           taskID,
			LeaseID:          active.Lease.ID,
			SessionID:        active.Lease.SessionID,
			AgentID:          active.AgentID,
			ExpectedRevision: active.TaskRevision,
			BlockedReason:    "task cancelled by supervisor",
		}); err != nil {
			return err
		}
		task, err = r.store.GetTask(ctx, taskID)
		if err != nil {
			return err
		}
	}
	_, err = r.store.TransitionTask(ctx, model.TaskTransitionRequest{
		TaskID:           task.ID,
		FromStatus:       task.Status,
		ToStatus:         model.TaskCancelled,
		ExpectedRevision: task.Revision,
		ActorRole:        model.RoleOrchestrator,
	})
	return err
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
	status := Status{Project: projectIdentity, SchemaVersion: version, Honeypot: r.honeypotStatus()}
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
	if request.EnforceRevision && active.TaskRevision != request.ExpectedRevision {
		return fmt.Errorf("%w: task %s moved from revision %d to %d",
			model.ErrConflict, request.TaskID, request.ExpectedRevision, active.TaskRevision)
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
		var err error
		request.Command, err = DefaultVerificationCommand(r.layout.Root)
		if err != nil {
			return VerifyResult{}, err
		}
	}
	if r.policyConfigured {
		if err := r.authorizeRuntime(ctx, "verification", "", "", policy.Action("verify"), policy.Resource(request.Command[0])); err != nil {
			return VerifyResult{}, err
		}
	} else {
		resolved, err := resolveBaselineVerificationCommand(request.Command)
		if err != nil {
			return VerifyResult{}, err
		}
		request.Command = resolved
	}
	result, err := worker.RunVerification(ctx, r.layout.Root, request.Command, 15*time.Minute, 8<<20)
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

// ReviewCurrentCommitWithCodex runs one fixed, read-only native Codex review
// through the canonical verification authority. Unlike a generic command
// field, callers cannot inject a prompt, path, config override, uncommitted
// state, or apply operation: the target is the runtime's exact checked-out
// commit.
func (r *Runtime) ReviewCurrentCommitWithCodex(ctx context.Context) (VerifyResult, error) {
	if r == nil {
		return VerifyResult{}, fmt.Errorf("%w: current commit is unavailable", model.ErrUnavailable)
	}
	// HEAD is mutable outside a long-running daemon. Re-discover it at the
	// canonical execution boundary instead of reviewing the daemon-start value.
	live, err := project.Discover(r.layout.Root)
	if err != nil || strings.TrimSpace(live.HEAD) == "" {
		return VerifyResult{}, fmt.Errorf("%w: current commit is unavailable", model.ErrUnavailable)
	}
	result, err := r.Verify(ctx, VerifyRequest{Command: []string{"codex", "review", "--commit", live.HEAD}})
	// Verify's general-purpose result retains the runtime-open commit for
	// legacy callers. This fixed review is bound to the freshly discovered
	// commit, so report that exact identifier instead.
	result.Commit = live.HEAD
	return result, err
}

func resolveBaselineVerificationCommand(command []string) ([]string, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("%w: verification command is empty", model.ErrInvalid)
	}
	name := filepath.Base(command[0])
	args := command[1:]
	var candidates []string
	switch name {
	case "git":
		if len(args) > 0 {
			switch args[0] {
			case "status", "diff", "log", "show", "rev-parse":
				// Repository configuration must not launch external helpers.
				if args[0] == "diff" || args[0] == "log" || args[0] == "show" {
					safeFlags := []string{"--no-ext-diff", "--no-textconv"}
					if args[0] != "diff" {
						safeFlags = append(safeFlags, "--no-show-signature")
					}
					end := len(args)
					for i, arg := range args {
						if arg == "--" {
							end = i
							break
						}
						if arg == "--remerge-diff" || arg == "--diff-merges=remerge" || arg == "--diff-merges=r" ||
							(arg == "--diff-merges" && i+1 < len(args) && (args[i+1] == "remerge" || args[i+1] == "r")) {
							return nil, fmt.Errorf("%w: external merge drivers require an active runtime policy", model.ErrPolicyDenied)
						}
					}
					args = append(append(append([]string(nil), args[:end]...), safeFlags...), args[end:]...)
				}
				args = append([]string{"-c", "core.fsmonitor=false", "-c", "log.diffMerges=separate"}, args...)
				candidates = []string{"/usr/bin/git", "/bin/git"}
				if p, err := exec.LookPath("git"); err == nil {
					candidates = append(candidates, p)
				}
			}
		}
	case "go":
		if len(args) > 0 && (args[0] == "test" || args[0] == "vet") {
			for _, arg := range args[1:] {
				if arg == "-args" || arg == "--args" {
					break
				}
				if !strings.HasPrefix(arg, "-") {
					continue
				}
				flag := strings.SplitN(strings.TrimLeft(arg, "-"), "=", 2)[0]
				if flag == "exec" || flag == "toolexec" || flag == "vettool" {
					return nil, fmt.Errorf("%w: external verification tools require an active runtime policy", model.ErrPolicyDenied)
				}
			}
			candidates = []string{"/usr/local/go/bin/go", "/usr/bin/go"}
			if p, err := exec.LookPath("go"); err == nil {
				candidates = append(candidates, p)
			}
		}
	case "python", "python3":
		if len(args) >= 2 && filepath.Clean(args[0]) == "conformance/runner.py" && args[1] == "validate-pack" {
			candidates = []string{"/usr/bin/python3", "/usr/local/bin/python3", "/usr/local/bin/python"}
			if p, err := exec.LookPath("python3"); err == nil {
				candidates = append(candidates, p)
			}
			if p, err := exec.LookPath("python"); err == nil {
				candidates = append(candidates, p)
			}
		}
	case "codex":
		// Codex review is admitted only for an exact commit. In particular do
		// not allow --uncommitted, a custom prompt, --config, or apply-like
		// flags through this baseline verification path.
		if len(args) == 3 && args[0] == "review" && args[1] == "--commit" && isCommitIdentifier(args[2]) {
			if p, err := exec.LookPath("codex"); err == nil {
				candidates = append(candidates, p)
			}
		}
	}
	for _, candidate := range candidates {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o022 == 0 {
			result := append([]string{resolved}, args...)
			return result, nil
		}
	}
	return nil, fmt.Errorf("%w: command %q requires an active runtime policy or trusted system executable", model.ErrPolicyDenied, strings.Join(command, " "))
}

func isCommitIdentifier(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func (r *Runtime) Run(ctx context.Context, request RunRequest) (finalResult RunResult, finalErr error) {
	var settle func() error
	var admissionErr error
	ctx, settle, admissionErr = r.superviseTask(ctx, request.TaskID)
	if admissionErr != nil {
		return RunResult{}, admissionErr
	}
	defer func() {
		if err := settle(); err != nil && finalErr == nil {
			finalErr = err
		}
	}()

	if request.Adapter == "" {
		request.Adapter = "codex"
	}
	task, err := r.store.GetTask(ctx, request.TaskID)
	if err != nil {
		return RunResult{}, err
	}
	// Supervision excludes another live dispatch. On failure, release only this
	// dispatch's lease, including an assignment made before Run was called.
	var dispatchSession string
	if task.Status == model.TaskClaimed {
		active, err := r.store.ActiveLease(ctx, task.ID)
		if err != nil {
			return RunResult{}, err
		}
		if active.AgentID == request.AgentID {
			dispatchSession = active.Lease.SessionID
		}
	}
	defer func() {
		if finalErr == nil || dispatchSession == "" {
			return
		}
		cleanup := context.Background()
		active, err := r.store.ActiveLease(cleanup, task.ID)
		if errors.Is(err, model.ErrNotFound) {
			return // execution finalization already released it
		}
		if err != nil {
			finalErr = errors.Join(finalErr, err)
			return
		}
		if active.Lease.SessionID != dispatchSession || active.AgentID != request.AgentID {
			return
		}
		current, err := r.store.GetTask(cleanup, task.ID)
		if err != nil {
			finalErr = errors.Join(finalErr, err)
			return
		}
		if current.ControlState != "" {
			return // operator control is settled by the supervisor
		}
		finalErr = errors.Join(finalErr, r.Release(cleanup, ReleaseRequest{
			TaskID: task.ID, ExpectedRevision: active.TaskRevision, EnforceRevision: true,
		}))
	}()

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
	// Network policy is a prerequisite, not an after-claim cleanup. Evaluate
	// its static, deny-by-default portion before adapter/provider selection so
	// a malformed egress request cannot be masked by an unrelated provider
	// configuration error or create a lease first.
	if request.NetworkRequired {
		if len(request.EgressRules) == 0 {
			agent, err := r.store.GetAgent(ctx, request.AgentID)
			if err != nil || agent.Status == model.AgentDisabled || agent.ModelProvider != request.Adapter {
				return RunResult{}, fmt.Errorf("%w: provider default requires an enabled agent bound to the selected provider", model.ErrPolicyDenied)
			}
		}
		if len(request.EgressRules) == 0 && r.adapters[request.Adapter] != nil {
			return RunResult{}, fmt.Errorf("%w: network access requires an explicit egress allowlist", model.ErrPolicyDenied)
		}
		if r.adapters[request.Adapter] != nil {
			return RunResult{}, netpolicy.ErrEnforcementUnavailable
		}
		endpoint, err := providerEndpoint(request.Adapter, request.Model)
		if err != nil {
			return RunResult{}, err
		}
		// Caller/model-supplied rules are never grants. Only the default API
		// endpoint can be admitted before a live operator-controlled run exists.
		for _, rule := range request.EgressRules {
			if err := rule.Validate(); err != nil {
				return RunResult{}, model.ErrPolicyDenied
			}
			for _, port := range rule.Ports {
				exact, err := netpolicy.Endpoint(net.JoinHostPort(rule.HostPattern, strconv.Itoa(port)))
				if err != nil || exact != endpoint || rule.Protocol != netpolicy.ProtocolTCP || rule.Action != netpolicy.ActionAllow {
					return RunResult{}, model.ErrPolicyDenied
				}
			}
		}
	}

	// Adapter identity is part of the principal binding, not a cosmetic UI
	// choice. It intentionally follows constitutional/gate/network admission:
	// the earlier gates must retain their truthful refusal reason and no claim
	// has been acquired at this point.
	// Codex and Claude are both native CLI providers whose runs must be bound
	// to a principal registered for that exact provider. A role label or an
	// agent bound to a different provider is not an authority for this one.
	switch request.Adapter {
	case "codex", "claude":
		agent, agentErr := r.store.GetAgent(ctx, request.AgentID)
		if agentErr != nil {
			return RunResult{}, fmt.Errorf("resolve %s execution agent: %w", request.Adapter, agentErr)
		}
		if agent.Status == model.AgentDisabled || agent.ModelProvider != request.Adapter {
			return RunResult{}, fmt.Errorf("%w: %s adapter requires an enabled agent bound to provider %s", model.ErrInvalid, request.Adapter, request.Adapter)
		}
	}
	if request.Model != "" && (request.Adapter == "codex" || request.Adapter == "claude") {
		// Each provider spells its own bypass flags, so the refusal list is
		// provider-specific rather than shared.
		var flagErr error
		if request.Adapter == "codex" {
			flagErr = codex.ValidateDangerousFlags([]string{request.Model})
		} else {
			flagErr = claude.ValidateDangerousFlags([]string{request.Model})
		}
		if flagErr != nil {
			return RunResult{}, flagErr
		}
		if candidate, ok := r.adapters[request.Adapter].(interface {
			ValidateModel(context.Context, string) error
		}); ok {
			if err := candidate.ValidateModel(ctx, request.Model); err != nil {
				return RunResult{}, err
			}

		}
	}
	claim, err := r.claimForRun(ctx, task, request)
	if err != nil {
		return RunResult{}, err
	}
	claimedRevision := task.Revision + 1
	if task.Status == model.TaskClaimed {
		claimedRevision = task.Revision
	}
	dispatchSession = claim.Session.ID

	input := model.PolicyInput{
		AgentID: request.AgentID, SessionID: claim.Session.ID, Role: claim.Session.Role,
		TaskID: task.ID, Risk: task.Risk, Operation: model.ShellExecute,
		Target: r.layout.Root, TaskOwned: true, TargetInScope: true, Required: true,
	}
	if err := policy.Enforce(r.policy, input, func() error { return nil }); err != nil {
		return RunResult{}, err
	}
	var baseCommit string
	if task.BaseCommit != nil {
		baseCommit = *task.BaseCommit
	} else {
		// The target branch can advance while a daemon remains running,
		// independently of the branch checked out in the operator's worktree.
		project, projectErr := r.store.Project(ctx)
		if projectErr != nil {
			return RunResult{}, fmt.Errorf("resolve task target: %w", projectErr)
		}
		if project.DefaultBranch == "" {
			return RunResult{}, errors.New("task target branch is missing")
		}
		baseCommit, err = gitMarshal(ctx, r.layout.Root, "rev-parse", "--verify", "refs/heads/"+project.DefaultBranch+"^{commit}")
		if err != nil {
			return RunResult{}, fmt.Errorf("resolve task base: %w", err)
		}
	}
	branch := "marshal/" + task.ID
	worktreeManager := worktree.New(r.layout.Root, r.layout.Worktrees)
	worktreeState, err := worktreeManager.Prepare(ctx, model.WorktreeRequest{
		TaskID: task.ID, Branch: branch, BaseCommit: baseCommit,
	})
	if err != nil {
		return RunResult{}, err
	}
	if err := r.store.BeginExecution(ctx, task.ID, claim.Session.ID, request.AgentID,
		branch, worktreeState.Path, baseCommit, claimedRevision); err != nil {
		return RunResult{}, err
	}
	executionRevision := claimedRevision + 1
	runID, err := model.NewID("RUN-")
	if err != nil {
		return RunResult{}, err
	}
	proxySocket, closeEgress, err := r.startProviderEgress(ctx, runID, "", request.Adapter, request.Model, task, request.AgentID, claim.Session.ID, claim.Session.Role)
	if err != nil {
		_ = r.store.FinalizeExecution(context.Background(), task.ID, claim.Session.ID, false, executionRevision)
		return RunResult{}, err
	}
	defer closeEgress()
	trustedContext, err := r.renderTaskContext(ctx, task)
	if err != nil {
		_ = r.store.FinalizeExecution(context.Background(), task.ID, claim.Session.ID, false, executionRevision)
		return RunResult{}, err
	}

	memoryPrincipal := authz.Principal{ID: request.AgentID, Role: authz.Role{Name: "developer", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	worktreeID := strings.Join([]string{task.ID, request.AgentID, claim.Session.ID}, ":")
	fingerprintQuery := strings.Join([]string{task.ID, task.Title, r.layout.Branch, r.layout.HEAD, branch, worktreeState.HEAD, worktreeID, request.AgentID, request.Adapter, request.Model, string(task.Risk)}, " ")
	recall, err := r.memoryService.Recall(ctx, memoryPrincipal, RecallRequest{
		ProjectID: localProjectID, Query: fingerprintQuery,
		AllowedScopeIDs: []string{localProjectID, task.ID, request.AgentID, branch},
		CurrentHead:     worktreeState.HEAD, CanonicalHead: r.layout.HEAD, CurrentBranch: branch,
		CurrentWorktreeID: worktreeID, MaxRecords: 8, MaxBytes: 12 << 10,
		RunID: runID, TaskID: task.ID, Provider: request.Adapter,
	})
	if err != nil {
		_ = r.store.FinalizeExecution(context.Background(), task.ID, claim.Session.ID, false, executionRevision)
		return RunResult{}, fmt.Errorf("automatic memory recall: %w", err)
	}
	trustedContext += "\n" + recall.Context
	agentAdapter, shellExecGrant, err := r.resolveAdapter(ctx, request.Adapter, task, worktreeState.Path, request.AgentID, proxySocket != "", request.Model, proxySocket)
	if err != nil {
		_ = r.store.FinalizeExecution(context.Background(), task.ID, claim.Session.ID, false, executionRevision)
		return RunResult{}, err
	}
	if shellExecGrant != "" && r.capabilityBroker != nil {
		defer func() {
			_ = r.capabilityBroker.Revoke(context.Background(), capability.RevokeRequest{
				GrantID: shellExecGrant, Actor: capability.SubjectID("runtime"),
			})
		}()
	}
	probe, err := agentAdapter.Probe(ctx)
	if err != nil {
		_ = r.store.FinalizeExecution(context.Background(), task.ID, claim.Session.ID, false, executionRevision)
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
		Model:      request.Model,
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
				if err := ensureNoSecretsInWorktree(ctx, worktreeState.Path, leasedSecrets); err != nil {
					return err
				}
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
	var runEvidenceIDs []string
	if ctx.Err() == nil {
		if evidenceErr := r.recordRunEvidence(ctx, runID, task.ID, request.Adapter, probe.Version, baseCommit, resultCommit, request.Model, result); evidenceErr != nil {
			runErr = evidenceErr
		} else {
			runEvidenceIDs = []string{"EVIDENCE-RUN-" + runID + "-COMMAND", "EVIDENCE-RUN-" + runID + "-OUTPUT", "EVIDENCE-RUN-" + runID + "-ENV"}
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
	var captureErr error
	if finishErr == nil && len(runEvidenceIDs) > 0 {
		failureReason := ""
		retryCondition := ""
		if !success {
			failureReason = finishStatus
			if runErr != nil {
				failureReason = runErr.Error()
			}
			switch finishStatus {
			case "timeout":
				retryCondition = "retry only after the execution time budget or approach changes"
			case "blocked":
				retryCondition = "retry only after the blocking policy or capability changes"
			case "cancelled":
				retryCondition = "retry only after an operator submits a new run"
			default:
				retryCondition = "retry only after the recorded failure evidence is reviewed"
			}
		}
		errorDigest := sha256.Sum256(append([]byte(finishStatus+":"), stderr...))
		var capturedOutcome model.MemoryRecordV2
		capturedOutcome, captureErr = r.memoryService.CaptureOutcome(context.Background(), OutcomeCaptureRequest{
			ProjectID: localProjectID, TaskID: task.ID, TaskTitle: task.Title, RunID: runID, SessionID: claim.Session.ID,
			AgentID: request.AgentID, Provider: request.Adapter, Status: finishStatus,
			ExitStatus: result.ExitCode, BaseCommit: baseCommit, HeadCommit: resultCommit, Branch: branch,
			WorktreeID:  worktreeID,
			EvidenceIDs: runEvidenceIDs, ErrorSignature: "sha256:" + hex.EncodeToString(errorDigest[:]),
			FailureReason: failureReason, RetryCondition: retryCondition,
			Environment: map[string]string{"adapter_version": probe.Version, "isolation": fmt.Sprint(result.Isolation)},
		})
		if captureErr == nil {
			_, captureErr = r.memoryService.ProposeOutcomeConsolidation(context.Background(), memoryPrincipal, capturedOutcome, task.Title)
		}
	}
	currentRevision := executionRevision
	if success && resultCommit != baseCommit {
		if err := r.store.ObserveHEAD(context.Background(), task.ID, resultCommit, currentRevision); err != nil {
			success = false
		} else {
			currentRevision++
		}
	}
	finalizeErr := r.store.FinalizeExecution(context.Background(), task.ID, claim.Session.ID, success, currentRevision)
	for _, candidate := range []error{runErr, inspectErr, stdoutErr, stderrErr, finishErr, captureErr, finalizeErr} {
		if candidate != nil {
			return RunResult{}, candidate
		}
	}
	sessionID := result.SessionID
	if sessionID == "" {
		sessionID = claim.Session.ID
	}
	return RunResult{
		RunID:          runID,
		TaskID:         task.ID,
		SessionID:      sessionID,
		Model:          result.Model,
		RequestedModel: request.Model,
		Status:         finishStatus,
		BaseCommit:     baseCommit,
		ResultCommit:   resultCommit,
		ExitStatus:     result.ExitCode,
		Isolation:      result.Isolation,
		StdoutArtifact: stdoutArtifact,
		StderrArtifact: stderrArtifact,
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
		{"add", "--all"},
		{"commit", "-m", "chore(task): complete " + taskID},
	} {
		command, err := hostgit.Command(ctx, worktreePath, args...)
		if err != nil {
			return err
		}
		if output, err := command.CombinedOutput(); err != nil {
			if len(output) > 4096 {
				output = output[:4096]
			}
			return fmt.Errorf("git task commit: %w: %s", err, bytes.TrimSpace(output))
		}
	}
	return nil
}

func ensureNoSecretsInWorktree(ctx context.Context, worktreePath string, secrets []string) error {
	var paths []string
	for _, args := range [][]string{
		{"diff", "--name-only", "-z", "HEAD"},
		{"ls-files", "--others", "--exclude-standard", "-z"},
	} {
		command, err := hostgit.Command(ctx, worktreePath, args...)
		if err != nil {
			return err
		}
		output, err := command.Output()
		if err != nil {
			return fmt.Errorf("%w: enumerate changed files", model.ErrUnavailable)
		}
		for _, item := range bytes.Split(output, []byte{0}) {
			if len(item) > 0 {
				paths = append(paths, string(item))
			}
		}
	}
	seen := make(map[string]struct{}, len(paths))
	for _, relative := range paths {
		if _, ok := seen[relative]; ok {
			continue
		}
		seen[relative] = struct{}{}
		lower := strings.ToLower(filepath.ToSlash(relative))
		for _, forbidden := range []string{"auth.json", ".env", ".netrc", ".git-credentials", "credentials.json", "service-account.json"} {
			if strings.Contains(lower, forbidden) {
				return fmt.Errorf("%w: changed credential-bearing path %q", evidence.ErrSecretRejected, relative)
			}
		}
		absolute := filepath.Join(worktreePath, relative)
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			continue
		}
		within, err := filepath.Rel(worktreePath, resolved)
		if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%w: changed path escapes worktree: %q", model.ErrInvalid, relative)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > 8<<20 {
			return fmt.Errorf("%w: changed file %q exceeds secret-inspection limit", evidence.ErrSecretRejected, relative)
		}
		content, err := os.ReadFile(resolved)
		if err != nil {
			return fmt.Errorf("%w: inspect changed file %q", model.ErrUnavailable, relative)
		}
		for _, secret := range secrets {
			if secret != "" && bytes.Contains(content, []byte(secret)) {
				return fmt.Errorf("%w: changed file %q contains a leased secret", evidence.ErrSecretRejected, relative)
			}
		}
	}
	return nil
}

// egressEnforcementAvailable requires the actual network namespace and a trusted
// Unix-to-loopback bridge. Every provider invocation rechecks its envelope.
func (r *Runtime) egressEnforcementAvailable() bool {
	path, err := trustedBwrapPath()
	if err != nil {
		return false
	}
	if _, err := sandbox.TrustedBridgePath(); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return sandbox.NewBwrap(path).Probe(ctx).Available
}

func (r *Runtime) resolveAdapter(ctx context.Context, name string, task model.Task, worktreePath, subject string, networkAllowed bool, modelName string, proxySocket string) (adapter.Adapter, capability.GrantID, error) {
	if candidate := r.adapters[name]; candidate != nil {
		return candidate, "", nil
	}
	switch name {
	case "codex", "gemini", "claude", "opencode":
	default:
		return nil, "", fmt.Errorf("%w: adapter %s is unavailable", model.ErrUnavailable, name)
	}
	binary, err := project.FindBinary(name)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s CLI is missing", model.ErrUnavailable, name)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(binary); resolveErr == nil {
		binary = resolved
	}
	process := worker.New(30*time.Minute, 3*time.Second, 8<<20)
	var runner adapter.ProcessRunner = process
	if bwrapPath, lookupErr := trustedBwrapPath(); lookupErr == nil {
		backend := sandbox.NewBwrap(bwrapPath)
		capability := backend.Probe(ctx)
		chosen, chooseErr := sandbox.ChooseIsolation(capability, task.Risk, networkAllowed, r.allowProcessOnly)
		if chooseErr != nil {
			return nil, "", chooseErr
		}
		if chosen.Level == model.IsolationBwrap {
			resolvedBinary, installationBind, mountErr := cliInstallationMount(binary)
			if mountErr != nil {
				return nil, "", mountErr
			}
			binary = resolvedBinary
			readOnlyBinds := []model.Bind{installationBind}
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

			// Provider HOME/XDG trees are fresh tmpfs mounts. Host-native auth,
			// instructions, memory, plugins and MCP configuration are deliberately
			// not mounted across the governance boundary.
			extraEnv = append(extraEnv,
				"XDG_CONFIG_HOME=/home/marshal/.config",
				"XDG_DATA_HOME=/home/marshal/.local/share",
				"XDG_CACHE_HOME=/home/marshal/.cache",
			)

			// Forward OLLAMA_HOST; default to the exact allowed IP for local Ollama
			ollamaHost := os.Getenv("OLLAMA_HOST")
			if ollamaHost == "" {
				ollamaHost = "http://127.0.0.1:11434"
			}
			extraEnv = append(extraEnv, "OLLAMA_HOST="+ollamaHost)

			// Forward MARSHAL_OPENCODE_MODEL if set
			if m := os.Getenv("MARSHAL_OPENCODE_MODEL"); m != "" {
				extraEnv = append(extraEnv, "MARSHAL_OPENCODE_MODEL="+m)
			}

			var bridge string
			if networkAllowed {
				bridge, err = sandbox.TrustedBridgePath()
				if err != nil || proxySocket == "" {
					return nil, "", netpolicy.ErrEnforcementUnavailable
				}
			}

			trap, err := r.armHoneypot(worktreePath)
			if err != nil {
				return nil, "", err
			}

			extraEnv = append(extraEnv, trap.Env...)
			brokerEnv, brokerErr := r.sandboxBroker(proxySocket, trap.Home)
			if brokerErr != nil {
				return nil, "", brokerErr
			}
			// Replace synthetic honeypot env entries for the selected provider.
			for _, kv := range brokerEnv {
				key := strings.SplitN(kv, "=", 2)[0] + "="
				filtered := extraEnv[:0]
				for _, old := range extraEnv {
					if !strings.HasPrefix(old, key) {
						filtered = append(filtered, old)
					}
				}
				extraEnv = append(filtered, kv)
			}

			runner = worker.NewGuardedSandboxed(process, backend, model.SandboxRequest{
				ScratchHome: trap.Home,
				Worktree:    worktreePath, NetworkAllowed: networkAllowed,
				EgressSocket: proxySocket, BridgeBinary: bridge,
				ReadOnlyBinds: readOnlyBinds,
				WritableTmpfs: writableTmpfs,
				ExtraEnv:      extraEnv,
			}, trap.Observe, func(ctx context.Context, result *adapter.ProcessResult) error {
				r.egressMu.Lock()
				identity := EgressAlert{TaskID: task.ID}
				for _, scope := range r.egressRuns {
					if scope.socket == proxySocket {
						identity = EgressAlert{RunID: scope.id, ParentRunID: scope.parent, TaskID: scope.task, Worker: scope.worker}
						break
					}
				}
				r.egressMu.Unlock()
				err := r.checkHoneypot(ctx, task.ID, trap, result.Stdout, result.Stderr, identity)
				result.Stdout = trap.Redact(result.Stdout)
				result.Stderr = trap.Redact(result.Stderr)
				return err
			}, r.socketObserver(proxySocket))
		}
	} else if _, err := sandbox.ChooseIsolation(model.IsolationCapability{}, task.Risk, networkAllowed, r.allowProcessOnly); err != nil {
		return nil, "", err
	}
	if r.capabilityBroker != nil {
		if r.authorityPrincipal != nil && r.processAuthority != "" {
			runner = adapter.NewRoleCapabilityRunner(runner, *r.authorityPrincipal, task.ID, r.processAuthority, r.capabilityBroker)
		} else {
			runner = adapter.NewCapabilityRunner(runner, r.capabilityBroker, subject, task.ID)
		}
	}
	// Issue the minimum scoped capability the provider process launch requires:
	// a shell-exec grant bound to this exact executable, task, and agent. It is
	// deliberately narrow (single binary path, "execute" only) and expires on a
	// short lease so a provider never holds a global execution grant.
	var shellExecGrant capability.GrantID
	if r.capabilityBroker != nil {
		grantKey, keyErr := model.NewID("shell-exec-grant-")
		if keyErr != nil {
			return nil, "", keyErr
		}
		grant, grantErr := r.capabilityBroker.Grant(ctx, capability.GrantRequest{
			Subject:        capability.SubjectID(subject),
			TaskID:         capability.TaskID(task.ID),
			Kind:           capability.KindShellExec,
			Scope:          capability.Scope{Resource: binary, Actions: []string{"execute"}},
			IssuedAt:       time.Now().UTC(),
			ExpiresAt:      time.Now().UTC().Add(30 * time.Minute),
			Issuer:         "runtime",
			IdempotencyKey: grantKey,
		})
		if grantErr != nil {
			return nil, "", fmt.Errorf("%w: grant provider execution capability: %v", model.ErrPolicyDenied, grantErr)
		}
		shellExecGrant = grant.ID
	}
	switch name {
	case "codex":
		return codex.New(binary, runner), shellExecGrant, nil
	case "gemini":
		return gemini.New(binary, runner), shellExecGrant, nil
	case "claude":
		return claude.New(binary, runner), shellExecGrant, nil
	case "opencode":
		return opencode.NewWithModel(binary, runner, modelName), shellExecGrant, nil
	default:
		return nil, "", fmt.Errorf("%w: adapter %s is unavailable", model.ErrUnavailable, name)
	}
}

func trustedBwrapPath() (string, error) {
	for _, candidate := range []string{"/usr/bin/bwrap", "/bin/bwrap"} {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o022 == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("trusted bubblewrap binary is unavailable")
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
func (r *Runtime) SecretBroker() secrets.Broker        { return r.secretBroker }
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
	return RestoreStateForProject(ctx, rootDir, backupPath, "")
}

// RestoreStateForProject restores a verified backup into the project runtime
// database. Callers that already know the canonical project identity must
// supply it: accepting a syntactically valid backup from another project is
// not a safe restore.
func RestoreStateForProject(ctx context.Context, rootDir, backupPath, projectID string) error {
	layout, err := project.Discover(rootDir)
	if err != nil {
		return err
	}
	return store.RestoreDatabase(ctx, backupPath, layout.Database, projectID, store.LatestSchemaVersion)
}

// RestoreStateForProjectExpected is the TOCTOU-safe restore boundary for
// interactive callers. It accepts only the exact digest verified during the
// destructive confirmation, never a mutable path alone.
func RestoreStateForProjectExpected(ctx context.Context, rootDir, backupPath, projectID, expectedDigest string) error {
	layout, err := project.Discover(rootDir)
	if err != nil {
		return err
	}
	return store.RestoreDatabaseExpected(ctx, backupPath, layout.Database, projectID, store.LatestSchemaVersion, expectedDigest)
}

// ProjectRoot is the canonical project root used by lifecycle operations. It
// is deliberately an internal application boundary; callers must not render
// it as a user-facing path.
func (r *Runtime) ProjectRoot() string {
	if r == nil {
		return ""
	}
	return r.layout.Root
}

func (r *Runtime) Store() *store.Store { return r.store }

// Memory exposes the single canonical product-facing memory service to local
// interfaces. Callers must use its authorization and governance boundaries.
func (r *Runtime) Memory() *MemoryService { return r.memoryService }

// Marshal exposes the project Marshal service.
func (r *Runtime) Marshal() *MarshalService {
	if r == nil {
		return nil
	}
	project, err := r.store.Project(context.Background())
	if err != nil {
		return nil
	}
	return &MarshalService{Store: r.store, ProjectID: project.ID, Repository: r.layout.Root, Worktrees: r.layout.Worktrees}
}
