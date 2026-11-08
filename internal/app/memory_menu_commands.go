package app

// Operator memory commands behind the advanced-menu (Ctrl+N) memory entries:
// extract a candidate from a run, on-demand post-run reflection, set and
// compare-and-swap a task working slot, grant or revoke task-memory access,
// attach a content-addressed artifact reference, and compile a memory handoff
// without submitting it.
//
// Every mutation derives its operator identity from OpenLocalControl and is
// gated by an operation-scoped capability through memoryCommand; the surface
// never supplies an actor, a role, a run binding or evidence. Content the
// operator types (a candidate's title and body, a slot value) is recorded as
// an untrusted candidate or working slot, never as durable knowledge:
// promotion stays the separate reviewed operation.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/artifact"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/memory/artifactref"
	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/memory/writeback"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

// memoryMenuLocalControlActions are the scoped capabilities OpenLocalControl
// provisions for these commands, one per operation.
var memoryMenuLocalControlActions = []string{
	"memory.extract", "memory.reflect", "memory.slot-set", "memory.slot-cas",
	"memory.task-access", "memory.attach-artifact",
}

// MemoryExtractCommand is an operator-typed candidate bound to a Process 05
// run. The run, its version and the evidence are read by the runtime; only
// Kind, Title and Body come from the operator.
type MemoryExtractCommand struct {
	RunID  string           `json:"run_id"`
	TaskID string           `json:"task_id,omitempty"`
	Kind   model.MemoryKind `json:"kind"`
	Title  string           `json:"title"`
	Body   string           `json:"body"`
}

// boundRun rereads the run the envelope names and refuses a stale version.
func (r *Runtime) boundRun(ctx context.Context, e CommandEnvelope, runID string) (execution.ExecutionRun, error) {
	if strings.TrimSpace(runID) == "" || runID != e.TargetID {
		return execution.ExecutionRun{}, model.ErrInvalid
	}
	service := r.Execution()
	if service == nil {
		return execution.ExecutionRun{}, fmt.Errorf("%w: execution service", model.ErrUnavailable)
	}
	run, err := service.GetRun(ctx, runID)
	if err != nil {
		return execution.ExecutionRun{}, err
	}
	if run.Version != e.ExpectedVersion {
		return execution.ExecutionRun{}, fmt.Errorf("%w: run %s is at version %d, reviewed %d", model.ErrConflict, runID, run.Version, e.ExpectedVersion)
	}
	return run, nil
}

// CommandExtractMemoryCandidate records an operator-typed candidate bound to
// the exact run (and optionally one of its tasks) the operator reviewed.
func (r *Runtime) CommandExtractMemoryCandidate(ctx context.Context, e CommandEnvelope, req MemoryExtractCommand) (model.MemoryRecordV2, error) {
	req.Title, req.Body, req.TaskID = strings.TrimSpace(req.Title), strings.TrimSpace(req.Body), strings.TrimSpace(req.TaskID)
	if req.Title == "" || req.Body == "" || !req.Kind.IsValid() || req.Kind == model.MemoryKindWorking {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: kind, title and body are required; working memory is set through a slot", model.ErrInvalid)
	}
	_, p, _, err := r.memoryCommand(ctx, e, "memory.extract", req)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	run, err := r.boundRun(ctx, e, req.RunID)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	extract := ExtractCandidateRequest{
		ProjectID: r.ProjectID(), RunID: run.RunID, SessionID: e.SessionID,
		Kind: req.Kind, Title: req.Title, Body: req.Body, Scope: model.ScopeProject, ScopeID: r.ProjectID(),
		ExtMeta: map[string]any{"extracted_from": "run", "run_version": run.Version},
	}
	if req.TaskID != "" {
		task, ok := run.Tasks[req.TaskID]
		if !ok {
			return model.MemoryRecordV2{}, fmt.Errorf("%w: task %s is not part of run %s", model.ErrNotFound, req.TaskID, run.RunID)
		}
		extract.TaskID = task.TaskID
		extract.EvidenceIDs = append([]string(nil), task.CollectedEvidence...)
		extract.HeadCommit = task.ResultCommit
		extract.WorktreeID = task.WorktreePath
		extract.ExtMeta["task_id"] = task.TaskID
	}
	return r.memoryService.ExtractCandidate(ctx, p, extract)
}

