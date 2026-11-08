package tui

// Control bindings: tokens, workspace exit, artifacts, approvals, checkpoints and rollback.
// Split mechanically from controlbind.go; see that file's header for the
// binding contract.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/optimization"
)

func parseTokenCapabilities(value string) ([]string, error) {
	return auth.ValidateCapabilities(strings.Split(value, ","))
}

func (s *ControlSource) executeCreateToken(kind auth.PrincipalKind) Executor {
	return func(ctx context.Context, req ActionRequest) (Outcome, error) {
		caps, err := parseTokenCapabilities(req.Inputs["capabilities"])
		if err != nil {
			return refusal("the capability scope is invalid", err, req.Target, "internal/auth")
		}
		plaintext, created, record, err := func() (string, bool, auth.TokenRecord, error) {
			plain, rec, wasCreated, createErr := s.Authority.CreateAccessToken(
				ctx, req.Inputs["name"], kind, caps, req.IdempotencyKey)
			return plain, wasCreated, rec, createErr
		}()
		if err != nil {
			return refusal("the access token could not be created", err, req.Target, "internal/auth")
		}
		records, err := s.Authority.TokenMetadata(ctx)
		if err != nil {
			return unproven(req.Target, record.ID, err)
		}
		var proof auth.TokenRecord
		for _, candidate := range records {
			if candidate.ID == record.ID {
				proof = candidate
				break
			}
		}
		if proof.ID == "" || proof.Revoked || proof.Name != req.Inputs["name"] || proof.Kind != kind {
			reason := "token creation returned but canonical metadata did not prove the requested active token"
			return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
				Proof: Unknown(reason, "internal/auth.Manager")}, nil
		}
		detail := fmt.Sprintf("created %s token %s", kind, proof.ID)
		if !created {
			detail = fmt.Sprintf("request was already applied as token %s; plaintext is not shown again", proof.ID)
			plaintext = ""
		}
		return Outcome{
			Verdict: VerdictPass, Detail: detail,
			Target:     Target{Kind: "access-token", ID: proof.ID, Revision: proof.CreatedAt.UnixNano(), Scope: strings.Join(proof.Capabilities, ",")},
			Proof:      Known(fmt.Sprintf("active %s token %s", proof.Kind, proof.ID), "internal/auth.Manager.ListTokens"),
			SecretOnce: plaintext,
		}, nil
	}
}

func (s *ControlSource) executeRevokeToken(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.Authority.RevokeAccessToken(ctx, req.Target.ID); err != nil {
		return refusal("the access token could not be revoked", err, req.Target, "internal/auth")
	}
	records, err := s.Authority.TokenMetadata(ctx)
	if err != nil {
		return unproven(req.Target, req.Target.ID, err)
	}
	for _, record := range records {
		if record.ID == req.Target.ID && record.Revoked {
			return Outcome{Verdict: VerdictPass,
				Detail: fmt.Sprintf("token %s revoked", record.ID),
				Target: Target{Kind: "access-token", ID: record.ID, Revision: record.CreatedAt.UnixNano(), Scope: strings.Join(record.Capabilities, ",")},
				Proof:  Known("revoked", "internal/auth.Manager.ListTokens")}, nil
		}
	}
	reason := "token revoke returned but canonical metadata did not prove revocation"
	return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
		Proof: Unknown(reason, "internal/auth.Manager.ListTokens")}, nil
}

func (s *ControlSource) executeWorkspaceExit(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.Authority.RequestWorkspaceExit(ctx); err != nil {
		return refusal("workspace exit could not be requested", err, req.Target, "internal/tui.Workspace")
	}
	if !s.Authority.WorkspaceExitRequested() {
		return unproven(req.Target, s.SessionID, errors.New("workspace loop did not record the exit request"))
	}
	return Outcome{Verdict: VerdictPass,
		Detail: "workspace exit requested; durable session state remains available",
		Target: Target{Kind: "workspace-session", ID: s.SessionID, Digest: "exit-requested"},
		Proof:  Known("exit-requested", "internal/tui.Workspace")}, nil
}

