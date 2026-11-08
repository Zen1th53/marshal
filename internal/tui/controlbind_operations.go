package tui

// Control bindings: plan handoff, agents, collection, grants, backup and restore.
// Split mechanically from controlbind.go; see that file's header for the
// binding contract.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// executeHandoffPlan hands the approved plan to Process 05.
//
// The spec names PlanService.Handoff. An earlier version bound this to
// StartRun, which is a different operation: starting a run does more than hand
// a plan over, and labelling one as the other is an action substitution.
func (s *ControlSource) executeHandoffPlan(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	handoff, err := s.Authority.HandoffPlan(ctx, s.SessionID)
	if err != nil {
		return refusal("the plan could not be handed off", err, req.Target,
			"internal/app/plan_runtime.go")
	}
	// The typed handoff itself must bind the exact plan the operator reviewed.
	// An ID-only check would accept a handoff produced from a different revision
	// of the same plan.
	if handoff.ID == "" || handoff.EvidenceID == "" || handoff.PlanID != req.Target.ID ||
		handoff.Version != req.Target.Revision || handoff.Digest != req.Target.Digest {
		reason := fmt.Sprintf(
			"the handoff binding %q/%d/%s does not match reviewed plan %q/%d/%s",
			handoff.PlanID, handoff.Version, shortDigest(handoff.Digest),
			req.Target.ID, req.Target.Revision, shortDigest(req.Target.Digest))
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/plan")}, nil
	}
	// Prove it by rereading every plan field the handoff names.
	proof, err := s.Authority.CurrentPlan(ctx)
	if err != nil {
		return unproven(req.Target, handoff.PlanID, err)
	}
	if proof.PlanID != handoff.PlanID || proof.Version != handoff.Version ||
		proof.Digest != handoff.Digest {
		reason := fmt.Sprintf(
			"the handed-off plan moved: handoff %q/%d/%s, current %q/%d/%s",
			handoff.PlanID, handoff.Version, shortDigest(handoff.Digest),
			proof.PlanID, proof.Version, shortDigest(proof.Digest))
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/plan"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail: fmt.Sprintf("plan %s v%d handed off to Process 05",
			proof.PlanID, proof.Version),
		Target: Target{Kind: "plan", ID: proof.PlanID, Revision: proof.Version,
			Digest: proof.Digest},
		Proof: Known(fmt.Sprintf("typed handoff durably recorded as %s", handoff.EvidenceID),
			"internal/events, internal/plan"),
		Evidence: handoff.EvidenceID,
	}, nil
}

// executeImportPlanTasks imports the plan's tasks through the canonical runtime.
//
// The create-task spec names this same boundary — "ImportTasks with one
// validated task; no separate create endpoint" — so both actions route here
// rather than one of them inventing a second write path.
func (s *ControlSource) executeImportPlanTasks(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	before, err := s.Authority.Tasks(ctx)
	if err != nil {
		return refusal("the current tasks could not be read", err, req.Target, "internal/store")
	}
	tasks, err := s.Authority.PlanTasks(ctx)
	if err != nil {
		return refusal("the plan's tasks could not be read", err, req.Target, "internal/plan")
	}
	if len(tasks) == 0 {
		reason := fmt.Sprintf(
			"plan %s defines no tasks to import", req.Target.ID)
		return Outcome{
			Verdict: VerdictBlocked, Detail: reason, Target: req.Target,
			Proof: Blocked(reason, "MARSHAL — COMMUNITY TUI / Work / Plan — Process 04",
				"internal/plan"),
		}, nil
	}

	added, err := s.Authority.ImportTasks(ctx, tasks)
	if err != nil {
		return refusal("the tasks could not be imported", err, req.Target,
			"internal/app/runtime.go")
	}
	// Prove it by rereading the task list: the count must have moved by what
	// the import claims to have added.
	after, err := s.Authority.Tasks(ctx)
	if err != nil {
		return unproven(req.Target, "the task list", err)
	}
	if len(after) != len(before)+added {
		reason := fmt.Sprintf(
			"the import reported %d added but the task list moved from %d to %d",
			added, len(before), len(after))
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/store"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("%d task(s) imported from plan %s", added, req.Target.ID),
		Target:  req.Target,
		Proof:   Known(fmt.Sprintf("%d tasks", len(after)), "internal/store"),
	}, nil
}

