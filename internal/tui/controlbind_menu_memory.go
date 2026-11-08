package tui

// Advanced-menu memory bindings (TUI3-0017): extract a candidate from a run,
// on-demand post-run reflection, set and compare-and-swap a task working
// slot, grant or revoke task-memory access, attach a content-addressed
// artifact reference, and compile a memory handoff without submitting it.
//
// Each binding collects a closed typed form, binds the exact canonical target
// at preparation, submits through one Runtime command and proves the result
// by rereading durable state. These entries supersede the gap entries of the
// same IDs in controlbind_bindings.go because menuMemoryBindings is merged
// after them.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
)

// MenuMemoryAuthority is optional, like MemoryActionAuthority: an authority
// that does not hold an authenticated local operator does not acquire these
// writes by implementing ControlAuthority.
type MenuMemoryAuthority interface {
	ReviewMemoryActionRecord(context.Context, string) (model.MemoryRecordV2, error)
	ReviewMemoryActionSlot(context.Context, string, working.SlotType) (app.SlotPromotionReview, error)
	ExtractMemoryCandidateAction(context.Context, app.CommandEnvelope, app.MemoryExtractCommand) (model.MemoryRecordV2, error)
	ReflectRunTaskAction(context.Context, app.CommandEnvelope, string, string) (model.MemoryRecordV2, error)
	SetMemorySlotAction(context.Context, app.CommandEnvelope, string, working.SlotType, string) (working.WorkingSlot, error)
	UpdateMemorySlotCASAction(context.Context, app.CommandEnvelope, app.SlotPromotionReview, string) (working.WorkingSlot, error)
	ReviewTaskMemoryAccessAction(context.Context, string, string) (authz.RoleBinding, bool, error)
	TaskMemoryAccessAction(context.Context, app.CommandEnvelope, string, string, bool) (authz.RoleBinding, error)
	AttachMemoryArtifactAction(context.Context, app.CommandEnvelope, string, string) (model.MemoryRecordV2, error)
	CompileMemoryHandoffAction(context.Context, string, string) (app.HandoffBundle, error)
}

func menuMemoryBindings(s *ControlSource) map[ActionID]Binding {
	out := map[ActionID]Binding{
		"CTUI-0425": menuExtractBinding(s),
		"CTUI-0429": menuAttachArtifactBinding(s),
		"CTUI-0430": menuReflectBinding(s),
		"CTUI-0433": menuSetSlotBinding(s),
		"CTUI-0434": menuCASSlotBinding(s),
		"CTUI-0436": menuTaskAccessBinding(s),
		"CTUI-0439": menuHandoffBinding(s),
	}
	if _, ok := s.Authority.(MenuMemoryAuthority); !ok {
		for id, b := range out {
			b.Requires = "Process 07 memory writes need the authenticated local operator authority, which this workspace does not hold"
			b.Execute = nil
			out[id] = b
		}
	}
	// These stay unavailable in every workspace: the backend they name does
	// not hold durable state to act on, and binding them would report an
	// effect that never happened. Each needs an owner decision first.
	for id, gap := range menuMemoryOwnerGaps {
		out[id] = Binding{Action: id, Title: gap.title, Safety: gap.safety, Requires: gap.cause}
	}
	return out
}

var menuMemoryOwnerGaps = map[ActionID]struct {
	title, cause string
	safety       Safety
}{
	"CTUI-0457": {"Retention and hard purge", "a hard purge destroys a record irrecoverably, and the only purge code " +
		"(memory/retention PurgeManager.HardPurge) drops derived index entries into an in-memory ledger while the " +
		"canonical memory row stays; no governed store purge or retention-policy authority exists, so this needs an " +
		"owner decision on who may purge before it is wired", SafetyDestructive},
	"CTUI-0458": {"Protected-record encryption and key rotation", "memory/encryption Vault is an in-memory AES-GCM " +
		"helper fed caller-supplied key bytes; no memory record is stored encrypted and no key custody exists outside " +
		"UI state, so key rotation needs an owner decision on key storage before it is wired", SafetyGoverned},
	"CTUI-0476": {"Governed branch merge", "the memory/versioning branch engine is a stub: RecordBranchWrite stores " +
		"nothing and MergeBranch returns a fixed record list, so no durable memory branch exists to merge; wiring it " +
		"would report a merge that never happened", SafetyGoverned},
}