// executeGCArtifacts collects eligible artifacts.
//
// The dry run runs first so the outcome can say what would be removed, and the
// collection proves itself by the eligible count falling.
func (s *ControlSource) executeGCArtifacts(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	planned, err := s.Authority.GCArtifacts(ctx, true)
	if err != nil {
		return refusal("eligible artifacts could not be identified", err, req.Target,
			"internal/app/runtime.go GCArtifacts")
	}
	if planned == 0 {
		reason := "no artifact is eligible for collection"
		return Outcome{
			Verdict: VerdictBlocked, Detail: reason, Target: req.Target,
			Proof: Blocked(reason, "MARSHAL — COMMUNITY TUI / System",
				"internal/app/runtime.go"),
		}, nil
	}

	removed, err := s.Authority.GCArtifacts(ctx, false)
	if err != nil {
		return refusal("the artifacts could not be collected", err, req.Target,
			"internal/app/runtime.go GCArtifacts")
	}
	remaining, err := s.Authority.GCArtifacts(ctx, true)
	if err != nil {
		return unproven(req.Target, "the artifact inventory", err)
	}
	if remaining >= planned {
		reason := fmt.Sprintf(
			"collection reported %d removed but %d artifacts remain eligible, "+
				"unchanged from %d before", removed, remaining, planned)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/app/runtime.go"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("%d artifact(s) collected", removed),
		Target:  req.Target,
		Proof:   Known(fmt.Sprintf("%d still eligible", remaining), "internal/app/runtime.go"),
	}, nil
}

func (s *ControlSource) executeApprove(ctx context.Context, req ActionRequest) (Outcome, error) {
	return s.decideApproval(ctx, req, true)
}

func (s *ControlSource) executeReject(ctx context.Context, req ActionRequest) (Outcome, error) {
	return s.decideApproval(ctx, req, false)
}

// decideApproval records a decision through the canonical approval manager.
//
// The manager enforces expiry, the one-shot state transition and the digest
// binding. Nothing here edits an approval payload or marks one consumed, which
// the contract explicitly forbids the UI from doing.
func (s *ControlSource) decideApproval(ctx context.Context, req ActionRequest, approve bool) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	approvalID := req.Target.ID
	if approvalID == "" {
		return Outcome{}, ErrApprovalRequired
	}

	// Reread the approval and confirm the digest still matches what was shown.
	// An approval binds one exact action; if the digest moved, the decision
	// the operator is about to record is for something else.
	current, err := s.Authority.Approval(ctx, approvalID)
	if err != nil {
		return refusal("the approval could not be re-read", err, req.Target, "internal/execution/approvals.go")
	}
	if approvalDigest(current) != req.Target.Digest {
		reason := fmt.Sprintf(
			"the action digest changed from %s to %s since this approval was displayed; "+
				"the decision would apply to a different action",
			shortDigest(req.Target.Digest), shortDigest(approvalDigest(current)))
		return Outcome{
			Verdict: VerdictBlocked, Detail: reason, Target: approvalTarget(current),
			Proof: Blocked(reason, "re-open the approval", "internal/execution/approvals.go"),
		}, ErrStaleTarget
	}
	if current.Status != execution.ApprovalRequested {
		reason := fmt.Sprintf("approval %s is already %s and cannot be decided again",
			approvalID, current.Status)
		return Outcome{
			Verdict: VerdictBlocked, Detail: reason, Target: approvalTarget(current),
			Proof: Blocked(reason, "Control / Approvals / Decision history",
				"internal/execution/approvals.go"),
		}, nil
	}

	_, _, rationale := s.selection()
	if rationale == "" {
		rationale = "decided from the Control screen"
	}
	var decisionErr error
	if bound, ok := s.Authority.(interface {
		DecideApprovalTarget(context.Context, Target, bool, string) error
	}); ok {
		decisionErr = bound.DecideApprovalTarget(ctx, req.Target, approve, rationale)
	} else {
		decisionErr = s.Authority.DecideApproval(ctx, approvalID, approve, s.ApproverID, rationale)
	}
	if err := decisionErr; err != nil {
		return refusal("the decision was not recorded", err, req.Target, "internal/execution/approvals.go")
	}

	// Prove it by rereading the approval's durable status.
	after, err := s.Authority.Approval(ctx, approvalID)
	if err != nil {
		return unproven(req.Target, approvalID, err)
	}
	want := execution.ApprovalApproved
	if !approve {
		want = execution.ApprovalDenied
	}
	if after.Status != want && !(approve && after.Status == execution.ApprovalConsumed) {
		reason := fmt.Sprintf(
			"the decision was submitted but approval %s reads as %s rather than %s",
			approvalID, after.Status, want)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: approvalTarget(after),
			Proof: Unknown(reason, "internal/execution/approvals.go"),
		}, nil
	}
	verb := "approved"
	if !approve {
		verb = "rejected"
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("approval %s %s", approvalID, verb),
		Target:  approvalTarget(after),
		Proof:   Known(string(after.Status), "internal/execution/approvals.go"),
		// The evidence reference is bounded and carries no payload.
		Evidence: fmt.Sprintf("approval:%s digest:%s", approvalID, shortDigest(after.ActionDigest)),
	}, nil
}

