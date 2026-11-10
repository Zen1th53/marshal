package tui

// Control bindings for the advanced-menu entries that needed an
// application-layer boundary first (tasks/tui-v3/TUI3-0014..0020). Each form
// collects every argument it passes; the boundary in internal/app/menu_rest.go
// authenticates the local operator, binds the reviewed target and makes a
// retried submission idempotent. Success is proved by rereading durable state.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/localserve"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policytest"
	"github.com/Zen1th53/marshal/internal/protocol"
)

// MenuRestAuthority is optional, like MemoryActionAuthority: an authority
// gains these operator powers only by implementing them over the canonical
// runtime.
type MenuRestAuthority interface {
	ReviewPolicySuite(ctx context.Context, path string) (app.MemoryFileBinding, error)
	RunPolicySuite(ctx context.Context, reviewed app.MemoryFileBinding) (app.PolicyTestReport, error)
	OperatorTaskHandoff(ctx context.Context, e app.CommandEnvelope, req app.TaskHandoffRequest) (protocol.Handoff, error)
	OperatorMemoryHandoff(ctx context.Context, e app.CommandEnvelope, req app.MemoryHandoffRequest) (protocol.Handoff, error)
	TypedHandoff(ctx context.Context, id protocol.HandoffID) (protocol.Handoff, error)
	ReviewClaim(ctx context.Context, claimID string) (model.Claim, error)
	ChallengeClaim(ctx context.Context, e app.CommandEnvelope, req app.ClaimChallengeRequest) (model.Claim, error)
	CorrectInterpretation(ctx context.Context, e app.CommandEnvelope, decisionID, option string) (model.GoalContract, error)
}

func menuRestBindings(s *ControlSource) map[ActionID]Binding {
	out := map[ActionID]Binding{
		"CTUI-0649": policySuiteBinding(s),
		"CTUI-0320": taskHandoffBinding(s),
		"CTUI-0321": memoryHandoffBinding(s),
		"CTUI-0318": claimChallengeBinding(s),
		"CTUI-0248": interpretationCorrectionBinding(s),
	}
	if _, ok := s.Authority.(MenuRestAuthority); !ok {
		for id, b := range out {
			b.Requires = "authenticated local operator authority is unavailable"
			b.Execute = nil
			out[id] = b
		}
	}
	for id, b := range localServerBindings(s) {
		out[id] = b
	}
	for _, entry := range menuRestUnderConstruction {
		id := ActionID(entry.id)
		out[id] = Binding{Action: id, Title: entry.title, Safety: entry.safety, Requires: entry.requires}
	}
	return out
}

func menuRestAuthority(s *ControlSource) (MenuRestAuthority, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	a, ok := s.Authority.(MenuRestAuthority)
	if !ok {
		return nil, ErrNoBinding
	}
	return a, nil
}

func menuEnvelope(s *ControlSource, req ActionRequest) app.CommandEnvelope {
	return app.CommandEnvelope{ProjectID: s.ProjectID, SessionID: s.SessionID, TargetID: req.Target.ID, ExpectedVersion: req.Target.Revision, IdempotencyKey: req.IdempotencyKey}
}

func validateHandoffRole(v string) error {
	switch protocol.Role(strings.TrimSpace(v)) {
	case protocol.RoleOrchestrator, protocol.RoleArchitect, protocol.RoleDeveloper, protocol.RoleQA, protocol.RoleAppSec:
		return nil
	}
	return fmt.Errorf("enter orchestrator, architect, developer, qa or appsec")
}

func menuSplitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// --- CTUI-0649 ---

func policySuiteBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0649", Title: "Run policy test suite", Safety: SafetyGoverned,
		Inputs: []InputSpec{{Key: "path", Label: "Project-relative policy test suite (JSON)", Required: true}},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			a, err := menuRestAuthority(s)
			if err != nil {
				return Target{}, err
			}
			file, err := a.ReviewPolicySuite(ctx, strings.TrimSpace(req.Inputs["path"]))
			if err != nil {
				return Target{}, fmt.Errorf("the suite could not be read: %w", fileReadReason(err))
			}
			return Target{Kind: "policy-suite", ID: file.Path, Revision: file.Size, Digest: file.Digest, Scope: "declarative evaluation; nothing is executed or written",
				Summary: fmt.Sprintf("%s: %d bytes #%s", file.Path, file.Size, shortDigest(file.Digest))}, nil
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			a, err := menuRestAuthority(s)
			if err != nil {
				return Outcome{}, err
			}
			report, err := a.RunPolicySuite(ctx, app.MemoryFileBinding{Path: req.Target.ID, Size: req.Target.Revision, Digest: req.Target.Digest})
			if err != nil {
				return refusal("the policy test suite did not run", err, req.Target, "internal/app/policytest_service.go")
			}
			failed := 0
			for _, c := range report.Cases {
				if c.Status != policytest.StatusPass {
					failed++
				}
			}
			detail := fmt.Sprintf("suite %s: %d cases, %d not passing; policy #%s", report.Status, len(report.Cases), failed, shortDigest(string(report.PolicyDigest)))
			verdict := VerdictPass
			if report.Status != policytest.StatusPass {
				verdict = VerdictFail
			}
			return Outcome{Verdict: verdict, Detail: detail, Target: req.Target, Evidence: string(report.PolicyDigest),
				Proof: Known(string(report.Status), "internal/policytest")}, nil
		}}
}

// --- CTUI-0320 / CTUI-0321 ---

func prepareHandoffTask(ctx context.Context, s *ControlSource, req ActionRequest, kind string) (Target, error) {
	if _, err := menuRestAuthority(s); err != nil {
		return Target{}, err
	}
	task, err := s.Authority.Task(ctx, strings.TrimSpace(req.Inputs["task_id"]))
	if err != nil {
		return Target{}, fmt.Errorf("the task could not be read: %w", err)
	}
	role := strings.TrimSpace(req.Inputs["target_role"])
	return Target{Kind: "task", ID: task.ID, Revision: task.Revision, Digest: memoryBindingDigest(struct{ Task, Role, Kind string }{task.ID, role, kind}),
		Scope:   fmt.Sprintf("task %s @%d", task.ID, task.Revision),
		Summary: fmt.Sprintf("%s handoff of %s (%s) to %s", kind, task.ID, task.Status, role)}, nil
}

func proveHandoff(ctx context.Context, a MenuRestAuthority, req ActionRequest, sent protocol.Handoff, source string) (Outcome, error) {
	proof, err := a.TypedHandoff(ctx, sent.ID)
	if err != nil {
		return unproven(req.Target, string(sent.ID), err)
	}
	if proof.Status != protocol.StatusAccepted && proof.Status != protocol.StatusConsumed || string(proof.TaskID) != req.Target.ID ||
		string(proof.ToRole) != strings.TrimSpace(req.Inputs["target_role"]) {
		reason := fmt.Sprintf("handoff %s does not read back as accepted for %s", proof.ID, req.Target.ID)
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/store/typed_handoffs.go")}, nil
	}
	return Outcome{Verdict: VerdictPass, Detail: fmt.Sprintf("handoff %s to %s is %s", proof.ID, proof.ToRole, proof.Status),
		Target: req.Target, Evidence: proof.ContextDigest, Proof: Known(string(proof.Status), source)}, nil
}

func taskHandoffBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0320", Title: "Typed task handoff", Safety: SafetyGoverned,
		Inputs: []InputSpec{
			{Key: "task_id", Label: "Task ID", Required: true},
			{Key: "target_role", Label: "Target role (developer, qa, architect, appsec, orchestrator)", Required: true, Validate: validateHandoffRole},
			{Key: "summary", Label: "Handoff summary", Required: true},
			{Key: "evidence_ids", Label: "Evidence IDs of this task (comma-separated)", Advanced: true},
			{Key: "risks", Label: "Open risks (comma-separated)", Advanced: true},
		},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			return prepareHandoffTask(ctx, s, req, "typed")
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			a, err := menuRestAuthority(s)
			if err != nil {
				return Outcome{}, err
			}
			sent, err := a.OperatorTaskHandoff(ctx, menuEnvelope(s, req), app.TaskHandoffRequest{
				TaskID: req.Target.ID, TargetRole: strings.TrimSpace(req.Inputs["target_role"]), Summary: req.Inputs["summary"],
				EvidenceIDs: menuSplitList(req.Inputs["evidence_ids"]), Risks: menuSplitList(req.Inputs["risks"]),
			})
			if err != nil {
				return refusal("the handoff was not accepted", err, req.Target, "Runtime.SubmitHandoff")
			}
			return proveHandoff(ctx, a, req, sent, "Runtime.SubmitHandoff")
		}}
}

func memoryHandoffBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0321", Title: "Memory handoff", Safety: SafetyGoverned,
		Inputs: []InputSpec{
			{Key: "task_id", Label: "Task ID whose memory is handed over", Required: true},
			{Key: "target_role", Label: "Target role (developer, qa, architect, appsec, orchestrator)", Required: true, Validate: validateHandoffRole},
			{Key: "max_bytes", Label: "Memory context budget in bytes (default 32768)", Advanced: true, Validate: func(v string) error {
				if strings.TrimSpace(v) == "" {
					return nil
				}
				n, err := strconv.Atoi(strings.TrimSpace(v))
				if err != nil || n < 1 || n > 128<<10 {
					return fmt.Errorf("enter a number from 1 to %d", 128<<10)
				}
				return nil
			}},
		},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			return prepareHandoffTask(ctx, s, req, "memory")
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			a, err := menuRestAuthority(s)
			if err != nil {
				return Outcome{}, err
			}
			maxBytes, _ := strconv.Atoi(strings.TrimSpace(req.Inputs["max_bytes"]))
			sent, err := a.OperatorMemoryHandoff(ctx, menuEnvelope(s, req), app.MemoryHandoffRequest{
				TaskID: req.Target.ID, TargetRole: strings.TrimSpace(req.Inputs["target_role"]), MaxBytes: maxBytes,
			})
			if err != nil {
				return refusal("the memory handoff was not accepted", err, req.Target, "Runtime.CompileAndSubmitHandoff")
			}
			return proveHandoff(ctx, a, req, sent, "Runtime.CompileAndSubmitHandoff")
		}}
}

// --- CTUI-0318 ---

func claimChallengeBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0318", Title: "Claim challenges and counter-evidence", Safety: SafetyGoverned,
		Inputs: []InputSpec{
			{Key: "claim_id", Label: "Claim ID", Required: true},
			{Key: "evidence_id", Label: "Stored counter-evidence ID", Required: true},
			{Key: "reason", Label: "Why the evidence contradicts the claim", Required: true},
		},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			a, err := menuRestAuthority(s)
			if err != nil {
				return Target{}, err
			}
			claim, err := a.ReviewClaim(ctx, strings.TrimSpace(req.Inputs["claim_id"]))
			if err != nil {
				return Target{}, fmt.Errorf("the claim could not be read: %w", err)
			}
			if claim.State == model.ClaimStateInvalidated {
				return Target{}, fmt.Errorf("claim %s is already invalidated", claim.ID)
			}
			return Target{Kind: "claim", ID: claim.ID, Digest: app.ClaimDigest(claim), Scope: fmt.Sprintf("goal %s @%d", claim.GoalID, claim.GoalRevision),
				Summary: fmt.Sprintf("contest %s (%s): %s; counter-evidence %s", claim.ID, claim.State, claim.NormalizedText, strings.TrimSpace(req.Inputs["evidence_id"]))}, nil
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			a, err := menuRestAuthority(s)
			if err != nil {
				return Outcome{}, err
			}
			evidenceID := strings.TrimSpace(req.Inputs["evidence_id"])
			e := menuEnvelope(s, req)
			e.ExpectedVersion = 0
			if _, err := a.ChallengeClaim(ctx, e, app.ClaimChallengeRequest{EvidenceID: evidenceID, Reason: req.Inputs["reason"], ExpectedDigest: req.Target.Digest}); err != nil {
				return refusal("the claim was not challenged", err, req.Target, "collaboration.Coordinator.ChallengeClaim")
			}
			proof, err := a.ReviewClaim(ctx, req.Target.ID)
			if err != nil {
				return unproven(req.Target, req.Target.ID, err)
			}
			cited := false
			for _, ref := range proof.ContradictingEvidence {
				cited = cited || ref.EvidenceID == evidenceID
			}
			if proof.State != model.ClaimStateContested || !cited {
				reason := fmt.Sprintf("claim %s reads back as %s without the counter-evidence", proof.ID, proof.State)
				return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/store/claim.go")}, nil
			}
			return Outcome{Verdict: VerdictPass, Detail: fmt.Sprintf("claim %s is CONTESTED by %s", proof.ID, evidenceID),
				Target: Target{Kind: "claim", ID: proof.ID, Digest: app.ClaimDigest(proof)}, Evidence: evidenceID,
				Proof: Known(string(proof.State), "collaboration.Coordinator.ChallengeClaim")}, nil
		}}
}

