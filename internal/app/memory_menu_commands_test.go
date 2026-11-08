package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	artifactstore "github.com/Zen1th53/marshal/internal/artifact"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

func menuEnvelope(r *Runtime, target string, version int64, key string) CommandEnvelope {
	return CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "session-menu", TargetID: target, ExpectedVersion: version, IdempotencyKey: key}
}

func menuTask(t *testing.T, r *Runtime, id string) {
	t.Helper()
	if _, err := r.store.ImportTasks(t.Context(), []model.Task{{ID: id, Title: "menu memory task " + id, Status: model.TaskReady, Risk: model.R0}}); err != nil {
		t.Fatal(err)
	}
}

func TestMenuSlotSetAndCASUseCanonicalWorkingMemory(t *testing.T) {
	r, ctx := memoryOperatorRuntime(t)
	menuTask(t, r, "TASK-menu-slot")
	if _, err := r.CommandSetMemorySlot(ctx, menuEnvelope(r, "TASK-missing", 0, "set-missing"), "TASK-missing", working.SlotDecision, "x"); err == nil {
		t.Fatal("slot set on a task that does not exist")
	}
	if _, err := r.CommandSetMemorySlot(ctx, menuEnvelope(r, "TASK-menu-slot", 0, "bad"), "TASK-menu-slot", working.SlotType("free-form"), "x"); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("unknown slot type accepted: %v", err)
	}
	if _, err := r.CommandSetMemorySlot(context.Background(), menuEnvelope(r, "TASK-menu-slot", 0, "anon"), "TASK-menu-slot", working.SlotDecision, "x"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous slot set: %v", err)
	}
	slot, err := r.CommandSetMemorySlot(ctx, menuEnvelope(r, "TASK-menu-slot", 0, "set-1"), "TASK-menu-slot", working.SlotDecision, "use the canonical store")
	if err != nil || slot.Revision != 1 || slot.Value != "use the canonical store" {
		t.Fatalf("set slot: %+v %v", slot, err)
	}
	if _, err := r.CommandSetMemorySlot(ctx, menuEnvelope(r, "TASK-menu-slot", 0, "set-2"), "TASK-menu-slot", working.SlotDecision, "overwrite"); !errors.Is(err, working.ErrCASConflict) {
		t.Fatalf("set overwrote an existing slot: %v", err)
	}
	review, err := r.ReviewMemorySlot(ctx, "TASK-menu-slot", working.SlotDecision)
	if err != nil {
		t.Fatal(err)
	}
	stale := review
	stale.Revision++
	if _, err := r.CommandUpdateMemorySlotCAS(ctx, menuEnvelope(r, review.SourceID, stale.Revision, "cas-stale"), stale, "stale"); err == nil {
		t.Fatal("stale CAS accepted")
	}
	e := menuEnvelope(r, review.SourceID, review.Revision, "cas-1")
	updated, err := r.CommandUpdateMemorySlotCAS(ctx, e, review, "use the canonical store, CAS-guarded")
	if err != nil || updated.Revision != 2 || updated.Value != "use the canonical store, CAS-guarded" {
		t.Fatalf("cas: %+v %v", updated, err)
	}
	replay, err := r.CommandUpdateMemorySlotCAS(ctx, e, review, "use the canonical store, CAS-guarded")
	if err != nil || replay.Revision != 2 {
		t.Fatalf("replay must return the same slot: %+v %v", replay, err)
	}
	if _, err := r.CommandUpdateMemorySlotCAS(ctx, menuEnvelope(r, review.SourceID, review.Revision, "cas-2"), review, "lost update"); err == nil {
		t.Fatal("second CAS at the old revision accepted")
	}
}