func menuMemoryAuthority(s *ControlSource) (MenuMemoryAuthority, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	a, ok := s.Authority.(MenuMemoryAuthority)
	if !ok {
		return nil, ErrNoBinding
	}
	return a, nil
}

func validateCandidateKind(v string) error {
	kind := model.MemoryKind(strings.TrimSpace(v))
	if !kind.IsValid() || kind == model.MemoryKindWorking {
		return errors.New("enter a memory kind such as semantic, procedural, decision or finding (not working)")
	}
	return nil
}

func validateSlotType(v string) error {
	switch working.SlotType(strings.TrimSpace(v)) {
	case working.SlotHypothesis, working.SlotPlanState, working.SlotActiveSymbols, working.SlotBlockers,
		working.SlotTemporaryObservations, working.SlotToolResults, working.SlotFinding, working.SlotDecision,
		working.SlotConstraint, working.SlotFailedApproach, working.SlotArtifactReference,
		working.SlotOpenQuestion, working.SlotHandoffNote:
		return nil
	}
	return errors.New("enter a slot type: hypothesis, plan_state, blockers, finding, decision, constraint, failed_approach, open_question, handoff_note, ...")
}

func validatePositiveInt(v string) error {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err != nil || n <= 0 {
		return errors.New("enter the positive slot revision shown on the slot")
	}
	return nil
}

func validateGrantOperation(v string) error {
	if v := strings.TrimSpace(v); v != "grant" && v != "revoke" {
		return errors.New("enter grant or revoke")
	}
	return nil
}

// currentRunTarget binds the current Process 05 run at its version. When a
// task is named it must belong to the run.
func (s *ControlSource) currentRunTarget(ctx context.Context, taskID string) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	runID, err := s.Authority.CurrentRunID(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("read current run: %w", err)
	}
	if runID == "" {
		return Target{}, errors.New("no Process 05 run is active in this session")
	}
	run, err := s.Authority.GetRun(ctx, runID)
	if err != nil {
		return Target{}, fmt.Errorf("read current run: %w", err)
	}
	state := ""
	if taskID != "" {
		task, ok := run.Tasks[taskID]
		if !ok {
			return Target{}, fmt.Errorf("task %s is not part of current run %s", taskID, runID)
		}
		state = string(task.State)
	}
	digest, err := digestTarget(struct {
		Run     string
		Version int64
		Task    string
		State   string
	}{run.RunID, run.Version, taskID, state})
	if err != nil {
		return Target{}, err
	}
	summary := fmt.Sprintf("run %s at version %d", run.RunID, run.Version)
	if taskID != "" {
		summary += fmt.Sprintf("; task %s is %s", taskID, state)
	}
	return Target{Kind: "run", ID: run.RunID, Revision: run.Version, Digest: digest,
		Scope: fmt.Sprintf("project memory for %s", s.ProjectID), Summary: summary}, nil
}

func (s *ControlSource) taskTarget(ctx context.Context, taskID, kind, summary string) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	task, err := s.Authority.Task(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return Target{}, fmt.Errorf("read task %s: %w", taskID, err)
	}
	digest, err := digestTarget(struct{ ID, Title, Status string }{task.ID, task.Title, string(task.Status)})
	if err != nil {
		return Target{}, err
	}
	return Target{Kind: kind, ID: task.ID, Digest: digest, Scope: "task " + task.ID, Summary: summary}, nil
}