// --- CTUI-0248 ---

func interpretationCorrectionBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0248", Title: "Resolve with user correction", Safety: SafetyPlain,
		Inputs: []InputSpec{
			{Key: "decision_id", Label: "Unresolved decision ID", Required: true},
			{Key: "option", Label: "Your answer (one of the decision's options when it lists any)", Required: true},
		},
		// Pre-form check: a goal with an unresolved decision must exist, or
		// the form could only be refused after it was filled in.
		Prepare: func(ctx context.Context) (Target, error) {
			if _, err := menuRestAuthority(s); err != nil {
				return Target{}, err
			}
			target, err := s.prepareGoal(ctx)
			if err != nil {
				return Target{}, err
			}
			goal, err := s.Authority.CurrentGoal(ctx)
			if err != nil {
				return Target{}, err
			}
			if len(goal.UnresolvedDecisions) == 0 {
				return Target{}, fmt.Errorf("goal %s has no unresolved decision to answer", goal.ID)
			}
			return target, nil
		},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			if _, err := menuRestAuthority(s); err != nil {
				return Target{}, err
			}
			target, err := s.prepareGoal(ctx)
			if err != nil {
				return Target{}, err
			}
			goal, err := s.Authority.CurrentGoal(ctx)
			if err != nil {
				return Target{}, err
			}
			id := strings.TrimSpace(req.Inputs["decision_id"])
			for _, d := range goal.UnresolvedDecisions {
				if d.ID == id {
					target.Summary = fmt.Sprintf("answer %q on goal %s @%d with %q; critical claims of this revision are invalidated and the goal needs confirmation again",
						d.Question, goal.ID, goal.Revision, strings.TrimSpace(req.Inputs["option"]))
					return target, nil
				}
			}
			return Target{}, fmt.Errorf("goal %s has no unresolved decision %s", goal.ID, id)
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			a, err := menuRestAuthority(s)
			if err != nil {
				return Outcome{}, err
			}
			decisionID := strings.TrimSpace(req.Inputs["decision_id"])
			if _, err := a.CorrectInterpretation(ctx, menuEnvelope(s, req), decisionID, req.Inputs["option"]); err != nil {
				return refusal("the correction was not applied", err, req.Target, "interpretation.Invalidator.ApplyCorrection")
			}
			proof, err := s.Authority.CurrentGoal(ctx)
			if err != nil {
				return unproven(req.Target, req.Target.ID, err)
			}
			for _, d := range proof.UnresolvedDecisions {
				if d.ID == decisionID {
					proof.Revision = 0
				}
			}
			if proof.ID != req.Target.ID || proof.Revision <= req.Target.Revision {
				reason := fmt.Sprintf("goal %s does not read back as a corrected revision", req.Target.ID)
				return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/store/goal.go")}, nil
			}
			return Outcome{Verdict: VerdictPass, Detail: fmt.Sprintf("goal %s corrected to revision %d; it requires fresh confirmation", proof.ID, proof.Revision),
				Target: Target{Kind: "goal", ID: proof.ID, Revision: proof.Revision, Digest: proof.RequestDigest},
				Proof:  Known(fmt.Sprintf("goal revision %d (%s)", proof.Revision, proof.Confirmation), "interpretation.Invalidator.ApplyCorrection")}, nil
		}}
}

// --- runtime adapter ---

func (a *runtimeControlAuthority) ReviewPolicySuite(ctx context.Context, path string) (app.MemoryFileBinding, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return app.MemoryFileBinding{}, err
	}
	return a.runtime.ReviewPolicyTestSuite(ctx, path)
}

