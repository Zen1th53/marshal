package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/risk"
)

func TestRiskAssessmentTwoStoresConvergeOneCanonicalOutcome(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "risk-contention.db")
	first, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err := second.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	request := risk.AssessmentRequest{
		ID: "assessment-a08-contention",
		Descriptor: risk.ToolDescriptor{
			Tool: "git", Action: "push", Resource: "repo:marshal",
			Factors: risk.Factors{ExternalWrite: true},
		},
	}
	start := make(chan struct{})
	results := make(chan risk.Assessment, 64)
	errorsCh := make(chan error, 64)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			engine := risk.NewEngine(first)
			if i%2 == 1 {
				engine = risk.NewEngine(second)
			}
			result, err := engine.Assess(ctx, request)
			if err != nil {
				errorsCh <- err
				return
			}
			results <- result
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		t.Errorf("contention error: %v", err)
	}
	if len(results) != 64 {
		t.Fatalf("successful results=%d, want 64 idempotent reconciliations", len(results))
	}
	stored, err := first.GetRiskAssessment(ctx, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != risk.StateRequirementsEmitted || stored.Level != risk.LevelHigh {
		t.Fatalf("stored assessment=%+v", stored)
	}
}

func TestRiskAssessmentCancellationBeforeMutationLeavesNoRow(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := risk.NewEngine(st).Assess(cancelled, risk.AssessmentRequest{
		ID:         "assessment-a08-cancelled",
		Descriptor: risk.ToolDescriptor{Tool: "git", Action: "push", Resource: "repo:marshal", Factors: risk.Factors{ExternalWrite: true}},
	})
	if err == nil {
		t.Fatal("cancelled assessment succeeded")
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM risk_assessments WHERE assessment_id = ?", "assessment-a08-cancelled"); got != 0 {
		t.Fatalf("cancelled assessment rows=%d, want 0", got)
	}
}

func TestRiskAssessmentLateStateTransitionRejected(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := risk.NewEngine(st).Assess(ctx, risk.AssessmentRequest{
		ID:         "assessment-a08-stale",
		Descriptor: risk.ToolDescriptor{Tool: "git", Action: "push", Resource: "repo:marshal", Factors: risk.Factors{ExternalWrite: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.TransitionRiskAssessmentState(ctx, "assessment-a08-stale", risk.StateRequested, risk.StateClassified); err == nil {
		t.Fatal("stale requested-to-classified transition accepted")
	}
}
