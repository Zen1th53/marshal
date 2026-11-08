package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/protocol"
)

func menuRestFixture(t *testing.T) (*Runtime, context.Context) {
	t.Helper()
	r := runtimeForPlan(t)
	local, err := r.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r, local.Context(context.Background())
}

func menuRestTask(t *testing.T, r *Runtime, id string) model.Task {
	t.Helper()
	ctx := context.Background()
	if _, err := r.ImportTasks(ctx, []model.Task{{ID: id, Title: "operator handoff " + id, Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	task, err := r.Store().GetTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func menuRestEvidence(t *testing.T, r *Runtime, id, taskID string) {
	t.Helper()
	metadata := map[string]string{"task_id": taskID}
	digest, err := evidence.CanonicalDigest(evidence.NodeTypeClaim, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.PutNode(context.Background(), evidence.Node{ID: evidence.NodeID(id), Type: evidence.NodeTypeClaim, Digest: digest, Metadata: metadata, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func policySuiteFixture(t *testing.T) []byte {
	t.Helper()
	p := policy.Policy{ID: "menu-policy", Version: 1, Default: policy.Effect("deny")}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"id": "menu-suite", "cases": []any{map[string]any{
		"id": "case-1", "name": "default decision",
		"given": map[string]any{
			"policy":  map[string]any{"id": string(p.ID), "version": p.Version, "default": string(p.Default), "rules": []any{}},
			"binding": map[string]any{"version": p.Version, "digest": string(digest), "generation": 1},
		},
		"when":   map[string]any{"subject_id": "subject-1", "task_id": "task-1", "change_id": "change-1", "action": "read", "resource": "repo"},
		"expect": map[string]any{"decision": "deny"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPolicyTestSuiteRunsOnlyTheReviewedBytes(t *testing.T) {
	r, owner := menuRestFixture(t)
	source := policySuiteFixture(t)
	path := filepath.Join(r.ProjectRoot(), "suite.json")
	if err := os.WriteFile(path, source, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReviewPolicyTestSuite(context.Background(), "suite.json"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous review: %v", err)
	}
	if _, err := r.ReviewPolicyTestSuite(owner, "../suite.json"); err == nil {
		t.Fatal("a path outside the project was reviewed")
	}
	reviewed, err := r.ReviewPolicyTestSuite(owner, "suite.json")
	if err != nil || reviewed.Digest == "" {
		t.Fatalf("review: %+v %v", reviewed, err)
	}
	report, err := r.RunPolicyTestSuite(owner, reviewed)
	if err != nil || report.Status == "" || len(report.Cases) == 0 {
		t.Fatalf("run: %+v %v", report, err)
	}
	if err := os.WriteFile(path, append(source, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunPolicyTestSuite(owner, reviewed); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("changed suite: %v", err)
	}
}

func TestOperatorTaskHandoffIsTypedIdempotentAndRevisionBound(t *testing.T) {
	r, owner := menuRestFixture(t)
	task := menuRestTask(t, r, "TASK-OP-HANDOFF")
	menuRestEvidence(t, r, "EVIDENCE-OP-HANDOFF", task.ID)
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "SESSION-plan", TargetID: task.ID, ExpectedVersion: task.Revision, IdempotencyKey: "op-handoff-1"}
	req := TaskHandoffRequest{TaskID: task.ID, TargetRole: "qa", Summary: "ready for review password=hunter2", EvidenceIDs: []string{"EVIDENCE-OP-HANDOFF"}}

	if _, err := r.CommandTaskHandoff(context.Background(), e, req); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous handoff: %v", err)
	}
	first, err := r.CommandTaskHandoff(owner, e, req)
	if err != nil || first.Status != protocol.StatusAccepted || first.ToRole != protocol.RoleQA || !strings.HasPrefix(first.FromAgent, "local-uid:") {
		t.Fatalf("handoff: %+v %v", first, err)
	}
	if strings.Contains(first.Claims["summary"], "hunter2") {
		t.Fatalf("secret reached the handoff: %+v", first.Claims)
	}
	again, err := r.CommandTaskHandoff(owner, e, req)
	if err != nil || again.ID != first.ID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	req.Summary = "different"
	if _, err := r.CommandTaskHandoff(owner, e, req); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("key reuse: %v", err)
	}
	stale := e
	stale.IdempotencyKey, stale.ExpectedVersion = "op-handoff-stale", task.Revision+1
	if _, err := r.CommandTaskHandoff(owner, stale, req); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale task revision: %v", err)
	}
	badRole := e
	badRole.IdempotencyKey = "op-handoff-role"
	if _, err := r.CommandTaskHandoff(owner, badRole, TaskHandoffRequest{TaskID: task.ID, TargetRole: "root", Summary: "x"}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("unknown role: %v", err)
	}
	foreign := e
	foreign.IdempotencyKey = "op-handoff-evidence"
	if _, err := r.CommandTaskHandoff(owner, foreign, TaskHandoffRequest{TaskID: task.ID, TargetRole: "qa", Summary: "x", EvidenceIDs: []string{"EVIDENCE-NOWHERE"}}); err == nil {
		t.Fatal("handoff accepted evidence that does not exist")
	}
}

func TestOperatorMemoryHandoffCompilesThroughCanonicalPath(t *testing.T) {
	r, owner := menuRestFixture(t)
	task := menuRestTask(t, r, "TASK-OP-MEMORY")
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "SESSION-plan", TargetID: task.ID, ExpectedVersion: task.Revision, IdempotencyKey: "op-memory-1"}
	req := MemoryHandoffRequest{TaskID: task.ID, TargetRole: "developer"}
	if _, err := r.CommandMemoryHandoff(context.Background(), e, req); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous memory handoff: %v", err)
	}
	first, err := r.CommandMemoryHandoff(owner, e, req)
	if err != nil || first.Status != protocol.StatusAccepted || first.Claims["goal"] != task.Title {
		t.Fatalf("memory handoff: %+v %v", first, err)
	}
	again, err := r.CommandMemoryHandoff(owner, e, req)
	if err != nil || again.ID != first.ID {
		t.Fatalf("replay compiled a second handoff: %+v %v", again, err)
	}
	stored, err := r.Store().GetHandoff(context.Background(), first.ID)
	if err != nil || stored.ContextDigest != first.ContextDigest {
		t.Fatalf("durable reread: %+v %v", stored, err)
	}
}

func TestOperatorClaimChallengeUsesStoredCounterEvidence(t *testing.T) {
	r, owner := menuRestFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	claim := model.Claim{ID: "CLAIM-op", GoalID: "GOAL-plan", GoalRevision: 1, Subject: "tests", NormalizedText: "all tests pass", Scope: "repo",
		Criticality: model.CriticalityStandard, State: model.ClaimStateSupported, Author: model.AuthorProvenance{AgentID: "AGENT-dev"}, CreatedAt: now, UpdatedAt: now}
	if err := r.Store().SaveClaim(ctx, claim); err != nil {
		t.Fatal(err)
	}
	menuRestEvidence(t, r, "EVIDENCE-COUNTER", "TASK-any")
	reviewed, err := r.ReviewClaim(owner, claim.ID)
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "SESSION-plan", TargetID: claim.ID, IdempotencyKey: "challenge-1"}
	req := ClaimChallengeRequest{EvidenceID: "EVIDENCE-COUNTER", Reason: "the failing run contradicts it", ExpectedDigest: ClaimDigest(reviewed)}
	if _, err := r.CommandChallengeClaim(ctx, e, req); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous challenge: %v", err)
	}
	missing := req
	missing.EvidenceID = "EVIDENCE-INVENTED"
	e2 := e
	e2.IdempotencyKey = "challenge-missing"
	if _, err := r.CommandChallengeClaim(owner, e2, missing); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("invented evidence: %v", err)
	}
	contested, err := r.CommandChallengeClaim(owner, e, req)
	if err != nil || contested.State != model.ClaimStateContested || len(contested.ContradictingEvidence) != 1 || contested.ContradictingEvidence[0].Digest == "" {
		t.Fatalf("challenge: %+v %v", contested, err)
	}
	replay, err := r.CommandChallengeClaim(owner, e, req)
	if err != nil || len(replay.ContradictingEvidence) != 1 {
		t.Fatalf("replay appended twice: %+v %v", replay, err)
	}
	e3 := e
	e3.IdempotencyKey = "challenge-stale"
	if _, err := r.CommandChallengeClaim(owner, e3, req); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale digest: %v", err)
	}
}

func TestOperatorCorrectionResolvesDecisionAndNeedsConfirmation(t *testing.T) {
	r, owner := menuRestFixture(t)
	ctx := context.Background()
	goal := planGoal()
	goal.ProjectID = r.ProjectIdentity()
	goal.ConstitutionVersion = constitution.Current.String()
	goal.UnresolvedDecisions = []model.UnresolvedDecision{{ID: "DEC-scope", Question: "Which file?", Options: []string{"README.md", "docs/index.md"}, RequiresUser: true}}
	if err := r.Store().SaveGoalContract(ctx, goal, 1); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	critical := model.Claim{ID: "CLAIM-critical", GoalID: goal.ID, GoalRevision: 2, Subject: "scope", NormalizedText: "docs only", Scope: "repo",
		Criticality: model.CriticalityBlocker, State: model.ClaimStateSupported, Author: model.AuthorProvenance{AgentID: "AGENT-dev"}, CreatedAt: now, UpdatedAt: now}
	if err := r.Store().SaveClaim(ctx, critical); err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: goal.SessionID, TargetID: goal.ID, ExpectedVersion: 2, IdempotencyKey: "correct-1"}
	if _, err := r.CommandCorrectInterpretation(owner, e, "DEC-scope", "somewhere else"); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("option outside the decision: %v", err)
	}
	e.IdempotencyKey = "correct-2"
	corrected, err := r.CommandCorrectInterpretation(owner, e, "DEC-scope", "README.md")
	if err != nil || corrected.Revision != 3 || corrected.Confirmation != model.ConfirmationPending || len(corrected.UnresolvedDecisions) != 0 {
		t.Fatalf("correction: %+v %v", corrected, err)
	}
	found := false
	for _, c := range corrected.Constraints {
		found = found || (c.IsHard && strings.Contains(c.Text, "README.md"))
	}
	if !found {
		t.Fatalf("operator decision was not recorded: %+v", corrected.Constraints)
	}
	if claim, err := r.Store().GetClaim(ctx, critical.ID); err != nil || claim.State != model.ClaimStateInvalidated {
		t.Fatalf("critical claim not invalidated: %+v %v", claim, err)
	}
	if replay, err := r.CommandCorrectInterpretation(owner, e, "DEC-scope", "README.md"); err != nil || replay.Revision != 3 {
		t.Fatalf("replay: %+v %v", replay, err)
	}
}