func (a *runtimeControlAuthority) RunPolicySuite(ctx context.Context, reviewed app.MemoryFileBinding) (app.PolicyTestReport, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return app.PolicyTestReport{}, err
	}
	return a.runtime.RunPolicyTestSuite(ctx, reviewed)
}

func (a *runtimeControlAuthority) OperatorTaskHandoff(ctx context.Context, e app.CommandEnvelope, req app.TaskHandoffRequest) (protocol.Handoff, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return protocol.Handoff{}, err
	}
	return a.runtime.CommandTaskHandoff(ctx, e, req)
}

func (a *runtimeControlAuthority) OperatorMemoryHandoff(ctx context.Context, e app.CommandEnvelope, req app.MemoryHandoffRequest) (protocol.Handoff, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return protocol.Handoff{}, err
	}
	return a.runtime.CommandMemoryHandoff(ctx, e, req)
}

func (a *runtimeControlAuthority) TypedHandoff(ctx context.Context, id protocol.HandoffID) (protocol.Handoff, error) {
	if a == nil || a.runtime == nil {
		return protocol.Handoff{}, errNoRuntime
	}
	return a.runtime.Store().GetHandoff(ctx, id)
}

func (a *runtimeControlAuthority) ReviewClaim(ctx context.Context, claimID string) (model.Claim, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return model.Claim{}, err
	}
	return a.runtime.ReviewClaim(ctx, claimID)
}

func (a *runtimeControlAuthority) ChallengeClaim(ctx context.Context, e app.CommandEnvelope, req app.ClaimChallengeRequest) (model.Claim, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return model.Claim{}, err
	}
	return a.runtime.CommandChallengeClaim(ctx, e, req)
}

func (a *runtimeControlAuthority) CorrectInterpretation(ctx context.Context, e app.CommandEnvelope, decisionID, option string) (model.GoalContract, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return model.GoalContract{}, err
	}
	return a.runtime.CommandCorrectInterpretation(ctx, e, decisionID, option)
}

// --- CTUI-0705 / CTUI-0741 / CTUI-0750: local servers ---

// LocalServerAuthority hosts the local servers on the workspace's runtime.
type LocalServerAuthority interface {
	StartLocalServer(ctx context.Context, kind localserve.Kind, addr string) (localserve.Status, error)
	LocalServerStatus(kind localserve.Kind) (localserve.Status, bool)
}

// localHosts holds one server Host per open runtime. A Host is closed when
// its workspace closes or its runtime is replaced by a restore.
var localHosts = struct {
	sync.Mutex
	m map[*app.Runtime]*localserve.Host
}{m: map[*app.Runtime]*localserve.Host{}}

func localHostFor(r *app.Runtime) *localserve.Host {
	localHosts.Lock()
	defer localHosts.Unlock()
	h, ok := localHosts.m[r]
	if !ok {
		h = localserve.New(r)
		localHosts.m[r] = h
	}
	return h
}

func closeLocalServers(r *app.Runtime) {
	if r == nil {
		return
	}
	localHosts.Lock()
	h := localHosts.m[r]
	delete(localHosts.m, r)
	localHosts.Unlock()
	h.Close()
}

func (w *Workspace) closeLocalServers() {
	w.mu.RLock()
	r := w.runtime
	w.mu.RUnlock()
	closeLocalServers(r)
}

func (a *runtimeControlAuthority) StartLocalServer(ctx context.Context, kind localserve.Kind, addr string) (localserve.Status, error) {
	if _, err := a.memoryActionContext(ctx); err != nil {
		return localserve.Status{}, err
	}
	return localHostFor(a.runtime).Start(ctx, kind, addr)
}

func (a *runtimeControlAuthority) LocalServerStatus(kind localserve.Kind) (localserve.Status, bool) {
	if a == nil || a.runtime == nil {
		return localserve.Status{}, false
	}
	localHosts.Lock()
	h := localHosts.m[a.runtime]
	localHosts.Unlock()
	return h.Status(kind)
}

