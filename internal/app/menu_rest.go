package app

// Application boundaries for advanced-menu actions that had none
// (tasks/tui-v3/TUI3-0014..0020). Each one is a thin, authenticated wrapper
// over an existing canonical primitive: it accepts only the local operator,
// checks the operator's scoped capability, binds the exact target revision or
// digest the operator reviewed, and makes a retried submission land on the
// same durable record instead of creating a second one.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/collaboration"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/interpretation"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/protocol"
	"github.com/Zen1th53/marshal/internal/store"
)

// menuRestLocalControlActions are the scoped local-operator capabilities the
// boundaries below check. They are provisioned with the others in
// OpenLocalControl.
var menuRestLocalControlActions = []string{"handoff.task", "handoff.memory", "claim.challenge", "goal.correct"}

// localOperator returns the authenticated local operator of this runtime, or
// ErrDenied. Nothing reachable from a socket, MCP or A2A carries this context.
func (r *Runtime) localOperator(ctx context.Context) (auth.LocalPrincipal, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != r.ProjectIdentity() {
		return auth.LocalPrincipal{}, authz.ErrDenied
	}
	return p, nil
}

// menuCommand authenticates the operator, checks the scoped capability for the
// operation and builds the durable command record for the envelope and input.
func (r *Runtime) menuCommand(ctx context.Context, e CommandEnvelope, operation string, authority authz.Authority, input any) (auth.LocalPrincipal, store.CommandRecord, error) {
	p, err := r.localOperator(ctx)
	if err != nil || e.ProjectID != r.ProjectIdentity() {
		return auth.LocalPrincipal{}, store.CommandRecord{}, authz.ErrDenied
	}
	if strings.TrimSpace(e.SessionID) == "" || strings.TrimSpace(e.TargetID) == "" || e.ExpectedVersion < 0 ||
		strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > 256 {
		return auth.LocalPrincipal{}, store.CommandRecord{}, model.ErrInvalid
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authority}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(e.ProjectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: operation}
	decision, err := authz.CanWithCapability(ctx, principal, authority, r.layout.Database, query, capability.NewEngine(r.store, nil))
	if err != nil {
		return auth.LocalPrincipal{}, store.CommandRecord{}, err
	}
	body, err := json.Marshal(struct {
		Envelope  CommandEnvelope
		Operation string
		Input     any
	}{e, operation, input})
	if err != nil {
		return auth.LocalPrincipal{}, store.CommandRecord{}, err
	}
	digest := sha256.Sum256(body)
	key := sha256.Sum256([]byte(e.IdempotencyKey))
	return p, store.CommandRecord{ProjectID: e.ProjectID, Actor: p.ID(), Key: hex.EncodeToString(key[:]), Operation: operation,
		SessionID: e.SessionID, TargetID: e.TargetID, ExpectedVersion: e.ExpectedVersion, Digest: hex.EncodeToString(digest[:]),
		CapabilityGrantID: decision.CapabilityGrantID}, nil
}

// --- CTUI-0649: run a policy test suite ---

// ReviewPolicyTestSuite binds a project-relative policy test suite by size and
// digest so the run that follows evaluates exactly the reviewed bytes.
func (r *Runtime) ReviewPolicyTestSuite(ctx context.Context, path string) (MemoryFileBinding, error) {
	if _, err := r.localOperator(ctx); err != nil {
		return MemoryFileBinding{}, err
	}
	_, binding, err := r.readMemoryActionFile(path)
	return binding, err
}

// RunPolicyTestSuite evaluates the reviewed suite with the declarative policy
// evaluator. It executes nothing and writes no durable state; a suite that
// changed since review is refused.
func (r *Runtime) RunPolicyTestSuite(ctx context.Context, reviewed MemoryFileBinding) (PolicyTestReport, error) {
	if _, err := r.localOperator(ctx); err != nil {
		return PolicyTestReport{}, err
	}
	data, bound, err := r.readMemoryActionFile(reviewed.Path)
	if err != nil {
		return PolicyTestReport{}, err
	}
	if reviewed.Digest == "" || bound.Digest != reviewed.Digest || bound.Size != reviewed.Size {
		return PolicyTestReport{}, fmt.Errorf("%w: policy test suite changed since review", model.ErrConflict)
	}
	return runPolicyTestData(ctx, data)
}

// --- CTUI-0320 / CTUI-0321: operator handoffs ---