func memoryRecordOutcome(rec model.MemoryRecordV2, detail, source string) Outcome {
	return memorySuccess(Target{Kind: "memory", ID: rec.ID, Revision: rec.Revision, Digest: rec.ContentDigest, Scope: rec.Scope + ":" + rec.ScopeID, Summary: rec.Title}, detail, rec.ContentDigest, source)
}

// --- CTUI-0425 Extract candidate from run ---

func menuExtractBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0425", Title: "Extract candidate from run or handoff", Safety: SafetyPlain,
		Inputs: []InputSpec{
			{Key: "kind", Label: "Candidate kind", Default: "semantic", Required: true, Validate: validateCandidateKind},
			{Key: "title", Label: "Candidate title", Required: true},
			{Key: "body", Label: "What the run showed", Required: true},
			{Key: "task_id", Label: "Task in the current run (optional)", Advanced: true},
		},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			return s.currentRunTarget(ctx, strings.TrimSpace(req.Inputs["task_id"]))
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			a, err := menuMemoryAuthority(s)
			if err != nil {
				return Outcome{}, err
			}
			rec, err := a.ExtractMemoryCandidateAction(ctx, memoryEnvelope(s, req), app.MemoryExtractCommand{
				RunID: req.Target.ID, TaskID: strings.TrimSpace(req.Inputs["task_id"]),
				Kind: model.MemoryKind(strings.TrimSpace(req.Inputs["kind"])), Title: req.Inputs["title"], Body: req.Inputs["body"],
			})
			if err != nil {
				return refusal("the candidate could not be extracted", err, req.Target, "Runtime.CommandExtractMemoryCandidate")
			}
			proof, err := a.ReviewMemoryActionRecord(ctx, rec.ID)
			if err != nil {
				return unproven(req.Target, rec.ID, err)
			}
			if proof.RunID != req.Target.ID || proof.Lifecycle != model.MemoryCandidate || proof.Authority != model.AuthorityAgent || proof.Title != strings.TrimSpace(req.Inputs["title"]) {
				reason := fmt.Sprintf("candidate %s did not reread as an untrusted candidate bound to run %s", rec.ID, req.Target.ID)
				return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/store/memory.go")}, nil
			}
			return memoryRecordOutcome(proof, fmt.Sprintf("candidate %s extracted from run %s; durable promotion requires separate review", proof.ID, req.Target.ID), "MemoryService.ExtractCandidate"), nil
		}}
}

// --- CTUI-0430 Post-run reflection and writeback ---

func menuReflectBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0430", Title: "Post-run reflection and writeback", Safety: SafetyGoverned,
		Inputs: []InputSpec{{Key: "task_id", Label: "Terminal task in the current run", Required: true}},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			return s.currentRunTarget(ctx, strings.TrimSpace(req.Inputs["task_id"]))
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			a, err := menuMemoryAuthority(s)
			if err != nil {
				return Outcome{}, err
			}
			taskID := strings.TrimSpace(req.Inputs["task_id"])
			rec, err := a.ReflectRunTaskAction(ctx, memoryEnvelope(s, req), req.Target.ID, taskID)
			if err != nil {
				return refusal("the reflection could not be written back", err, req.Target, "Runtime.CommandReflectRunTask")
			}
			proof, err := a.ReviewMemoryActionRecord(ctx, rec.ID)
			if err != nil {
				return unproven(req.Target, rec.ID, err)
			}
			if proof.RunID != req.Target.ID || proof.Lifecycle != model.MemoryCandidate {
				reason := fmt.Sprintf("reflection %s did not reread as a candidate bound to run %s", rec.ID, req.Target.ID)
				return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/store/memory.go")}, nil
			}
			return memoryRecordOutcome(proof, fmt.Sprintf("reflection %s written back for task %s as a candidate: %s", proof.ID, taskID, proof.Title), "writeback.Reflector + MemoryService.ExtractCandidate"), nil
		}}
}

// --- CTUI-0433 Set slot ---

func menuSetSlotBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0433", Title: "Set slot", Safety: SafetyGoverned,
		Inputs: []InputSpec{
			{Key: "task_id", Label: "Exact task ID", Required: true},
			{Key: "slot_type", Label: "Working slot type", Required: true, Validate: validateSlotType},
			{Key: "value", Label: "Slot value", Required: true},
		},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			a, err := menuMemoryAuthority(s)
			if err != nil {
				return Target{}, err
			}
			slot := working.SlotType(strings.TrimSpace(req.Inputs["slot_type"]))
			if review, err := a.ReviewMemoryActionSlot(ctx, strings.TrimSpace(req.Inputs["task_id"]), slot); err == nil {
				return Target{}, fmt.Errorf("slot %s already exists at revision %d; use Compare-and-swap update", slot, review.Revision+1)
			} else if !errors.Is(err, model.ErrNotFound) {
				return Target{}, err
			}
			return s.taskTarget(ctx, req.Inputs["task_id"], "task", fmt.Sprintf("create working slot %s on task %s", slot, strings.TrimSpace(req.Inputs["task_id"])))
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			a, err := menuMemoryAuthority(s)
			if err != nil {
				return Outcome{}, err
			}
			slot := working.SlotType(strings.TrimSpace(req.Inputs["slot_type"]))
			got, err := a.SetMemorySlotAction(ctx, memoryEnvelope(s, req), req.Target.ID, slot, req.Inputs["value"])
			if err != nil {
				return refusal("the slot could not be set", err, req.Target, "MemoryService.SetTaskSlotWithProvenance")
			}
			review, err := a.ReviewMemoryActionSlot(ctx, req.Target.ID, slot)
			if err != nil {
				return unproven(req.Target, string(slot), err)
			}
			if int64(got.Revision) != review.Revision+1 {
				return unproven(req.Target, review.SourceID, ErrStaleTarget)
			}
			return memorySuccess(Target{Kind: "memory-slot", ID: review.SourceID, Revision: review.Revision, Digest: review.Digest, Scope: "task " + req.Target.ID, Summary: string(slot)},
				fmt.Sprintf("slot %s on task %s set at revision %d", slot, req.Target.ID, got.Revision), review.Digest, "MemoryService.SetTaskSlotWithProvenance"), nil
		}}
}

// --- CTUI-0434 Compare-and-swap update ---

func menuCASSlotBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0434", Title: "Compare-and-swap update", Safety: SafetyGoverned,
		Inputs: []InputSpec{
			{Key: "task_id", Label: "Exact task ID", Required: true},
			{Key: "slot_type", Label: "Working slot type", Required: true, Validate: validateSlotType},
			{Key: "expected_revision", Label: "Expected slot revision", Required: true, Validate: validatePositiveInt},
			{Key: "value", Label: "New slot value", Required: true},
		},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			a, err := menuMemoryAuthority(s)
			if err != nil {
				return Target{}, err
			}
			taskID, slot := strings.TrimSpace(req.Inputs["task_id"]), working.SlotType(strings.TrimSpace(req.Inputs["slot_type"]))
			review, err := a.ReviewMemoryActionSlot(ctx, taskID, slot)
			if err != nil {
				return Target{}, fmt.Errorf("read slot %s on task %s: %w", slot, taskID, err)
			}
			expected, _ := strconv.Atoi(strings.TrimSpace(req.Inputs["expected_revision"]))
			if int64(expected) != review.Revision+1 {
				return Target{}, fmt.Errorf("%w: slot %s is at revision %d, not %d", ErrStaleTarget, slot, review.Revision+1, expected)
			}
			return Target{Kind: "memory-slot", ID: review.SourceID, Revision: review.Revision, Digest: review.Digest, Scope: "task " + taskID,
				Summary: fmt.Sprintf("replace slot %s on task %s at revision %d", slot, taskID, expected)}, nil
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			a, err := menuMemoryAuthority(s)
			if err != nil {
				return Outcome{}, err
			}
			taskID, slot := strings.TrimSpace(req.Inputs["task_id"]), working.SlotType(strings.TrimSpace(req.Inputs["slot_type"]))
			review := app.SlotPromotionReview{TaskID: taskID, SlotType: slot, SourceID: req.Target.ID, Revision: req.Target.Revision, Digest: req.Target.Digest}
			got, err := a.UpdateMemorySlotCASAction(ctx, memoryEnvelope(s, req), review, req.Inputs["value"])
			if err != nil {
				return refusal("the slot was not replaced", err, req.Target, "MemoryService.UpdateTaskSlotCASWithProvenance")
			}
			after, err := a.ReviewMemoryActionSlot(ctx, taskID, slot)
			if err != nil {
				return unproven(req.Target, req.Target.ID, err)
			}
			if after.Revision != req.Target.Revision+1 || int64(got.Revision) != after.Revision+1 {
				return unproven(req.Target, req.Target.ID, ErrStaleTarget)
			}
			return memorySuccess(Target{Kind: "memory-slot", ID: after.SourceID, Revision: after.Revision, Digest: after.Digest, Scope: req.Target.Scope, Summary: string(slot)},
				fmt.Sprintf("slot %s on task %s replaced; now at revision %d", slot, taskID, got.Revision), after.Digest, "MemoryService.UpdateTaskSlotCASWithProvenance"), nil
		}}
}