func localServerBindings(s *ControlSource) map[ActionID]Binding {
	out := map[ActionID]Binding{
		"CTUI-0705": localServerBinding(s, "CTUI-0705", "Start local daemon", localserve.Daemon, "runtime socket for `marshal` CLI clients"),
		"CTUI-0741": localServerBinding(s, "CTUI-0741", "Start local MCP server", localserve.MCP, "authenticated MCP over HTTP"),
		"CTUI-0750": localServerBinding(s, "CTUI-0750", "Start local A2A server", localserve.A2A, "authenticated A2A over HTTP"),
	}
	if _, ok := s.Authority.(LocalServerAuthority); !ok {
		for id, b := range out {
			b.Requires = "this workspace has no runtime to host a local server on"
			b.Execute = nil
			out[id] = b
		}
	}
	return out
}

func localServerBinding(s *ControlSource, id ActionID, title string, kind localserve.Kind, what string) Binding {
	authority := func() (LocalServerAuthority, error) {
		if err := s.available(); err != nil {
			return nil, err
		}
		a, ok := s.Authority.(LocalServerAuthority)
		if !ok {
			return nil, ErrNoBinding
		}
		return a, nil
	}
	prepare := func(ctx context.Context, req ActionRequest) (Target, error) {
		a, err := authority()
		if err != nil {
			return Target{}, err
		}
		if running, ok := a.LocalServerStatus(kind); ok {
			return Target{}, fmt.Errorf("the %s server is already running on %s", kind, running.Address)
		}
		addr := strings.TrimSpace(req.Inputs["listen"])
		where := addr
		if kind == localserve.Daemon {
			where = "the project's runtime socket"
		}
		return Target{Kind: "local-server", ID: string(kind), Digest: memoryBindingDigest(struct{ Kind, Addr string }{string(kind), addr}),
			Scope:   "hosted by this workspace; it stops when MARSHAL exits",
			Summary: fmt.Sprintf("start %s (%s) on %s", kind, what, where)}, nil
	}
	b := Binding{Action: id, Title: title, Safety: SafetyPlain, PrepareRequest: prepare,
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			a, err := authority()
			if err != nil {
				return Outcome{}, err
			}
			started, err := a.StartLocalServer(ctx, kind, strings.TrimSpace(req.Inputs["listen"]))
			if err != nil {
				return refusal(fmt.Sprintf("the %s server did not start", kind), err, req.Target, "internal/localserve")
			}
			proof, ok := a.LocalServerStatus(kind)
			if !ok || proof.Address != started.Address {
				return unproven(req.Target, string(kind), fmt.Errorf("the server is not running after start"))
			}
			return Outcome{Verdict: VerdictPass, Detail: fmt.Sprintf("%s server listening on %s until MARSHAL exits", kind, proof.Address),
				Target: req.Target, Evidence: proof.Address, Proof: Known("listening on "+proof.Address, "internal/localserve")}, nil
		}}
	if kind != localserve.Daemon {
		b.Inputs = []InputSpec{{Key: "listen", Label: "Loopback listen address (host:port)", Required: true, Default: localserve.DefaultAddress(kind), Validate: func(v string) error {
			if !localserve.LoopbackAddress(v) {
				return fmt.Errorf("enter a loopback host:port such as %s", localserve.DefaultAddress(kind))
			}
			return nil
		}}}
	}
	return b
}

