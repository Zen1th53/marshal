package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	goruntime "runtime"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	marshalstore "github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

// VerificationService is the sole application boundary for Process 06 state.
// Surfaces may render its returned canonical records, but cannot manufacture a
// success state by writing an audit event or local UI state.
type VerificationService struct {
	runtime *Runtime
	now     func() time.Time
}

func (r *Runtime) Verification() *VerificationService {
	if r == nil {
		return nil
	}
	return &VerificationService{runtime: r, now: func() time.Time { return time.Now().UTC() }}
}

func (s *VerificationService) Start(ctx context.Context, session verification.Session) (verification.Session, error) {
	if s == nil || s.runtime == nil {
		return verification.Session{}, fmt.Errorf("%w: verification service unavailable", model.ErrUnavailable)
	}
	if session.State == verification.VerifiedComplete {
		return verification.Session{}, fmt.Errorf("%w: a new session cannot self-assert completion", verification.ErrInvalid)
	}
	bound, err := s.authoritativeBinding(ctx, session.Binding.RunID)
	if err != nil {
		return verification.Session{}, err
	}
	if session.Binding != bound {
		return verification.Session{}, verification.ErrBindingMismatch
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = s.now()
	}
	session.UpdatedAt = session.CreatedAt
	session.State = verification.Blocked
	if err := s.runtime.store.CreateVerificationSession(ctx, session); err != nil {
		return verification.Session{}, err
	}
	return s.runtime.store.GetVerificationSession(ctx, session.ID)
}

// StartForRun creates a Process 06 session bound to the canonical Process 05
// run. Callers provide only verifier findings; the goal/plan/run/tree binding
// is always derived here from durable runtime state.
func (s *VerificationService) StartForRun(ctx context.Context, runID string, session verification.Session) (verification.Session, error) {
	binding, err := s.BindingForRun(ctx, runID)
	if err != nil {
		return verification.Session{}, err
	}
	session.Binding = binding
	return s.Start(ctx, session)
}

// BindingForRun returns the exact runtime-derived Process06 binding for a
// Process05 run. It exposes no mutable runtime state.
func (s *VerificationService) BindingForRun(ctx context.Context, runID string) (verification.Binding, error) {
	if s == nil || s.runtime == nil {
		return verification.Binding{}, fmt.Errorf("%w: verification service unavailable", model.ErrUnavailable)
	}
	return s.authoritativeBinding(ctx, runID)
}

func (s *VerificationService) Current(ctx context.Context, id string) (verification.Session, error) {
	if s == nil || s.runtime == nil {
		return verification.Session{}, model.ErrUnavailable
	}
	return s.runtime.store.GetVerificationSession(ctx, id)
}

func (s *VerificationService) Evaluate(ctx context.Context, id string) (verification.Session, error) {
	session, err := s.Current(ctx, id)
	if err != nil {
		return verification.Session{}, err
	}
	current, err := s.authoritativeBinding(ctx, session.Binding.RunID)
	if err != nil {
		return verification.Session{}, err
	}
	decision := verification.Evaluate(session, current, s.now())
	session.Version++
	session.State = decision
	session.UpdatedAt = s.now()
	if err := s.runtime.store.UpdateVerificationSession(ctx, session, session.Version-1); err != nil {
		return verification.Session{}, err
	}
	return s.Current(ctx, id)
}

func (s *VerificationService) Attest(ctx context.Context, id string, envelope verification.BundleEnvelope, provenance string) (verification.CompletionAttestation, error) {
	session, err := s.Current(ctx, id)
	if err != nil {
		return verification.CompletionAttestation{}, err
	}
	current, err := s.authoritativeBinding(ctx, session.Binding.RunID)
	if err != nil {
		return verification.CompletionAttestation{}, err
	}
	decision := verification.Evaluate(session, current, s.now())
	if decision != session.State {
		return verification.CompletionAttestation{}, verification.ErrBindingMismatch
	}
	if envelope.Bundle.VerificationID != session.ID {
		return verification.CompletionAttestation{}, verification.ErrBindingMismatch
	}
	if err := envelope.Bundle.Verify(envelope.Payloads, current); err != nil {
		return verification.CompletionAttestation{}, err
	}
	a, err := verification.NewCompletionAttestation("completion-"+id+fmt.Sprintf("-%d", session.Version), session, envelope.Bundle.ManifestDigest, provenance, s.now())
	if err != nil {
		return verification.CompletionAttestation{}, err
	}
	if err := s.runtime.store.AppendCompletionAttestation(ctx, a); err != nil {
		return verification.CompletionAttestation{}, err
	}
	return a, nil
}

func (s *VerificationService) authoritativeBinding(ctx context.Context, runID string) (verification.Binding, error) {
	run, err := s.runtime.Execution().GetRun(ctx, runID)
	if err != nil {
		return verification.Binding{}, fmt.Errorf("read canonical execution run: %w", err)
	}
	goalRevisions, err := s.runtime.store.ListGoalRevisions(ctx, run.GoalID)
	if err != nil || len(goalRevisions) == 0 {
		return verification.Binding{}, fmt.Errorf("read canonical goal: %w", err)
	}
	goal := goalRevisions[len(goalRevisions)-1]
	plan, err := s.runtime.store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		return verification.Binding{}, fmt.Errorf("read canonical plan: %w", err)
	}
	if goal.Revision != run.GoalRevision || plan.Goal.GoalID != run.GoalID || plan.Goal.Revision != run.GoalRevision || string(run.ProjectID) != goal.ProjectID || plan.ProjectID != run.ProjectID {
		return verification.Binding{}, verification.ErrBindingMismatch
	}
	tree, err := execution.WorkspaceTreeDigest(s.runtime.layout.Root)
	if err != nil {
		return verification.Binding{}, err
	}
	environment, err := json.Marshal(struct {
		GOOS, GOARCH string
		Schema       int
		Policy       RuntimePolicyConfig
	}{goruntime.GOOS, goruntime.GOARCH, marshalstore.LatestSchemaVersion, s.runtime.runtimePolicy})
	if err != nil {
		return verification.Binding{}, err
	}
	h := sha256.Sum256(environment)
	return verification.Binding{ProjectID: string(run.ProjectID), GoalID: run.GoalID, GoalRevision: run.GoalRevision, PlanID: run.PlanID, PlanVersion: run.PlanVersion, RunID: run.RunID, RunVersion: run.Version, TreeDigest: tree, EnvironmentDigest: hex.EncodeToString(h[:])}, nil
}