// CommandReflectRunTask performs the post-run reflection for one terminal
// task of the reviewed run on demand. Every word of the candidate comes from
// canonical run and verification facts through writeback.Reflector; a
// completed task is reflected as a success only when Process 06 verified the
// run, otherwise the reflection would claim an unverified success.
func (r *Runtime) CommandReflectRunTask(ctx context.Context, e CommandEnvelope, runID, taskID string) (model.MemoryRecordV2, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: task is required", model.ErrInvalid)
	}
	_, p, _, err := r.memoryCommand(ctx, e, "memory.reflect", struct{ Run, Task string }{runID, taskID})
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	run, err := r.boundRun(ctx, e, runID)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	task, ok := run.Tasks[taskID]
	if !ok {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: task %s is not part of run %s", model.ErrNotFound, taskID, run.RunID)
	}
	outcome := writeback.RunOutcome{TaskID: task.TaskID, ProjectID: r.ProjectID(), CommitSHA: task.ResultCommit}
	switch task.State {
	case execution.TaskFailed, execution.TaskBlocked:
		outcome.Status = "FAILED"
		outcome.ErrorMessage = strings.TrimSpace(string(task.State) + ": " + task.LastFailureReason)
	case execution.TaskCancelled:
		outcome.Status = "CANCELLED"
	case execution.TaskCompletedPendingVerify:
		service := r.Verification()
		session, verr := service.CurrentForRun(ctx, run.RunID)
		if verr != nil && !errors.Is(verr, model.ErrNotFound) && !errors.Is(verr, verification.ErrNotFound) {
			return model.MemoryRecordV2{}, verr
		}
		if verr != nil || session.State != verification.VerifiedComplete || session.Binding.RunID != run.RunID {
			state := "NOT_RUN"
			if verr == nil {
				state = string(session.State)
			}
			return model.MemoryRecordV2{}, fmt.Errorf("%w: task %s completed but Process 06 has not verified run %s (%s); reflection would claim an unverified success", model.ErrInvalid, task.TaskID, run.RunID, state)
		}
		outcome.Status = "SUCCESS"
		outcome.VerificationIDs = []string{session.ID}
	default:
		return model.MemoryRecordV2{}, fmt.Errorf("%w: task %s is %s; reflection runs on a terminal task", model.ErrInvalid, task.TaskID, task.State)
	}
	reflected, err := writeback.NewReflector().ReflectAndWriteback(ctx, outcome)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	evidence := append(append([]string(nil), task.CollectedEvidence...), outcome.VerificationIDs...)
	meta := map[string]any{"source_kind": reflected.Source.Kind, "run_version": run.Version}
	for k, v := range reflected.ExtMeta {
		meta[k] = v
	}
	return r.memoryService.ExtractCandidate(ctx, p, ExtractCandidateRequest{
		ProjectID: r.ProjectID(), TaskID: task.TaskID, RunID: run.RunID, SessionID: e.SessionID,
		Kind: reflected.Kind, Title: reflected.Title, Body: reflected.Body,
		Scope: model.ScopeProject, ScopeID: r.ProjectID(), EvidenceIDs: evidence,
		HeadCommit: task.ResultCommit, WorktreeID: task.WorktreePath, ExtMeta: meta,
	})
}

func validSlotType(slot working.SlotType) bool {
	switch slot {
	case working.SlotHypothesis, working.SlotPlanState, working.SlotActiveSymbols, working.SlotBlockers,
		working.SlotTemporaryObservations, working.SlotToolResults, working.SlotFinding, working.SlotDecision,
		working.SlotConstraint, working.SlotFailedApproach, working.SlotArtifactReference,
		working.SlotOpenQuestion, working.SlotHandoffNote:
		return true
	}
	return false
}

// CommandSetMemorySlot creates a task working slot. The task must exist; an
// existing slot is a CAS conflict, never an overwrite.
func (r *Runtime) CommandSetMemorySlot(ctx context.Context, e CommandEnvelope, taskID string, slot working.SlotType, value string) (working.WorkingSlot, error) {
	if !validSlotType(slot) || strings.TrimSpace(value) == "" || taskID == "" || taskID != e.TargetID {
		return working.WorkingSlot{}, model.ErrInvalid
	}
	_, p, _, err := r.memoryCommand(ctx, e, "memory.slot-set", struct {
		Task  string
		Slot  working.SlotType
		Value string
	}{taskID, slot, value})
	if err != nil {
		return working.WorkingSlot{}, err
	}
	if _, err := r.store.GetTask(ctx, taskID); err != nil {
		return working.WorkingSlot{}, err
	}
	return r.memoryService.SetTaskSlotWithProvenance(ctx, p, r.ProjectID(), taskID, slot, value, false, WorkingProvenance{Provider: "operator", SessionID: e.SessionID})
}