func (s *ControlSource) executeCreateCheckpoint(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	_, _, reason := s.selection()
	if reason == "" {
		reason = "manual checkpoint from the Control screen"
	}
	record, err := s.Authority.CreateCheckpoint(ctx, req.Target.ID, "", reason)
	if err != nil {
		return refusal("the checkpoint could not be created", err, req.Target, "internal/execution/checkpoint.go")
	}
	// Prove it is durable by rereading it, not by trusting the return.
	proof, err := s.Authority.Checkpoint(ctx, record.CheckpointID)
	if err != nil {
		return unproven(req.Target, record.CheckpointID, err)
	}
	// The reread must be the checkpoint that was created.
	if proof.CheckpointID != record.CheckpointID {
		reason := fmt.Sprintf("checkpoint %s was created but reads back as %q",
			record.CheckpointID, proof.CheckpointID)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/execution/checkpoint.go"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("checkpoint %s created", proof.CheckpointID),
		Target: Target{Kind: "checkpoint", ID: proof.CheckpointID,
			Revision: proof.CreatedAt.UnixNano(), Digest: proof.StateDigest},
		Proof:    Known(proof.CheckpointID, "internal/execution/checkpoint.go"),
		Evidence: fmt.Sprintf("checkpoint:%s digest:%s", proof.CheckpointID, shortDigest(proof.StateDigest)),
	}, nil
}

// executeRollback restores a checkpoint after verifying what can be verified.
//
// The canonical record binds both its metadata and the complete captured file
// tree. Control verifies the binding it displayed, then the checkpoint engine
// recomputes the content digest before copying a byte into the live workspace.
func (s *ControlSource) executeRollback(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}

	// Verify before restoring: reread the durable record and confirm it is the
	// one that was displayed.
	record, err := s.Authority.Checkpoint(ctx, req.Target.ID)
	if err != nil {
		return refusal("the checkpoint could not be verified before restoring", err,
			req.Target, "internal/execution/checkpoint.go")
	}
	if checkpointDigest(record) != req.Target.Digest {
		reason := fmt.Sprintf(
			"checkpoint %s now carries digest %s but %s was displayed; "+
				"the record changed and restoring it would not restore what was reviewed",
			record.CheckpointID, shortDigest(checkpointDigest(record)), shortDigest(req.Target.Digest))
		return Outcome{
			Verdict: VerdictBlocked, Detail: reason, Target: req.Target,
			Proof: Blocked(reason, "re-open the checkpoint", "internal/execution/checkpoint.go"),
		}, ErrStaleTarget
	}
	if record.WorktreePath == "" {
		reason := fmt.Sprintf(
			"checkpoint %s records no snapshot path, so there is nothing to restore",
			record.CheckpointID)
		return Outcome{
			Verdict: VerdictBlocked, Detail: reason, Target: req.Target,
			Proof: Blocked(reason, "Control / Checkpoints", "internal/execution/checkpoint.go"),
		}, nil
	}
	if record.SnapshotDigest == "" {
		reason := fmt.Sprintf(
			"checkpoint %s has no snapshot content digest and cannot be restored safely",
			record.CheckpointID)
		return Outcome{Verdict: VerdictBlocked, Detail: reason, Target: req.Target,
			Proof: Blocked(reason, "create a new checkpoint", "internal/execution/checkpoint.go")}, nil
	}

	// A restore overwrites the project root. Doing that while a run is still
	// executing would rewrite files under a worker's feet, so it is refused
	// rather than raced.
	if runID, runErr := s.Authority.CurrentRunID(ctx); runErr == nil && runID != "" {
		if run, getErr := s.Authority.GetRun(ctx, runID); getErr == nil && !run.State.IsTerminal() {
			active := 0
			for _, task := range run.Tasks {
				if task.State == execution.TaskRunning || task.State == execution.TaskWaitingTool {
					active++
				}
			}
			if active > 0 {
				reason := fmt.Sprintf(
					"run %s has %d task(s) still executing; restoring the workspace "+
						"now would overwrite files a worker is using. Cancel the run "+
						"or let it settle first",
					run.RunID, active)
				return Outcome{
					Verdict: VerdictBlocked, Detail: reason, Target: req.Target,
					Proof: Blocked(reason,
						"MARSHAL — COMMUNITY TUI / Control / Execution / Cancel task",
						"internal/execution"),
				}, nil
			}
		}
	}

	restored, err := s.Authority.Rollback(ctx, req.Target.ID)
	if err != nil {
		return refusal("the rollback failed", err, req.Target, "internal/execution/checkpoint.go")
	}
	proof, err := s.Authority.Checkpoint(ctx, restored.CheckpointID)
	if err != nil {
		return unproven(req.Target, restored.CheckpointID, err)
	}
	// A restore proves itself by the record carrying a RestoredAt stamp.
	if proof.RestoredAt == nil {
		reason := fmt.Sprintf(
			"the rollback was accepted but checkpoint %s records no restore time, "+
				"so the workspace state cannot be confirmed", proof.CheckpointID)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/execution/checkpoint.go"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail: fmt.Sprintf("workspace restored to checkpoint %s after canonical "+
			"metadata and snapshot-content integrity verification", proof.CheckpointID),
		Target: Target{Kind: "checkpoint", ID: proof.CheckpointID,
			Revision: proof.CreatedAt.UnixNano(), Digest: checkpointDigest(proof)},
		Proof:    Known(proof.RestoredAt.UTC().Format(time.RFC3339), "internal/execution/checkpoint.go"),
		Evidence: fmt.Sprintf("checkpoint:%s restored", proof.CheckpointID),
	}, nil
}