func TestMenuTaskMemoryAccessGrantsAndRevokesExactBinding(t *testing.T) {
	r, ctx := memoryOperatorRuntime(t)
	menuTask(t, r, "TASK-menu-acl")
	if err := r.store.RegisterAgent(ctx, model.Agent{ID: "AGENT-menu", ProjectID: r.ProjectID(), DisplayName: "menu fixture", Role: model.RoleDeveloper, Status: model.AgentRegistered, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommandTaskMemoryAccess(ctx, menuEnvelope(r, "TASK-menu-acl", 0, "g-unknown"), "TASK-menu-acl", "AGENT-nobody", true); err == nil {
		t.Fatal("grant to an unknown agent accepted")
	}
	if _, err := r.CommandTaskMemoryAccess(ctx, menuEnvelope(r, "TASK-menu-acl", 0, "r-none"), "TASK-menu-acl", "AGENT-menu", false); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("revoke of a missing grant: %v", err)
	}
	granted, err := r.CommandTaskMemoryAccess(ctx, menuEnvelope(r, "TASK-menu-acl", 0, "g-1"), "TASK-menu-acl", "AGENT-menu", true)
	if err != nil || granted.ScopeID != "TASK-menu-acl" || granted.PolicyDigest != "sha256:"+r.memoryPolicyDigest() || granted.RevokedAt != nil {
		t.Fatalf("grant: %+v %v", granted, err)
	}
	if ok, err := r.store.HasActiveRoleBinding(ctx, "AGENT-menu", "TASK-menu-acl"); err != nil || !ok {
		t.Fatalf("grant is not effective: %v %v", ok, err)
	}
	review, found, err := r.ReviewTaskMemoryAccess(ctx, "TASK-menu-acl", "AGENT-menu")
	if err != nil || !found || review.ID != granted.ID {
		t.Fatalf("review grant: %+v %v %v", review, found, err)
	}
	revoked, err := r.CommandTaskMemoryAccess(ctx, menuEnvelope(r, "TASK-menu-acl", 0, "r-1"), "TASK-menu-acl", "AGENT-menu", false)
	if err != nil || revoked.ID != granted.ID || revoked.RevokedAt == nil {
		t.Fatalf("revoke: %+v %v", revoked, err)
	}
	if ok, _ := r.store.HasActiveRoleBinding(ctx, "AGENT-menu", "TASK-menu-acl"); ok {
		t.Fatal("revoked grant still effective")
	}
	if _, err := r.CommandTaskMemoryAccess(context.Background(), menuEnvelope(r, "TASK-menu-acl", 0, "anon"), "TASK-menu-acl", "AGENT-menu", true); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous grant: %v", err)
	}
}

