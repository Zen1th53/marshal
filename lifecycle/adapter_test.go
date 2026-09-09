package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"github.com/Zen1th53/marshal/internal/verification"
)

type approved struct{ ok bool }

func (a approved) Approved(context.Context, Request) (bool, error)                 { return a.ok, nil }
func (a approved) ApproveExecution(context.Context, Request, string) (bool, error) { return a.ok, nil }

type passVerifier struct{}

func (passVerifier) Session(_ context.Context, _ Request, b verification.Binding) (verification.Session, error) {
	return verification.Session{ID: "verify-lifecycle", Version: 1, Criteria: []verification.Criterion{{ID: "done", Mandatory: true, ClaimIDs: []string{"claim"}}}, Claims: []verification.Claim{{ID: "claim", CriterionID: "done", SemanticScope: []string{"README.md"}, EvidenceIDs: []string{"evidence"}}}, Evidence: []verification.Evidence{{ID: "evidence", ClaimID: "claim", Status: verification.StatusPass, ContentDigest: "d", TreeDigest: b.TreeDigest, EnvironmentDigest: b.EnvironmentDigest, ClusterID: "independent", Attempts: 1, Passes: 1}}, RequiredChecks: map[string]verification.Status{"check": verification.StatusPass}}, nil
}
func (passVerifier) Envelope(_ context.Context, _ Request, s verification.Session) (verification.BundleEnvelope, string, error) {
	p := []byte("independent local verification")
	d := sha256.Sum256(p)
	b, err := verification.BuildEvidenceBundle("bundle-lifecycle", s.ID, s.Binding, []verification.BundleEntry{{Path: "evidence.txt", Digest: hex.EncodeToString(d[:]), Size: int64(len(p))}}, time.Now().UTC())
	return verification.BundleEnvelope{Bundle: b, Payloads: map[string][]byte{"evidence.txt": p}}, "local-lifecycle-test", err
}

func TestAdapterRunsCanonicalProcess03Through07(t *testing.T) {
	ctx := context.Background()
	repo := testgit.New(t)
	for _, n := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		b, err := os.ReadFile(filepath.Join("..", n))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), n), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.Execution().RegisterHarness(execution.NewMockHarness("openai", func(context.Context, execution.TaskExecution, execution.ConstraintPackage, string) (execution.TaskResult, error) {
		return execution.TaskResult{TaskID: "task", Success: true, Claims: []execution.ExecutionClaim{{ClaimID: "claim", TaskID: "task", ClaimText: "done", Status: execution.ClaimSupported, EvidenceRefs: []string{"evidence"}}}}, nil
	}))
	a, err := New(runtime, approved{true}, passVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cap := goalintake.UnknownCapacity("openai", true)
	req := Request{CorrelationID: "job-1", SessionID: "session-1", GoalID: "goal-1", ProjectID: "PROJECT-0123456789abcdef0123456789abcdef", Intent: "Make a bounded safe change", Constraints: []model.Constraint{{ID: "immutable", Text: "do not access secrets", IsHard: true}}, SuccessCriteria: []string{"done"}, Tasks: []plan.Task{{ID: "task", Title: "bounded change", Criteria: []string{"done"}}}, RequestedHarness: "test-harness", Candidates: []goalintake.Candidate{{Provider: "openai", Model: "gpt-4o", Capacity: cap, Governance: constitution.GovernanceVerified}}, HarnessCandidates: []plan.HarnessCandidate{{Profile: model.HarnessProfile{Harness: "test-harness", InstalledVersion: "1", SupportedModels: []string{"gpt-4o"}, DefaultModel: "gpt-4o", ProbeEvidenceID: "probe", ProbedAt: now}, Provider: "openai", Capacity: cap}}}
	out, err := a.Submit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusCompleted || out.VerificationID == "" || out.MemoryCommitID == "" {
		t.Fatalf("result %+v", out)
	}
	run, err := runtime.Execution().GetRun(ctx, out.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(run.HardConstraints, "\n"), "do not access secrets") {
		t.Fatalf("constraint reinjection lost: %v", run.HardConstraints)
	}
	again, err := a.Submit(ctx, req)
	if err != nil || again != out {
		t.Fatalf("idempotency %#v %v", again, err)
	}
	blocked := req
	blocked.CorrelationID = "job-denied"
	blocked.SessionID = "session-denied"
	blocked.GoalID = "goal-denied"
	denied, err := New(runtime, approved{false}, passVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := denied.Submit(ctx, blocked)
	if err != nil || pending.Status != StatusApprovalRequired {
		t.Fatalf("local approval gate result=%+v err=%v", pending, err)
	}
}

func TestAdapterRejectsMalformedAndNonImmutableRequests(t *testing.T) {
	a := &Adapter{completed: map[string]Result{}}
	if _, err := a.Submit(context.Background(), Request{}); err == nil {
		t.Fatal("accepted malformed request")
	}
	if err := validate(Request{CorrelationID: "c", SessionID: "s", GoalID: "g", ProjectID: "p", Intent: "i", RequestedHarness: "h", Tasks: []plan.Task{{ID: "t"}}, Constraints: []model.Constraint{{ID: "weak", Text: "weak"}}}); err == nil {
		t.Fatal("accepted mutable constraint")
	}
}