// --- CTUI-0436 Grant or revoke task-memory access ---

func menuTaskAccessBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0436", Title: "Grant or revoke task-memory access", Safety: SafetyGoverned,
		Inputs: []InputSpec{
			{Key: "operation", Label: "grant or revoke", Default: "grant", Required: true, Validate: validateGrantOperation},
			{Key: "task_id", Label: "Exact task ID", Required: true},
			{Key: "agent_id", Label: "Exact agent ID", Required: true},
		},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			a, err := menuMemoryAuthority(s)
			if err != nil {
				return Target{}, err
			}
			op, taskID, agentID := strings.TrimSpace(req.Inputs["operation"]), strings.TrimSpace(req.Inputs["task_id"]), strings.TrimSpace(req.Inputs["agent_id"])
			binding, found, err := a.ReviewTaskMemoryAccessAction(ctx, taskID, agentID)
			if err != nil {
				return Target{}, err
			}
			active := found && binding.RevokedAt == nil
			if op == "revoke" && !active {
				return Target{}, fmt.Errorf("agent %s holds no active memory grant on task %s", agentID, taskID)
			}
			if op == "grant" {
				if active {
					return Target{}, fmt.Errorf("agent %s already holds memory grant %s on task %s", agentID, binding.ID, taskID)
				}
				if found {
					return Target{}, fmt.Errorf("task memory grant %s was revoked and is immutable; it is not re-issued", binding.ID)
				}
				agents, err := s.Authority.Agents(ctx)
				if err != nil {
					return Target{}, fmt.Errorf("read agents: %w", err)
				}
				known := false
				for _, agent := range agents {
					known = known || (agent.ID == agentID && agent.Status != model.AgentDisabled)
				}
				if !known {
					return Target{}, fmt.Errorf("agent %s is not an active registered agent", agentID)
				}
			}
			target, err := s.taskTarget(ctx, taskID, "task-memory-grant", fmt.Sprintf("%s agent %s access to task %s memory", op, agentID, taskID))
			if err != nil {
				return Target{}, err
			}
			target.ActorID = agentID
			return target, nil
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			a, err := menuMemoryAuthority(s)
			if err != nil {
				return Outcome{}, err
			}
			grant := strings.TrimSpace(req.Inputs["operation"]) == "grant"
			binding, err := a.TaskMemoryAccessAction(ctx, memoryEnvelope(s, req), req.Target.ID, req.Target.ActorID, grant)
			if err != nil {
				return refusal("task-memory access was not changed", err, req.Target, "MemoryService.GrantTaskAccess/RevokeTaskAccess")
			}
			proof, found, err := a.ReviewTaskMemoryAccessAction(ctx, req.Target.ID, req.Target.ActorID)
			if err != nil {
				return unproven(req.Target, binding.ID, err)
			}
			if !found || proof.ID != binding.ID || (proof.RevokedAt == nil) != grant {
				return unproven(req.Target, binding.ID, ErrStaleTarget)
			}
			verb := "granted"
			if !grant {
				verb = "revoked"
			}
			return memorySuccess(Target{Kind: "role-binding", ID: proof.ID, ActorID: proof.PrincipalID, Scope: "task " + proof.ScopeID, Summary: proof.Role},
				fmt.Sprintf("task-memory access %s: %s on task %s (%s)", verb, proof.PrincipalID, proof.ScopeID, proof.ID), proof.PolicyDigest, "MemoryService.GrantTaskAccess/RevokeTaskAccess"), nil
		}}
}