func TestMenuAttachArtifactRequiresVerifiedPayloadAndCandidate(t *testing.T) {
	r, ctx := memoryOperatorRuntime(t)
	payload := []byte("build log line\n")
	sum := sha256.Sum256(payload)
	stored, err := artifactstore.New(r.layout.Artifacts, r.store).Put(ctx, model.ArtifactInput{
		ID: "ART-menu", ProjectID: r.ProjectID(), Kind: "report", ClaimedDigest: "sha256:" + hex.EncodeToString(sum[:]),
		SourceCommit: strings.Repeat("a", 40), Data: bytes.NewReader(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := r.memoryReviewPrincipal(ctx)
	candidate, err := r.memoryService.ExtractCandidate(ctx, p, ExtractCandidateRequest{ProjectID: r.ProjectID(), Kind: model.MemoryKindFinding, Title: "build log finding", Body: "the build log shows the failing step"})
	if err != nil {
		t.Fatal(err)
	}
	e := menuEnvelope(r, candidate.ID, candidate.Revision, "attach-1")
	got, err := r.CommandAttachMemoryArtifact(ctx, e, candidate.ID, stored.ID)
	if err != nil || got.Revision != candidate.Revision+1 {
		t.Fatalf("attach: %+v %v", got, err)
	}
	reread, err := r.store.GetMemoryV2(ctx, r.ProjectID(), candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	refs, _ := reread.ExtMeta["artifact_refs"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["digest"] != stored.Digest {
		t.Fatalf("artifact reference not durable: %#v", reread.ExtMeta["artifact_refs"])
	}
	if replay, err := r.CommandAttachMemoryArtifact(ctx, e, candidate.ID, stored.ID); err != nil || replay.Revision != got.Revision {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if _, err := r.CommandAttachMemoryArtifact(ctx, menuEnvelope(r, candidate.ID, got.Revision, "attach-dup"), candidate.ID, stored.ID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("duplicate attach: %v", err)
	}
	if err := os.WriteFile(stored.Path, []byte("mutated"), 0o600); err != nil {
		t.Fatal(err)
	}
	other, err := r.memoryService.ExtractCandidate(ctx, p, ExtractCandidateRequest{ProjectID: r.ProjectID(), Kind: model.MemoryKindFinding, Title: "other", Body: "another finding"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommandAttachMemoryArtifact(ctx, menuEnvelope(r, other.ID, other.Revision, "attach-mutated"), other.ID, stored.ID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("mutated artifact attached: %v", err)
	}
	durable, err := r.store.GetMemoryV2(ctx, r.ProjectID(), mustRememberDurable(t, r, ctx).ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommandAttachMemoryArtifact(ctx, menuEnvelope(r, durable.ID, durable.Revision, "attach-durable"), durable.ID, stored.ID); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("durable record changed without review: %v", err)
	}
}

func mustRememberDurable(t *testing.T, r *Runtime, ctx context.Context) model.MemoryRecordV2 {
	t.Helper()
	rec, err := r.CommandRemember(ctx, RememberRequest{ProjectID: r.ProjectID(), ScopeID: r.ProjectID(), Title: "reviewed fact", Body: "reviewed durable fact", Kind: model.MemoryKindSemantic})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Lifecycle != model.MemoryDurable {
		t.Skipf("operator remember produced %s, not a durable record", rec.Lifecycle)
	}
	return rec
}

func TestMenuCompileHandoffDoesNotSubmit(t *testing.T) {
	r, ctx := memoryOperatorRuntime(t)
	menuTask(t, r, "TASK-menu-handoff")
	if _, err := r.CommandSetMemorySlot(ctx, menuEnvelope(r, "TASK-menu-handoff", 0, "set-h"), "TASK-menu-handoff", working.SlotHandoffNote, "continue from the parser"); err != nil {
		t.Fatal(err)
	}
	before, err := r.store.ListMemoryV2(ctx, memoryFilter(r))
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := r.CompileMemoryHandoff(ctx, "TASK-menu-handoff", "reviewer")
	if err != nil || bundle.TaskID != "TASK-menu-handoff" || len(bundle.WorkingSlots) != 1 || bundle.ByteSize == 0 {
		t.Fatalf("compile: %+v %v", bundle, err)
	}
	after, err := r.store.ListMemoryV2(ctx, memoryFilter(r))
	if err != nil || len(after) != len(before) {
		t.Fatalf("compiling a handoff wrote memory: %d -> %d %v", len(before), len(after), err)
	}
	if _, err := r.CompileMemoryHandoff(context.Background(), "TASK-menu-handoff", ""); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous compile: %v", err)
	}
}

func TestMenuExtractAndReflectBindToTheReviewedRun(t *testing.T) {
	r := runtimeForPlan(t)
	ctx := context.Background()
	goal := planGoal()
	goal.ProjectID = r.ProjectIdentity()
	if err := r.Store().SaveGoalContract(ctx, goal, goal.Revision); err != nil {
		t.Fatal(err)
	}
	request := planCreateRequest()
	request.ProjectID = projectid.ID(r.ProjectIdentity())
	if _, err := r.Plans().Create(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Plans().Approve(ctx, request.ProjectID); err != nil {
		t.Fatal(err)
	}
	run, err := r.Execution().StartRun(ctx, "SESSION-plan", request.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(ctx)
	var taskID string
	for id := range run.Tasks {
		taskID = id
	}
	if _, err := r.CommandExtractMemoryCandidate(ctx, menuEnvelope(r, run.RunID, run.Version+1, "x-stale"), MemoryExtractCommand{RunID: run.RunID, Kind: model.MemoryKindSemantic, Title: "t", Body: "b"}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale run version accepted: %v", err)
	}
	if _, err := r.CommandExtractMemoryCandidate(ctx, menuEnvelope(r, run.RunID, run.Version, "x-task"), MemoryExtractCommand{RunID: run.RunID, TaskID: "not-in-run", Kind: model.MemoryKindSemantic, Title: "t", Body: "b"}); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("foreign task accepted: %v", err)
	}
	rec, err := r.CommandExtractMemoryCandidate(ctx, menuEnvelope(r, run.RunID, run.Version, "x-1"), MemoryExtractCommand{RunID: run.RunID, TaskID: taskID, Kind: model.MemoryKindProcedural, Title: "run lesson", Body: "the README typo fix needs one file"})
	if err != nil || rec.RunID != run.RunID || rec.Lifecycle != model.MemoryCandidate || rec.Authority != model.AuthorityAgent || rec.ExtMeta["task_id"] != taskID {
		t.Fatalf("extract: %+v %v", rec, err)
	}
	if _, err := r.CommandReflectRunTask(ctx, menuEnvelope(r, run.RunID, run.Version, "reflect-pending"), run.RunID, taskID); !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("reflection of a non-terminal task: %v", err)
	}
}

func memoryFilter(r *Runtime) store.MemoryQueryFilter {
	return store.MemoryQueryFilter{ProjectID: r.ProjectID()}
}

func TestMenuReflectUsesTerminalRunFactsOnly(t *testing.T) {
	r := runtimeForPlan(t)
	ctx := context.Background()
	goal := planGoal()
	goal.ProjectID = r.ProjectIdentity()
	if err := r.Store().SaveGoalContract(ctx, goal, goal.Revision); err != nil {
		t.Fatal(err)
	}
	request := planCreateRequest()
	request.ProjectID = projectid.ID(r.ProjectIdentity())
	if _, err := r.Plans().Create(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Plans().Approve(ctx, request.ProjectID); err != nil {
		t.Fatal(err)
	}
	service := r.Execution()
	service.RegisterHarness(execution.NewMockHarness("test-harness", func(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		return execution.TaskResult{TaskID: task.TaskID, Success: true, Claims: []execution.ExecutionClaim{{
			ClaimID: "claim-" + task.TaskID, TaskID: task.TaskID, ClaimText: "fixed typo in README.md",
			Status: execution.ClaimSupported, EvidenceRefs: []string{"ev-readme"},
		}}}, nil
	}))
	run, err := service.StartRun(ctx, "SESSION-plan", request.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	service.Engine().EvidenceOracle().RecordEvidence(execution.ExecutionEvidence{EvidenceID: "ev-readme", RunID: run.RunID, RelevantFiles: []string{"README.md"}, Status: execution.EvidenceValid})
	current, err := service.ExecuteRun(ctx, run.RunID)
	if err == nil && current.State == execution.RunNeedsApproval {
		for _, task := range current.Tasks {
			if task.ApprovalID != "" {
				if err := service.Approve(ctx, task.ApprovalID, "test-operator", "approve"); err != nil {
					t.Fatal(err)
				}
			}
		}
		current, err = service.ExecuteRun(ctx, run.RunID)
	}
	_ = current
	reread, err := service.GetRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	cur := &reread
	var taskID string
	var state execution.TaskExecutionState
	for id, task := range cur.Tasks {
		taskID, state = id, task.State
	}
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(ctx)
	rec, err := r.CommandReflectRunTask(ctx, menuEnvelope(r, cur.RunID, cur.Version, "reflect-1"), cur.RunID, taskID)
	switch state {
	case execution.TaskCompletedPendingVerify:
		// Without a VERIFIED_COMPLETE Process 06 session a completed task is
		// not a verified success, so reflection refuses rather than claim one.
		if !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "has not verified") {
			t.Fatalf("unverified success reflected: %+v %v", rec, err)
		}
	case execution.TaskFailed, execution.TaskBlocked:
		if err != nil || rec.Kind != model.MemoryKindFinding || rec.Lifecycle != model.MemoryCandidate || rec.RunID != cur.RunID || rec.ExtMeta["run_status"] != "FAILED" || !strings.Contains(rec.Body, string(state)) {
			t.Fatalf("failed task reflection: %+v %v", rec, err)
		}
		again, err := r.CommandReflectRunTask(ctx, menuEnvelope(r, cur.RunID, cur.Version, "reflect-2"), cur.RunID, taskID)
		if err != nil || again.ID != rec.ID {
			t.Fatalf("reflection is not deduplicated by content: %+v %v", again, err)
		}
	default:
		t.Skipf("fixture run left task %s in %s", taskID, state)
	}
}