// CommandUpdateMemorySlotCAS replaces a slot value only at the revision the
// operator reviewed. The envelope names the slot record and its memory
// revision (see ReviewMemorySlot); the store writes the command receipt in
// the same transaction, so a replay returns the same slot.
func (r *Runtime) CommandUpdateMemorySlotCAS(ctx context.Context, e CommandEnvelope, review SlotPromotionReview, value string) (working.WorkingSlot, error) {
	if !validSlotType(review.SlotType) || strings.TrimSpace(value) == "" || review.SourceID != e.TargetID || review.Revision != e.ExpectedVersion || review.TaskID == "" {
		return working.WorkingSlot{}, model.ErrInvalid
	}
	cctx, p, command, err := r.memoryCommand(ctx, e, "memory.slot-cas", struct {
		Review SlotPromotionReview
		Value  string
	}{review, value})
	if err != nil {
		return working.WorkingSlot{}, err
	}
	if taskSlotID(r.ProjectID(), review.TaskID, string(review.SlotType)) != review.SourceID {
		return working.WorkingSlot{}, model.ErrInvalid
	}
	current := func() (working.WorkingSlot, error) {
		slots, err := r.memoryService.ListTaskSlots(ctx, p, r.ProjectID(), review.TaskID)
		if err != nil {
			return working.WorkingSlot{}, err
		}
		for _, s := range slots {
			if s.Type == review.SlotType {
				return s, nil
			}
		}
		return working.WorkingSlot{}, working.ErrSlotNotFound
	}
	if version, found, err := r.store.MemoryCommandResult(ctx, command); err != nil {
		return working.WorkingSlot{}, err
	} else if found {
		slot, err := current()
		if err != nil {
			return slot, err
		}
		if int64(slot.Revision) != version+1 {
			return slot, model.ErrConflict
		}
		return slot, nil
	}
	return r.memoryService.UpdateTaskSlotCASWithProvenance(cctx, p, r.ProjectID(), review.TaskID, review.SlotType, int(review.Revision)+1, value, WorkingProvenance{Provider: "operator", SessionID: e.SessionID})
}

// TaskMemoryGrantID is the deterministic binding a task-memory grant creates.
func (r *Runtime) TaskMemoryGrantID(taskID, principalID string) string {
	return taskMemoryGrantID(strings.TrimSpace(taskID), strings.TrimSpace(principalID))
}

// CommandTaskMemoryAccess grants or revokes one agent's access to one task's
// memory. The policy digest is the runtime's own memory policy digest, never
// a surface value; revocation touches only the exact task-memory binding.
func (r *Runtime) CommandTaskMemoryAccess(ctx context.Context, e CommandEnvelope, taskID, principalID string, grant bool) (authz.RoleBinding, error) {
	taskID, principalID = strings.TrimSpace(taskID), strings.TrimSpace(principalID)
	if taskID == "" || principalID == "" || taskID != e.TargetID {
		return authz.RoleBinding{}, model.ErrInvalid
	}
	_, p, _, err := r.memoryCommand(ctx, e, "memory.task-access", struct {
		Task, Principal string
		Grant           bool
	}{taskID, principalID, grant})
	if err != nil {
		return authz.RoleBinding{}, err
	}
	if grant {
		return r.memoryService.GrantTaskAccess(ctx, p, TaskMemoryGrantRequest{TaskID: taskID, PrincipalID: principalID, PolicyDigest: "sha256:" + r.memoryPolicyDigest()})
	}
	id := taskMemoryGrantID(taskID, principalID)
	binding, err := r.store.GetRoleBinding(ctx, id)
	if err != nil {
		return authz.RoleBinding{}, err
	}
	if binding.ScopeID != taskID || binding.PrincipalID != principalID {
		return authz.RoleBinding{}, model.ErrConflict
	}
	if binding.RevokedAt == nil {
		if err := r.memoryService.RevokeTaskAccess(ctx, p, id); err != nil {
			return authz.RoleBinding{}, err
		}
	}
	return r.store.GetRoleBinding(ctx, id)
}

// ReviewTaskMemoryAccess reads the exact binding a grant or revoke would touch.
func (r *Runtime) ReviewTaskMemoryAccess(ctx context.Context, taskID, principalID string) (authz.RoleBinding, bool, error) {
	if _, err := r.memoryReviewPrincipal(ctx); err != nil {
		return authz.RoleBinding{}, false, err
	}
	binding, err := r.store.GetRoleBinding(ctx, r.TaskMemoryGrantID(taskID, principalID))
	if errors.Is(err, model.ErrNotFound) {
		return authz.RoleBinding{}, false, nil
	}
	return binding, err == nil, err
}