// executeRegisterAgent registers this session's agent through the runtime.
func (s *ControlSource) executeRegisterAgent(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	// The agent is this session acting under its own identity. Inventing a
	// name here would register somebody who does not exist.
	agent, err := s.Authority.RegisterAgent(ctx, s.SessionID, string(model.RoleDeveloper))
	if err != nil {
		return refusal("the agent could not be registered", err, req.Target,
			"internal/app/runtime.go")
	}
	after, err := s.Authority.Agents(ctx)
	if err != nil {
		return unproven(req.Target, agent.ID, err)
	}
	// The proof is this agent's own presence in the roster, not that the roster
	// grew. A count comparison credits a concurrent registration by somebody
	// else as evidence for this one, and would equally miss a registration that
	// replaced an existing row rather than adding one.
	registered := false
	for _, candidate := range after {
		if candidate.ID == agent.ID {
			registered = true
			break
		}
	}
	if !registered {
		reason := fmt.Sprintf(
			"agent %s was accepted but does not appear in the roster of %d agents",
			agent.ID, len(after))
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/store"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("agent %s registered", agent.ID),
		Target:  Target{Kind: "agent", ID: agent.ID},
		Proof:   Known(fmt.Sprintf("present in a roster of %d", len(after)), "internal/store"),
	}, nil
}

// executeGCWorktrees collects eligible worktrees through the canonical runtime.
//
// It is destructive: a worktree holds uncommitted work until it is collected.
// The dry run is not offered as a separate action because the confirmation
// already shows what would be collected before anything is removed.
func (s *ControlSource) executeGCWorktrees(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	// Ask first what would be collected, so the outcome can state it.
	planned, err := s.Authority.GCWorktrees(ctx, true)
	if err != nil {
		return refusal("eligible worktrees could not be identified", err, req.Target,
			"internal/app/runtime.go")
	}
	if planned == 0 {
		reason := "no worktree is eligible for collection"
		return Outcome{
			Verdict: VerdictBlocked, Detail: reason, Target: req.Target,
			Proof: Blocked(reason, "MARSHAL — COMMUNITY TUI / Work / Workspaces & Worktrees",
				"internal/app/runtime.go"),
		}, nil
	}

	removed, err := s.Authority.GCWorktrees(ctx, false)
	if err != nil {
		return refusal("the worktrees could not be collected", err, req.Target,
			"internal/app/runtime.go")
	}
	// Prove it: a second dry run must now find fewer eligible worktrees.
	remaining, err := s.Authority.GCWorktrees(ctx, true)
	if err != nil {
		return unproven(req.Target, "the worktree inventory", err)
	}
	if remaining >= planned {
		reason := fmt.Sprintf(
			"collection reported %d removed but %d worktrees remain eligible, "+
				"unchanged from %d before", removed, remaining, planned)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/app/runtime.go"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("%d worktree(s) collected", removed),
		Target:  req.Target,
		Proof:   Known(fmt.Sprintf("%d still eligible", remaining), "internal/app/runtime.go"),
	}, nil
}

// executeRebuildProjections rebuilds the derived memory projections.
//
// It is one of the few Memory actions that carries no content: the projections
// are derived from records that already exist, so there is nothing for this
// screen to invent.
func (s *ControlSource) executeRebuildProjections(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	if err := s.Authority.RebuildMemoryProjections(ctx); err != nil {
		return refusal("the memory projections could not be rebuilt", err, req.Target,
			"internal/app/memory_runtime.go")
	}
	// The rebuild reports only whether it returned an error. It exposes no
	// durable marker — no projection count, no generation, no health read — so
	// there is nothing to reread that would prove the projections are now
	// correct. Reporting PASS here would be claiming a transition on the
	// strength of a nil error, which is the fake success the protocol exists to
	// prevent, so this stays UNKNOWN until the service offers a durable fact.
	reason := "the rebuild was accepted without error, but the memory service " +
		"exposes no projection state to reread, so this cannot show the " +
		"projections are correct"
	return Outcome{
		Verdict: VerdictUnknown,
		Detail:  reason,
		Target:  req.Target,
		Proof:   Unknown(reason, "internal/app/memory_runtime.go"),
	}, nil
}

