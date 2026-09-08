package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
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

func (s *VerificationService) Current(ctx context.Context, id string) (verification.Session, error) {
	if s == nil || s.runtime == nil {
		return verification.Session{}, model.ErrUnavailable
	}
	return s.runtime.store.GetVerificationSession(ctx, id)
}

func (s *VerificationService) Evaluate(ctx context.Context, id string, current verification.Binding) (verification.Session, error) {
	session, err := s.Current(ctx, id)
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

func (s *VerificationService) Attest(ctx context.Context, id string, current verification.Binding, bundleDigest, provenance string) (verification.CompletionAttestation, error) {
	session, err := s.Current(ctx, id)
	if err != nil {
		return verification.CompletionAttestation{}, err
	}
	decision := verification.Evaluate(session, current, s.now())
	if decision != session.State {
		return verification.CompletionAttestation{}, verification.ErrBindingMismatch
	}
	a, err := verification.NewCompletionAttestation("completion-"+id+fmt.Sprintf("-%d", session.Version), session, bundleDigest, provenance, s.now())
	if err != nil {
		return verification.CompletionAttestation{}, err
	}
	if err := s.runtime.store.AppendCompletionAttestation(ctx, a); err != nil {
		return verification.CompletionAttestation{}, err
	}
	return a, nil
}