// --- CTUI-0429 Attach content-addressed artifact reference ---

func menuAttachArtifactBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0429", Title: "Attach content-addressed artifact reference", Safety: SafetyPlain,
		Inputs: []InputSpec{
			{Key: "memory_id", Label: "Exact candidate memory ID", Required: true},
			{Key: "artifact_id", Label: "Exact registered artifact ID", Required: true},
		},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			a, err := menuMemoryAuthority(s)
			if err != nil {
				return Target{}, err
			}
			rec, err := a.ReviewMemoryActionRecord(ctx, strings.TrimSpace(req.Inputs["memory_id"]))
			if err != nil {
				return Target{}, err
			}
			if rec.Lifecycle != model.MemoryCandidate {
				return Target{}, fmt.Errorf("memory %s is %s; an artifact reference is attached to a candidate before review", rec.ID, rec.Lifecycle)
			}
			return Target{Kind: "memory", ID: rec.ID, Revision: rec.Revision, Digest: rec.ContentDigest, Scope: rec.Scope + ":" + rec.ScopeID,
				Summary: fmt.Sprintf("attach artifact %s to candidate %s @%d", strings.TrimSpace(req.Inputs["artifact_id"]), rec.ID, rec.Revision)}, nil
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			a, err := menuMemoryAuthority(s)
			if err != nil {
				return Outcome{}, err
			}
			artifactID := strings.TrimSpace(req.Inputs["artifact_id"])
			rec, err := a.AttachMemoryArtifactAction(ctx, memoryEnvelope(s, req), req.Target.ID, artifactID)
			if err != nil {
				return refusal("the artifact reference was not attached", err, req.Target, "Runtime.CommandAttachMemoryArtifact")
			}
			proof, err := a.ReviewMemoryActionRecord(ctx, rec.ID)
			if err != nil {
				return unproven(req.Target, rec.ID, err)
			}
			attached := false
			if refs, ok := proof.ExtMeta["artifact_refs"].([]any); ok {
				for _, item := range refs {
					if ref, ok := item.(map[string]any); ok && ref["artifact_id"] == artifactID {
						attached = true
					}
				}
			}
			if !attached || proof.Revision != req.Target.Revision+1 {
				return unproven(req.Target, rec.ID, ErrStaleTarget)
			}
			return memoryRecordOutcome(proof, fmt.Sprintf("artifact %s attached to %s at revision %d", artifactID, proof.ID, proof.Revision), "Runtime.CommandAttachMemoryArtifact"), nil
		}}
}

// --- CTUI-0439 Compile provider-neutral memory handoff ---

func menuHandoffBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0439", Title: "Compile provider-neutral memory handoff", Safety: SafetyPlain,
		Inputs: []InputSpec{
			{Key: "task_id", Label: "Exact task ID", Required: true},
			{Key: "target_role", Label: "Receiving role (optional)", Advanced: true},
		},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			return s.taskTarget(ctx, req.Inputs["task_id"], "task", "compile a memory handoff for task "+strings.TrimSpace(req.Inputs["task_id"])+"; nothing is submitted")
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			a, err := menuMemoryAuthority(s)
			if err != nil {
				return Outcome{}, err
			}
			bundle, err := a.CompileMemoryHandoffAction(ctx, req.Target.ID, req.Inputs["target_role"])
			if err != nil {
				return refusal("the memory handoff could not be compiled", err, req.Target, "MemoryService.CompileHandoff")
			}
			if bundle.TaskID != req.Target.ID || bundle.BundleID == "" {
				return unproven(req.Target, bundle.BundleID, ErrStaleTarget)
			}
			detail := fmt.Sprintf("handoff %s for task %s: %d slot(s), %d memory record(s), %d evidence ref(s), %d bytes; not submitted",
				bundle.BundleID, bundle.TaskID, len(bundle.WorkingSlots), len(bundle.MemoryIDs), len(bundle.EvidenceIDs), bundle.ByteSize)
			if len(bundle.FirewallRedacted) > 0 {
				detail += "; redacted: " + strings.Join(bundle.FirewallRedacted, ",")
			}
			return memorySuccess(Target{Kind: "memory-handoff", ID: bundle.BundleID, Scope: "task " + bundle.TaskID, Summary: bundle.TargetRole}, detail, bundle.BundleID, "MemoryService.CompileHandoff"), nil
		}}
}

// --- runtime authority ---

func (a *runtimeControlAuthority) ExtractMemoryCandidateAction(ctx context.Context, e app.CommandEnvelope, req app.MemoryExtractCommand) (model.MemoryRecordV2, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	return a.runtime.CommandExtractMemoryCandidate(ctx, e, req)
}

func (a *runtimeControlAuthority) ReflectRunTaskAction(ctx context.Context, e app.CommandEnvelope, runID, taskID string) (model.MemoryRecordV2, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	return a.runtime.CommandReflectRunTask(ctx, e, runID, taskID)
}

func (a *runtimeControlAuthority) SetMemorySlotAction(ctx context.Context, e app.CommandEnvelope, taskID string, slot working.SlotType, value string) (working.WorkingSlot, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return working.WorkingSlot{}, err
	}
	return a.runtime.CommandSetMemorySlot(ctx, e, taskID, slot, value)
}

func (a *runtimeControlAuthority) UpdateMemorySlotCASAction(ctx context.Context, e app.CommandEnvelope, review app.SlotPromotionReview, value string) (working.WorkingSlot, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return working.WorkingSlot{}, err
	}
	return a.runtime.CommandUpdateMemorySlotCAS(ctx, e, review, value)
}

func (a *runtimeControlAuthority) ReviewTaskMemoryAccessAction(ctx context.Context, taskID, agentID string) (authz.RoleBinding, bool, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return authz.RoleBinding{}, false, err
	}
	return a.runtime.ReviewTaskMemoryAccess(ctx, taskID, agentID)
}

func (a *runtimeControlAuthority) TaskMemoryAccessAction(ctx context.Context, e app.CommandEnvelope, taskID, agentID string, grant bool) (authz.RoleBinding, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return authz.RoleBinding{}, err
	}
	return a.runtime.CommandTaskMemoryAccess(ctx, e, taskID, agentID, grant)
}

func (a *runtimeControlAuthority) AttachMemoryArtifactAction(ctx context.Context, e app.CommandEnvelope, memoryID, artifactID string) (model.MemoryRecordV2, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	return a.runtime.CommandAttachMemoryArtifact(ctx, e, memoryID, artifactID)
}

func (a *runtimeControlAuthority) CompileMemoryHandoffAction(ctx context.Context, taskID, targetRole string) (app.HandoffBundle, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return app.HandoffBundle{}, err
	}
	return a.runtime.CompileMemoryHandoff(ctx, taskID, targetRole)
}