// prepareGrant targets the selected capability or role binding.
func (s *ControlSource) prepareGrant(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	selected := s.selectedGrant()
	if selected == "" {
		return Target{}, errors.New("no grant is selected to revoke")
	}
	binding, err := s.Authority.Grant(ctx, selected)
	if err != nil {
		return Target{}, fmt.Errorf("the grant could not be read: %w", err)
	}
	if binding.RevokedAt != nil && !binding.RevokedAt.IsZero() {
		return Target{}, fmt.Errorf("grant %s is already revoked", binding.ID)
	}
	return Target{
		Kind: "grant",
		ID:   binding.ID,
		// The policy digest binds the grant to the policy it was made under, so
		// a grant re-made under different rules shows a different digest.
		Digest: binding.PolicyDigest,
		Scope:  binding.ScopeID,
		Summary: fmt.Sprintf("%s holds %s in %s",
			binding.PrincipalID, binding.Role, binding.ScopeID),
	}, nil
}

// executeRevokeGrant withdraws a capability or role binding.
//
// It is destructive: revoking a grant can stop work that is currently relying
// on it, and the canonical store records the revocation rather than deleting
// the row, so the audit trail survives.
func (s *ControlSource) executeRevokeGrant(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	if err := s.Authority.RevokeGrant(ctx, req.Target.ID); err != nil {
		return refusal("the grant could not be revoked", err, req.Target,
			"internal/store/authz_role_bindings.go")
	}
	// Prove it: the binding must now read as revoked.
	binding, err := s.Authority.Grant(ctx, req.Target.ID)
	if err != nil {
		return unproven(req.Target, req.Target.ID, err)
	}
	if binding.RevokedAt == nil || binding.RevokedAt.IsZero() {
		reason := fmt.Sprintf(
			"the revocation was accepted but grant %s still reads as in force; "+
				"the principal may still hold access", binding.ID)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/store/authz_role_bindings.go"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("grant %s revoked", binding.ID),
		Target:  Target{Kind: "grant", ID: binding.ID, Digest: binding.PolicyDigest},
		Proof: Known(binding.RevokedAt.UTC().Format(time.RFC3339),
			"internal/store/authz_role_bindings.go"),
		Evidence: fmt.Sprintf("grant:%s principal:%s", binding.ID, binding.PrincipalID),
	}, nil
}

// executeBackupState writes a durable state backup.
//
// It is destructive in the sense the safety class means: it writes to disk and
// can overwrite an earlier backup at the same default path. The proof is the
// returned database digest, which demonstrates a backup exists rather than
// merely that one was requested.
func (s *ControlSource) executeBackupState(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	proof, err := s.Authority.BackupState(ctx)
	if err != nil {
		return refusal("the state backup could not be written", err, req.Target,
			"internal/app/runtime.go BackupState")
	}
	if proof.Digest == "" {
		reason := "the backup was accepted but reported no database digest, so " +
			"there is nothing to show it was actually written"
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/store/backup.go"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("state backed up at schema v%d", proof.SchemaVersion),
		Target:  req.Target,
		// The digest, not the path: a path would disclose the runtime
		// directory, and the digest is the part that proves anything.
		Proof: Known(shortDigest(proof.Digest), "internal/store/backup.go"),
		Evidence: fmt.Sprintf("backup digest:%s schema:v%d",
			shortDigest(proof.Digest), proof.SchemaVersion),
	}, nil
}

// executeRestoreState restores only the exact backup verified during
// preparation. The authority owns close/restore/reopen; the TUI merely
// compares the returned durable proof to the reviewed target.
func (s *ControlSource) executeRestoreState(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	expected := BackupProof{Digest: req.Target.Digest, SchemaVersion: int(req.Target.Revision)}
	proof, err := s.Authority.RestoreState(ctx, req.Inputs["backup_path"], expected)
	if err != nil {
		return refusal("the state backup could not be restored", err, req.Target,
			"internal/app/runtime.go RestoreStateForProject")
	}
	if proof.Digest == "" || proof.Digest != req.Target.Digest ||
		proof.SchemaVersion != int(req.Target.Revision) {
		reason := "the restore completed without a durable proof matching the verified backup"
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/store/backup.go")}, nil
	}
	return Outcome{Verdict: VerdictPass,
		Detail:   fmt.Sprintf("state restored from verified backup at schema v%d", proof.SchemaVersion),
		Target:   req.Target,
		Proof:    Known(shortDigest(proof.Digest), "internal/store/backup.go RestoreDatabase"),
		Evidence: fmt.Sprintf("restored backup digest:%s schema:v%d", shortDigest(proof.Digest), proof.SchemaVersion)}, nil
}