// menuRestUnderConstruction names, for each entry P10 reviewed and could not
// wire safely, the exact reason.
var menuRestUnderConstruction = []struct {
	id, title, requires string
	safety              Safety
}{
	{"CTUI-0210", "Consented initialization repair", "owner decision: startup.Repair's only repairable condition (STARTUP_PROJECT_NOT_INITIALIZED) cannot exist while a workspace has the project open, and its fix (app.Bootstrap / marshal setup) must not run against the store the workspace holds. A binding here could only ever refuse. Either widen startup.repairableReasons (Process 01 forbids silent repair) or keep repair in `marshal setup` / `marshal doctor` before launch (P10 review 2026-11-05)", SafetyGoverned},
	{"CTUI-0266", "Authorized dynamic DAG changes", "Runtime.DAG() is a dag.Engine built with NewEngine (no IdentityProvider, Authorizer or FreshnessValidator), so it cannot perform the 'authorized CAS graph mutation' the spec names, and no production code reads dag_nodes: Process 05 schedules from plan tasks. An edge added here would be unauthorized and change nothing. Needs the authorized engine wired into execution first (P10 review 2026-11-05)", SafetyGoverned},
	{"CTUI-0587", "Offline bounded replay", "owner decision: OptimizationService.ExecuteReplay/Promote/Canary take a caller-supplied optimization.Governance (governable providers, evidence freshness, independent-cluster floor, canary exposure cap, rollback requirement) and no canonical Process 08 governance record exists in the store or config; no production code constructs one or calls StartCycle. A TUI form supplying those values would author the very policy that judges its own promotion. Needs an owner decision on where Process 08 governance lives (P10 review 2026-11-05); the replay also needs a stored cycle and a bwrap ReplayRunner, which exists (optimization.BwrapReplayRunner) but has no production composition", SafetyGoverned},
	{"CTUI-0617", "Promotion decision and evidence", "owner decision: OptimizationService.ExecuteReplay/Promote/Canary take a caller-supplied optimization.Governance (governable providers, evidence freshness, independent-cluster floor, canary exposure cap, rollback requirement) and no canonical Process 08 governance record exists in the store or config; no production code constructs one or calls StartCycle. A TUI form supplying those values would author the very policy that judges its own promotion. Needs an owner decision on where Process 08 governance lives (P10 review 2026-11-05)", SafetyGoverned},
	{"CTUI-0621", "Canary creation", "owner decision: OptimizationService.ExecuteReplay/Promote/Canary take a caller-supplied optimization.Governance (governable providers, evidence freshness, independent-cluster floor, canary exposure cap, rollback requirement) and no canonical Process 08 governance record exists in the store or config; no production code constructs one or calls StartCycle. A TUI form supplying those values would author the very policy that judges its own promotion. Needs an owner decision on where Process 08 governance lives (P10 review 2026-11-05); a canary also needs a stored promotion record, which nothing produces", SafetyGoverned},
	{"CTUI-0623", "Governed playbook promotion", "owner decision: OptimizationService.ExecuteReplay/Promote/Canary take a caller-supplied optimization.Governance (governable providers, evidence freshness, independent-cluster floor, canary exposure cap, rollback requirement) and no canonical Process 08 governance record exists in the store or config; no production code constructs one or calls StartCycle. A TUI form supplying those values would author the very policy that judges its own promotion. Needs an owner decision on where Process 08 governance lives (P10 review 2026-11-05); the spec additionally requires the active playbook change to be separately authorized, and no playbook-activation authority exists", SafetyGoverned},
	{"CTUI-0626", "Return results to learning", "the spec requires Process 08 evidence to write back, and no Process 08 cycle, counterfactual or promotion can be produced (see CTUI-0587/0617). A typed benchmark form would record operator-invented evidence into learning (P10 review 2026-11-05)", SafetyGoverned},
	{"CTUI-0664", "Grant capability or role", "owner decision: capability.Engine.Grant has only trusted in-runtime callers (task execution grants, local-control self-provisioning). A TUI grant form would make the local operator a grant issuer for arbitrary principals, kinds and scopes; which principals, capability kinds and scopes the TUI may grant, and with what expiry, needs an owner decision (P10 review 2026-11-05)", SafetyGoverned},
	{"CTUI-0781", "SPDX SBOM generation", "owner decision (legal release): tools/release_trust.py generate-sbom is a repository release script that is not part of the shipped binary (a Community install has no tools/ directory) and produces release-trust artifacts; running release tooling from the TUI is a release decision, not a menu action (P10 review 2026-11-05)", SafetyPlain},
	{"CTUI-0782", "Detached key generation", "owner decision (key material): tools/detached_sign.py generates a detached signing key that must stay outside UI state; generating release keys from the TUI process would put secret material in the workspace's memory and terminal (P10 review 2026-11-05)", SafetySensitive},
	{"CTUI-0783", "Detached signing", "owner decision (key material, legal release): detached signing uses the release key from tools/detached_sign.py, which must stay outside UI state; signing release artifacts is a release decision (P10 review 2026-11-05)", SafetySensitive},
	{"CTUI-0785", "Reproducible Linux release build", "owner decision (legal release): tools/build_release.sh is a repository release script, not part of the shipped binary, and builds release artifacts; running it from the TUI is a release decision. Nothing here may publish (P10 review 2026-11-05)", SafetyPlain},
}
