package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestMemoryWritesCannotForgePrivateOrProjectScope(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestMemoryService(t)
	principal := testPrincipal("agent-scope-owner")

	if _, err := svc.Remember(ctx, principal, RememberRequest{
		ProjectID: "PROJECT-local", Kind: model.MemoryKindSemantic,
		Title: "forged private", Body: "must not persist",
		Scope: model.ScopeOperatorPrivate, ScopeID: "another-principal",
	}); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("forged private scope accepted: %v", err)
	}
	if _, err := svc.ExtractCandidate(ctx, principal, ExtractCandidateRequest{
		ProjectID: "PROJECT-local", Kind: model.MemoryKindSemantic,
		Title: "forged project", Body: "must not persist",
		Scope: model.ScopeProject, ScopeID: "PROJECT-OTHER",
	}); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("foreign project scope accepted: %v", err)
	}
}

func TestM12_FailureOutcomePreservesEvidenceAndRetryBoundary(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestMemoryService(t)
	rec, err := svc.CaptureOutcome(ctx, OutcomeCaptureRequest{
		ProjectID: "PROJECT-local", TaskID: "TASK-FAILURE-LESSON", TaskTitle: "kernel sandbox probe",
		RunID: "RUN-FAILURE-LESSON", AgentID: "agent-local", Provider: "local", Status: "failed", ExitStatus: 1,
		BaseCommit: "base-a", HeadCommit: "head-a", EvidenceIDs: []string{"EVID-FAILURE-COMMAND", "EVID-FAILURE-STDERR"},
		FilesChanged: []string{"internal/sandbox/probe.go"}, TestsRun: []string{"TestSandboxProbe"},
		ErrorSignature: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		FailureReason:  "kernel user namespaces disabled", RetryCondition: "retry only when user namespaces are enabled",
		Environment: map[string]string{"kernel": "test-kernel", "isolation": "process-only"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Kind != model.MemoryKindFailure || rec.Lifecycle != model.MemoryCandidate || rec.Authority != model.AuthorityAgent {
		t.Fatalf("unexpected failure candidate: %+v", rec)
	}
	if rec.ExtMeta["retry_condition"] != "retry only when user namespaces are enabled" || len(rec.EvidenceIDs) != 2 {
		t.Fatalf("failure lesson lost retry/evidence semantics: %+v", rec)
	}
}

func TestM12_GovernedCandidateExtractionAndDeduplication(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	pAgent := testPrincipal("agent-worker-1")

	// 1. Agent extracts candidate
	c1, err := svc.ExtractCandidate(ctx, pAgent, ExtractCandidateRequest{
		ProjectID:   projectID,
		TaskID:      "TASK-100",
		Kind:        model.MemoryKindFinding,
		Title:       "Flaky integration test in sqlite_test.go",
		Body:        "TestSQLiteConcurrentWal occasionally times out on slow disk IO",
		Scope:       model.ScopeProject,
		EvidenceIDs: []string{"EVID-100"},
	})
	if err != nil {
		t.Fatalf("extract candidate: %v", err)
	}

	// Invariant: Candidate must be AuthorityAgent and MemoryCandidate lifecycle
	if c1.Authority != model.AuthorityAgent || c1.Lifecycle != model.MemoryCandidate {
		t.Fatalf("candidate has unauthorized authority/lifecycle: authority=%s lifecycle=%s", c1.Authority, c1.Lifecycle)
	}

	// 2. Agent tries to submit exact duplicate candidate
	c2, err := svc.ExtractCandidate(ctx, pAgent, ExtractCandidateRequest{
		ProjectID:   projectID,
		TaskID:      "TASK-100",
		Kind:        model.MemoryKindFinding,
		Title:       "Flaky integration test in sqlite_test.go",
		Body:        "TestSQLiteConcurrentWal occasionally times out on slow disk IO",
		Scope:       model.ScopeProject,
		EvidenceIDs: []string{"EVID-100"},
	})
	if err != nil {
		t.Fatalf("extract duplicate candidate: %v", err)
	}

	// Invariant: Must return the existing record ID rather than duplicating
	if c2.ID != c1.ID {
		t.Fatalf("expected deduplication to return identical ID %s, got %s", c1.ID, c2.ID)
	}
}

func TestM12_SemanticContradictionConflictDetection(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	pAgent := testPrincipal("agent-worker-1")

	// 1. Initial finding
	f1, err := svc.ExtractCandidate(ctx, pAgent, ExtractCandidateRequest{
		ProjectID: projectID,
		TaskID:    "TASK-200",
		Kind:      model.MemoryKindFinding,
		Title:     "Compiler Optimization Flag Invariant",
		Body:      "Flag -O3 is required for release performance",
		Scope:     model.ScopeProject,
	})
	if err != nil {
		t.Fatalf("extract f1: %v", err)
	}

	// 2. Contradictory finding with same subject but conflicting assertion
	f2, err := svc.ExtractCandidate(ctx, pAgent, ExtractCandidateRequest{
		ProjectID: projectID,
		TaskID:    "TASK-201",
		Kind:      model.MemoryKindFinding,
		Title:     "Compiler Optimization Flag Invariant",
		Body:      "Flag -O3 causes segmentation faults in garbage collector, must use -O2",
		Scope:     model.ScopeProject,
	})
	if err != nil {
		t.Fatalf("extract f2: %v", err)
	}

	// Invariant: Both records must now be in MemoryConflicted lifecycle and reference each other
	if f2.Lifecycle != model.MemoryConflicted {
		t.Fatalf("expected f2 to be MemoryConflicted, got %s", f2.Lifecycle)
	}
	hasLink := false
	for _, cid := range f2.ConflictIDs {
		if cid == f1.ID {
			hasLink = true
			break
		}
	}
	if !hasLink {
		t.Fatalf("expected f2 to link f1 in ConflictIDs, got %+v", f2.ConflictIDs)
	}
}

func TestM12_GovernedOperatorPromotion(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	pAgent := testPrincipal("agent-worker-1")
	pOperator := authz.Principal{
		ID: "operator-admin",
		Role: authz.Role{
			Name: "admin",
			Authorities: []authz.Authority{
				authz.AuthorityPolicyAdmin,
			},
		},
	}
	evidenceMetadata := map[string]string{"task_id": "TASK-300", "finding": "connection-pool-limit"}
	digest, err := evidence.CanonicalDigest(evidence.NodeTypeClaim, evidenceMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Store().PutNode(ctx, evidence.Node{
		ID: "EVID-TASK-300", Type: evidence.NodeTypeClaim, Digest: digest,
		Metadata: evidenceMetadata, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	unverified, err := svc.ExtractCandidate(ctx, pAgent, ExtractCandidateRequest{
		ProjectID: projectID, TaskID: "TASK-300", Kind: model.MemoryKindSemantic,
		Title: "Unverified pool claim", Body: "this claim has no evidence", Scope: model.ScopeProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Promote(ctx, pOperator, PromoteRequest{ProjectID: projectID, MemoryID: unverified.ID, ScopeID: projectID}); err == nil {
		t.Fatal("operator promotion accepted an unevidenced agent claim")
	}
	conflicted := unverified
	conflicted.ID = "MEM-CONFLICTED-PROMOTION"
	conflicted.Lifecycle = model.MemoryConflicted
	conflicted.EvidenceIDs = []string{"EVID-TASK-300"}
	conflicted.ConflictIDs = []string{unverified.ID}
	conflicted.ContentDigest = ""
	if err := rt.Store().WriteMemoryV2(ctx, conflicted); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Promote(ctx, pOperator, PromoteRequest{ProjectID: projectID, MemoryID: conflicted.ID, ScopeID: projectID}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("conflicted memory promotion error = %v, want conflict", err)
	}

	// 1. Agent creates candidate
	cand, err := svc.ExtractCandidate(ctx, pAgent, ExtractCandidateRequest{
		ProjectID:   projectID,
		TaskID:      "TASK-300",
		Kind:        model.MemoryKindSemantic,
		Title:       "PostgreSQL Connection Pool Maximum",
		Body:        "Max open connections must not exceed 50 per node",
		Scope:       model.ScopeProject,
		EvidenceIDs: []string{"EVID-TASK-300"},
	})
	if err != nil {
		t.Fatalf("create candidate: %v", err)
	}

	// 2. Agent attempts self-promotion (must fail authorization)
	_, err = svc.Promote(ctx, pAgent, PromoteRequest{
		ProjectID: projectID,
		MemoryID:  cand.ID,
		ScopeID:   projectID,
	})
	if err == nil {
		t.Fatalf("expected unauthorized agent self-promotion to fail")
	}

	// 3. Operator promotes candidate
	promoted, err := svc.Promote(ctx, pOperator, PromoteRequest{
		ProjectID: projectID,
		MemoryID:  cand.ID,
		ScopeID:   projectID,
	})
	if err != nil {
		t.Fatalf("operator promote: %v", err)
	}
	if promoted.Lifecycle != model.MemoryDurable || promoted.Authority != model.AuthorityOperator {
		t.Fatalf("expected durable operator record after promotion: %+v", promoted)
	}
}
