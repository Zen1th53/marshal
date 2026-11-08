//go:build !governed

package lifecycle

import (
	"context"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Actual custom callbacks and an owned file write prove that post-execution
// verifier refusal cannot substitute for mandatory context before work.
func TestExtremeLifecycleRequiresContextBeforeCustomHarnessWork(t *testing.T) {
	for _, direct := range []bool{false, true} {
		for _, name := range []string{"test-harness", "codex", "mock"} {
			mode := "Service"
			if direct {
				mode = "Engine"
			}
			t.Run(mode+"/"+name, func(t *testing.T) {
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
				calls := atomic.Int64{}
				harness := execution.NewMockHarness(name, func(context.Context, execution.TaskExecution, execution.ConstraintPackage, string) (execution.TaskResult, error) {
					calls.Add(1)
					if err := os.WriteFile(filepath.Join(repo.Path(), "UNQUALIFIED-HARNESS-WORK"), []byte("unqualified work"), 0600); err != nil {
						return execution.TaskResult{}, err
					}
					return execution.TaskResult{TaskID: "task", Success: true, Claims: []execution.ExecutionClaim{{ClaimID: "claim", TaskID: "task", ClaimText: "done", Status: execution.ClaimSupported, EvidenceRefs: []string{"evidence"}}}}, nil
				})
				if direct {
					runtime.Execution().Engine().RegisterHarness(harness)
				} else {
					runtime.Execution().RegisterHarness(harness)
				}
				a, err := New(runtime, approved{true}, passVerifier{})
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC()
				cap := goalintake.UnknownCapacity("openai", true)
				req := Request{CorrelationID: "job-1", SessionID: "session-1", GoalID: "goal-1", ProjectID: "PROJECT-0123456789abcdef0123456789abcdef", Intent: "Make a bounded safe change", Constraints: []model.Constraint{{ID: "immutable", Text: "do not access secrets", IsHard: true}}, SuccessCriteria: []string{"done"}, Tasks: []plan.Task{{ID: "task", Title: "bounded change", Criteria: []string{"done"}}}, RequestedHarness: name, Candidates: []goalintake.Candidate{{Provider: "openai", Model: "gpt-4o", Capacity: cap, Governance: constitution.GovernanceVerified}}, HarnessCandidates: []plan.HarnessCandidate{{Profile: model.HarnessProfile{Harness: name, InstalledVersion: "1", BinaryPath: "/test/test-harness", SupportedModels: []string{"gpt-4o"}, DefaultModel: "gpt-4o", FeatureSupport: map[string]model.FeatureStatus{"approval_mode": model.StatusNative, "sandbox_mode": model.StatusNative}, ProbeEvidenceID: "probe", ProbedAt: now}, InstalledVersion: "1", Provider: "openai", Capacity: cap}}}
				out, err := a.Submit(ctx, req)
				if calls.Load() != 0 {
					t.Fatalf("harness callback executed %d times before missing required-context refusal: status=%s fingerprint=%s error=%v", calls.Load(), out.Status, out.FailureFingerprint, err)
				}
				if _, e := os.Stat(filepath.Join(repo.Path(), "UNQUALIFIED-HARNESS-WORK")); !os.IsNotExist(e) {
					t.Fatal("unqualified custom harness changed project", e)
				}
				if err == nil || out.Status != StatusFailed || out.VerificationID != "" || out.MemoryCommitID != "" {
					t.Fatalf("missing required context qualified: %+v %v", out, err)
				}

			})
		}
	}
}