func (s *ControlSource) executeRollbackCanary(ctx context.Context, req ActionRequest) (Outcome, error) {
	current, err := s.Authority.Canary(ctx, req.Target.ID)
	if err != nil {
		return refusal("the canary could not be re-read", err, req.Target, "internal/optimization")
	}
	if canaryDigest(current) != req.Target.Digest {
		return Outcome{Verdict: VerdictBlocked, Detail: ErrStaleTarget.Error(), Target: req.Target,
			Proof: Blocked("the canary changed after confirmation", "re-open the rollback", "internal/optimization")}, ErrStaleTarget
	}
	rolled, err := s.Authority.RollbackCanary(ctx, req.Target.ID, "operator rollback from Control")
	if err != nil {
		return refusal("the canary rollback failed", err, req.Target, "internal/optimization")
	}
	proof, err := s.Authority.Canary(ctx, rolled.ID)
	if err != nil {
		return unproven(req.Target, rolled.ID, err)
	}
	if proof.State != optimization.CanaryRolledBack {
		return Outcome{Verdict: VerdictUnknown, Detail: "the canary does not read back as rolled back", Target: req.Target,
			Proof: Unknown(string(proof.State), "internal/optimization")}, nil
	}
	return Outcome{Verdict: VerdictPass, Detail: fmt.Sprintf("canary %s rolled back", proof.ID),
		Target: Target{Kind: "canary", ID: proof.ID, Digest: canaryDigest(proof)},
		Proof:  Known(string(proof.State), "internal/optimization")}, nil
}

// refusal builds the outcome for a canonical authority declining a request.
func refusal(what string, err error, target Target, source string) (Outcome, error) {
	reason := fmt.Sprintf("%s: %s", what, err)
	return Outcome{
		Verdict: VerdictFail,
		Detail:  reason,
		Target:  target,
		Proof:   Errored(reason, source),
	}, err
}

// unproven builds the outcome for a call that returned but could not be proved.
//
// This is the case the contract cares most about: the mutation may or may not
// have applied, and the only honest report is that MARSHAL does not know.
func unproven(target Target, id string, err error) (Outcome, error) {
	reason := fmt.Sprintf(
		"the request was accepted but %s could not be re-read to prove it: %s", id, err)
	return Outcome{
		Verdict: VerdictUnknown,
		Detail:  reason,
		Target:  target,
		Proof:   Unknown(reason, "durable state"),
	}, nil
}