// TaskHandoffRequest is a typed task handoff authored by the operator. Every
// field is operator text or an existing evidence ID of the task.
type TaskHandoffRequest struct {
	TaskID      string   `json:"task_id"`
	TargetRole  string   `json:"target_role"`
	Summary     string   `json:"summary"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	Risks       []string `json:"risks,omitempty"`
	Unresolved  []string `json:"unresolved,omitempty"`
}

// MemoryHandoffRequest hands a task's governed memory context to a role.
type MemoryHandoffRequest struct {
	TaskID     string `json:"task_id"`
	TargetRole string `json:"target_role"`
	MaxBytes   int    `json:"max_bytes,omitempty"`
}

// handoffTarget checks the envelope binds the task at its reviewed revision.
func (r *Runtime) handoffTarget(ctx context.Context, e CommandEnvelope, taskID, role string) (model.Task, error) {
	if e.TargetID != taskID {
		return model.Task{}, model.ErrInvalid
	}
	switch protocol.Role(role) {
	case protocol.RoleOrchestrator, protocol.RoleArchitect, protocol.RoleDeveloper, protocol.RoleQA, protocol.RoleAppSec:
	default:
		return model.Task{}, fmt.Errorf("%w: target role must be orchestrator, architect, developer, qa or appsec", model.ErrInvalid)
	}
	task, err := r.store.GetTask(ctx, taskID)
	if err != nil {
		return model.Task{}, err
	}
	if task.Revision != e.ExpectedVersion {
		return model.Task{}, fmt.Errorf("%w: task %s moved from revision %d to %d", model.ErrConflict, task.ID, e.ExpectedVersion, task.Revision)
	}
	return task, nil
}

// operatorHandoffID derives the handoff identity from the operator's command,
// so a retried submission resolves to the handoff it already created.
func operatorHandoffID(record store.CommandRecord) protocol.HandoffID {
	sum := sha256.Sum256([]byte(record.Actor + "\x00" + record.Operation + "\x00" + record.Key))
	return protocol.HandoffID("HND-OP-" + hex.EncodeToString(sum[:16]))
}

// existingOperatorHandoff returns the handoff a previous identical command
// created, if any. A handoff under the derived ID that does not match is a
// conflict, never a silent replacement.
func (r *Runtime) existingOperatorHandoff(ctx context.Context, id protocol.HandoffID, actor, taskID, role string) (protocol.Handoff, bool, error) {
	existing, err := r.store.GetHandoff(ctx, id)
	if err != nil {
		if errors.Is(err, protocol.ErrHandoffNotFound) {
			return protocol.Handoff{}, false, nil
		}
		return protocol.Handoff{}, false, err
	}
	if existing.FromAgent != actor || string(existing.TaskID) != taskID || string(existing.ToRole) != role {
		return protocol.Handoff{}, false, fmt.Errorf("%w: idempotency key already binds another handoff", model.ErrConflict)
	}
	return existing, true, nil
}

// operatorHandoffPrincipal is the protocol identity the local operator submits
// as. The role is trusted composition; the capability is exactly handoff
// creation, never consumption.
func operatorHandoffPrincipal(actor string) protocol.Principal {
	return protocol.Principal{ID: actor, Role: protocol.RoleOrchestrator, Capabilities: []string{"handoff.create"}}
}

// CommandTaskHandoff submits a typed operator handoff for one task through the
// canonical protocol service.
func (r *Runtime) CommandTaskHandoff(ctx context.Context, e CommandEnvelope, req TaskHandoffRequest) (protocol.Handoff, error) {
	req.Summary = strings.TrimSpace(req.Summary)
	if req.Summary == "" {
		return protocol.Handoff{}, fmt.Errorf("%w: a handoff summary is required", model.ErrInvalid)
	}
	p, record, err := r.menuCommand(ctx, e, "handoff.task", authz.AuthorityTaskPlan, req)
	if err != nil {
		return protocol.Handoff{}, err
	}
	if _, found, err := r.store.CommandResult(ctx, record); err != nil {
		return protocol.Handoff{}, err
	} else if found {
		return r.store.GetHandoff(ctx, operatorHandoffID(record))
	}
	task, err := r.handoffTarget(ctx, e, req.TaskID, req.TargetRole)
	if err != nil {
		return protocol.Handoff{}, err
	}
	id := operatorHandoffID(record)
	if existing, found, err := r.existingOperatorHandoff(ctx, id, p.ID(), task.ID, req.TargetRole); err != nil || found {
		return existing, err
	}
	evidenceIDs := make([]protocol.EvidenceID, 0, len(req.EvidenceIDs))
	for _, evidenceID := range req.EvidenceIDs {
		evidenceIDs = append(evidenceIDs, protocol.EvidenceID(evidenceID))
	}
	body, err := json.Marshal(struct {
		Request      TaskHandoffRequest
		TaskRevision int64
	}{req, task.Revision})
	if err != nil {
		return protocol.Handoff{}, err
	}
	sum := sha256.Sum256(body)
	handoff, err := r.SubmitHandoff(ctx, operatorHandoffPrincipal(p.ID()), protocol.Submission{IdempotencyKey: string(id), Handoff: protocol.Handoff{
		ID: id, Version: protocol.Version1, TaskID: protocol.TaskID(task.ID), FromAgent: p.ID(), ToRole: protocol.Role(req.TargetRole),
		Claims:      map[string]string{"summary": execution.RedactSecrets(req.Summary), "task_status": string(task.Status)},
		EvidenceIDs: evidenceIDs, ChangedFiles: []string{}, Risks: req.Risks, Unresolved: req.Unresolved,
		ContextDigest: "sha256:" + hex.EncodeToString(sum[:]),
	}})
	if err != nil {
		return protocol.Handoff{}, err
	}
	if err := r.store.RecordCommandReceipt(ctx, record, task.Revision); err != nil {
		return handoff, err
	}
	return handoff, nil
}

// CommandMemoryHandoff compiles the task's governed memory context and hands
// it to a role through CompileAndSubmitHandoff's canonical path.
func (r *Runtime) CommandMemoryHandoff(ctx context.Context, e CommandEnvelope, req MemoryHandoffRequest) (protocol.Handoff, error) {
	if req.MaxBytes < 0 || req.MaxBytes > 128<<10 {
		return protocol.Handoff{}, fmt.Errorf("%w: max bytes must be between 0 and %d", model.ErrInvalid, 128<<10)
	}
	p, record, err := r.menuCommand(ctx, e, "handoff.memory", authz.AuthorityPolicyAdmin, req)
	if err != nil {
		return protocol.Handoff{}, err
	}
	if _, found, err := r.store.CommandResult(ctx, record); err != nil {
		return protocol.Handoff{}, err
	} else if found {
		return r.store.GetHandoff(ctx, operatorHandoffID(record))
	}
	task, err := r.handoffTarget(ctx, e, req.TaskID, req.TargetRole)
	if err != nil {
		return protocol.Handoff{}, err
	}
	id := operatorHandoffID(record)
	if existing, found, err := r.existingOperatorHandoff(ctx, id, p.ID(), task.ID, req.TargetRole); err != nil || found {
		return existing, err
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Capabilities: []string{"handoff.create"}, Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	handoff, err := r.compileAndSubmitHandoff(ctx, principal, HandoffCompileRequest{
		ProjectID: r.ProjectID(), TaskID: task.ID, SourceAgentID: p.ID(), TargetRole: req.TargetRole, MaxBytes: req.MaxBytes,
	}, id)
	if err != nil {
		return protocol.Handoff{}, err
	}
	if err := r.store.RecordCommandReceipt(ctx, record, task.Revision); err != nil {
		return handoff, err
	}
	return handoff, nil
}

// --- CTUI-0318: claim challenge with counter-evidence ---

// ClaimDigest binds the claim state an operator reviewed.
func ClaimDigest(c model.Claim) string {
	body, _ := json.Marshal(struct {
		ID, State, Reason string
		Contradicting     int
		Evaluated         time.Time
	}{c.ID, string(c.State), c.StateReason, len(c.ContradictingEvidence), c.EvaluatedAt.UTC()})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// ReviewClaim reads one claim for the operator.
func (r *Runtime) ReviewClaim(ctx context.Context, claimID string) (model.Claim, error) {
	if _, err := r.localOperator(ctx); err != nil {
		return model.Claim{}, err
	}
	return r.store.GetClaim(ctx, claimID)
}

// ClaimChallengeRequest names stored counter-evidence and the operator's reason.
type ClaimChallengeRequest struct {
	EvidenceID     string `json:"evidence_id"`
	Reason         string `json:"reason"`
	ExpectedDigest string `json:"expected_digest"`
}

// CommandChallengeClaim contests a claim through
// collaboration.Coordinator.ChallengeClaim. The counter-evidence must be a
// stored, non-draft evidence node; its type and digest come from the store,
// not from the caller.
func (r *Runtime) CommandChallengeClaim(ctx context.Context, e CommandEnvelope, req ClaimChallengeRequest) (model.Claim, error) {
	req.Reason, req.EvidenceID = strings.TrimSpace(req.Reason), strings.TrimSpace(req.EvidenceID)
	if req.Reason == "" || req.EvidenceID == "" || req.ExpectedDigest == "" {
		return model.Claim{}, model.ErrInvalid
	}
	p, record, err := r.menuCommand(ctx, e, "claim.challenge", authz.AuthorityTaskPlan, req)
	if err != nil {
		return model.Claim{}, err
	}
	if _, found, err := r.store.CommandResult(ctx, record); err != nil {
		return model.Claim{}, err
	} else if found {
		return r.store.GetClaim(ctx, e.TargetID)
	}
	claim, err := r.store.GetClaim(ctx, e.TargetID)
	if err != nil {
		return model.Claim{}, err
	}
	if ClaimDigest(claim) != req.ExpectedDigest {
		return model.Claim{}, fmt.Errorf("%w: claim %s changed since review", model.ErrConflict, claim.ID)
	}
	if claim.State == model.ClaimStateInvalidated {
		return model.Claim{}, fmt.Errorf("%w: claim %s is already invalidated", model.ErrConflict, claim.ID)
	}
	node, err := r.store.Get(ctx, evidence.NodeID(req.EvidenceID))
	if err != nil || node.State == "draft" || node.Digest == "" {
		return model.Claim{}, fmt.Errorf("%w: counter-evidence %s is not a stored evidence record", model.ErrInvalid, req.EvidenceID)
	}
	ref := model.EvidenceRef{EvidenceID: string(node.ID), EvidenceType: string(node.Type), Digest: node.Digest, Tool: "operator", CapturedAt: node.CreatedAt, Summary: execution.RedactSecrets(req.Reason)}
	coordinator := collaboration.NewCoordinator(r.store, nil)
	if err := coordinator.ChallengeClaim(ctx, e.SessionID, model.AuthorProvenance{AgentID: p.ID(), Harness: "tui"}, claim.ID, ref, execution.RedactSecrets(req.Reason)); err != nil {
		return model.Claim{}, err
	}
	if err := r.store.RecordCommandReceipt(ctx, record, 1); err != nil {
		return model.Claim{}, err
	}
	return r.store.GetClaim(ctx, claim.ID)
}

// --- CTUI-0248: resolve a blind-interpretation divergence with a correction ---

// CommandCorrectInterpretation answers one unresolved decision of the active
// goal with an operator option through interpretation.Invalidator, which
// writes the next goal revision and invalidates the critical claims formed
// under the corrected revision. The corrected goal needs fresh confirmation.
func (r *Runtime) CommandCorrectInterpretation(ctx context.Context, e CommandEnvelope, decisionID, option string) (model.GoalContract, error) {
	decisionID, option = strings.TrimSpace(decisionID), strings.TrimSpace(option)
	if decisionID == "" || option == "" {
		return model.GoalContract{}, model.ErrInvalid
	}
	return r.commandGoal(ctx, e, "goal.correct", struct{ DecisionID, Option string }{decisionID, option}, func(ctx context.Context, actor string) (model.GoalContract, error) {
		goal, err := r.store.GetActiveGoalContract(ctx, e.SessionID)
		if err != nil {
			return model.GoalContract{}, err
		}
		var decision *model.UnresolvedDecision
		for i := range goal.UnresolvedDecisions {
			if goal.UnresolvedDecisions[i].ID == decisionID {
				decision = &goal.UnresolvedDecisions[i]
			}
		}
		if decision == nil {
			return model.GoalContract{}, fmt.Errorf("%w: goal %s has no unresolved decision %s", model.ErrInvalid, goal.ID, decisionID)
		}
		if len(decision.Options) > 0 {
			known := false
			for _, candidate := range decision.Options {
				known = known || candidate == option
			}
			if !known {
				return model.GoalContract{}, fmt.Errorf("%w: %q is not one of the decision's options", model.ErrInvalid, option)
			}
		}
		current := goal
		current.Confirmation = model.ConfirmationPending
		current.RevisionReason = "operator correction of " + decisionID
		result, err := interpretation.NewInvalidator(r.store).ApplyCorrection(ctx, current, decisionID, option, model.AuthorProvenance{AgentID: actor, Harness: "tui"})
		if err != nil {
			return model.GoalContract{}, err
		}
		return r.store.GetGoalContract(ctx, goal.ID, result.UpdatedGoal.Revision)
	})
}