// CommandAttachMemoryArtifact binds a registered, content-addressed artifact
// to one candidate record at the reviewed revision. The artifact's stored
// bytes are re-hashed first: a missing or mutated payload is not attached.
// Durable records change only through review, so they are refused here.
func (r *Runtime) CommandAttachMemoryArtifact(ctx context.Context, e CommandEnvelope, memoryID, artifactID string) (model.MemoryRecordV2, error) {
	memoryID, artifactID = strings.TrimSpace(memoryID), strings.TrimSpace(artifactID)
	if memoryID == "" || artifactID == "" || memoryID != e.TargetID {
		return model.MemoryRecordV2{}, model.ErrInvalid
	}
	cctx, p, command, err := r.memoryCommand(ctx, e, "memory.attach-artifact", struct{ Memory, Artifact string }{memoryID, artifactID})
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	if version, found, err := r.store.MemoryCommandResult(ctx, command); err != nil {
		return model.MemoryRecordV2{}, err
	} else if found {
		rec, err := r.memoryService.Get(ctx, p, r.ProjectID(), memoryID)
		if err != nil {
			return rec, err
		}
		if rec.Revision != version {
			return rec, model.ErrConflict
		}
		return rec, nil
	}
	rec, err := r.memoryService.Get(ctx, p, r.ProjectID(), memoryID)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	if err := validateMemorySelection(rec, r.ProjectID()); err != nil {
		return model.MemoryRecordV2{}, err
	}
	if rec.Lifecycle != model.MemoryCandidate || rec.Kind == model.MemoryKindWorking {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: only a candidate record takes an artifact reference here; %s is %s", model.ErrInvalid, rec.ID, rec.Lifecycle)
	}
	if rec.Revision != e.ExpectedVersion {
		return model.MemoryRecordV2{}, model.ErrConflict
	}
	stored, err := r.store.GetArtifact(ctx, artifactID)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	if stored.ProjectID != r.ProjectID() && stored.ProjectID != r.ProjectIdentity() {
		return model.MemoryRecordV2{}, authz.ErrDenied
	}
	if state, verr := artifact.Verify(r.layout.Artifacts, stored); state != artifact.PayloadVerified {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: artifact %s payload is %s: %v", model.ErrConflict, artifactID, state, verr)
	}
	refs := artifactref.NewManager()
	refs.RegisterArtifact(artifactref.ArtifactRef{Digest: stored.Digest, Path: stored.Path, Kind: artifactref.ArtifactKind(stored.Kind), ScopeID: rec.ScopeID})
	bound, err := refs.BindMemoryToArtifact(ctx, rec, stored.Digest)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	updated, err := r.store.UpdateMemory(cctx, r.ProjectID(), rec.ID, rec.Revision, func(m *model.MemoryRecordV2) error {
		if m.ExtMeta == nil {
			m.ExtMeta = map[string]any{}
		}
		var existing []any
		if prior, ok := m.ExtMeta["artifact_refs"].([]any); ok {
			existing = prior
		}
		for _, item := range existing {
			if ref, ok := item.(map[string]any); ok && ref["digest"] == bound.ArtifactDigest {
				return fmt.Errorf("%w: artifact %s is already attached", model.ErrConflict, artifactID)
			}
		}
		m.ExtMeta["artifact_refs"] = append(existing, map[string]any{
			"artifact_id": stored.ID, "digest": bound.ArtifactDigest, "kind": stored.Kind,
			"source_commit": stored.SourceCommit, "size_bytes": stored.Size,
		})
		return nil
	})
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	if err := r.memoryService.IndexRecord(ctx, updated); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("index memory with artifact reference: %w", err)
	}
	return updated, nil
}

// CompileMemoryHandoff compiles the provider-neutral handoff bundle for one
// task as the local operator and returns it. Nothing is submitted, delivered
// or persisted: CompileAndSubmitHandoff remains the task-handover path.
func (r *Runtime) CompileMemoryHandoff(ctx context.Context, taskID, targetRole string) (HandoffBundle, error) {
	p, err := r.memoryReviewPrincipal(ctx)
	if err != nil {
		return HandoffBundle{}, err
	}
	return r.memoryService.CompileHandoff(ctx, p, HandoffCompileRequest{
		ProjectID: r.ProjectID(), TaskID: strings.TrimSpace(taskID), SourceAgentID: p.ID,
		TargetRole: strings.TrimSpace(targetRole),
	})
}
