package secrets

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
)

type LeaseStore interface {
	PutSecretLease(context.Context, Lease) error
	GetSecretLease(context.Context, string) (Lease, error)
	TransitionSecretLease(context.Context, string, LeaseState, LeaseState, time.Time) (Lease, error)
}

type EngineConfig struct {
	Store      LeaseStore
	Providers  map[string]Provider
	Capability capability.Broker
	Now        func() time.Time
}

type Engine struct {
	store      LeaseStore
	providers  map[string]Provider
	capability capability.Broker
	now        func() time.Time
}

func NewEngine(config EngineConfig) (*Engine, error) {
	if config.Store == nil || len(config.Providers) == 0 || config.Capability == nil {
		return nil, ErrDenied
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Engine{store: config.Store, providers: config.Providers, capability: config.Capability, now: config.Now}, nil
}

func (e *Engine) Lease(ctx context.Context, request LeaseRequest) (Lease, error) {
	if request.ID == "" || request.IssuedAt.IsZero() || request.ExpiresAt.IsZero() {
		return Lease{}, ErrDenied
	}
	lease := Lease{ID: request.ID, Subject: request.Subject, TaskID: request.TaskID, Ref: request.Ref, Purpose: request.Purpose, IssuedAt: request.IssuedAt.UTC(), ExpiresAt: request.ExpiresAt.UTC(), State: StateRequested}
	if err := lease.Validate(); err != nil {
		return Lease{}, err
	}
	if existing, err := e.store.GetSecretLease(ctx, lease.ID); err == nil {
		if sameLeaseScope(existing, lease) {
			return existing, nil
		}
		return Lease{}, ErrDenied
	} else if !isNotFound(err) {
		return Lease{}, ErrDenied
	}
	if err := e.store.PutSecretLease(ctx, lease); err != nil {
		return Lease{}, ErrDenied
	}
	leased, err := e.store.TransitionSecretLease(ctx, lease.ID, StateRequested, StateLeased, e.now().UTC())
	if err != nil {
		return Lease{}, ErrDenied
	}
	return leased, nil
}

func (e *Engine) WithSecret(ctx context.Context, lease Lease, use func([]byte) error) error {
	if use == nil {
		return ErrDenied
	}
	current, err := e.store.GetSecretLease(ctx, lease.ID)
	if err != nil || !sameLeaseScope(current, lease) {
		return ErrDenied
	}
	now := e.now().UTC()
	if !now.Before(current.ExpiresAt) {
		_, _ = e.store.TransitionSecretLease(ctx, current.ID, StateLeased, StateExpired, now)
		return ErrLeaseExpired
	}
	if current.State != StateLeased {
		return ErrDenied
	}
	resource, err := capability.NormalizeResource(capability.KindSecretUse, "secret://"+strings.Join([]string{current.Ref.Provider, current.Ref.Name, current.Ref.Version}, "/"))
	if err != nil {
		return ErrDenied
	}
	decision, err := e.capability.Authorize(ctx, capability.Query{
		Subject: capability.SubjectID(current.Subject), TaskID: capability.TaskID(current.TaskID),
		Kind: capability.KindSecretUse, Resource: resource, Action: "read", At: now,
	})
	if err != nil || decision.Outcome != capability.OutcomeAllow {
		return ErrDenied
	}
	provider, ok := e.providers[current.Ref.Provider]
	if !ok {
		return ErrProviderFailed
	}
	value, err := provider.Resolve(ctx, current.Ref)
	if err != nil {
		return ErrProviderFailed
	}
	defer zero(value)
	if err := use(value); err != nil {
		return ErrDenied
	}
	if _, err := e.store.TransitionSecretLease(ctx, current.ID, StateLeased, StateUsed, now); err != nil {
		return ErrDenied
	}
	return nil
}

func (e *Engine) Revoke(ctx context.Context, request RevokeRequest) error {
	current, err := e.store.GetSecretLease(ctx, request.LeaseID)
	if err != nil || current.Subject != request.Subject {
		return ErrDenied
	}
	if current.State != StateRequested && current.State != StateLeased {
		return ErrDenied
	}
	if _, err := e.store.TransitionSecretLease(ctx, current.ID, current.State, StateRevoked, e.now().UTC()); err != nil {
		return ErrDenied
	}
	return nil
}

func sameLeaseScope(a, b Lease) bool {
	return a.ID == b.ID && a.Subject == b.Subject && a.TaskID == b.TaskID && a.Ref == b.Ref && a.Purpose == b.Purpose && a.IssuedAt.Equal(b.IssuedAt) && a.ExpiresAt.Equal(b.ExpiresAt)
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func isNotFound(err error) bool {
	return err == ErrNotFound || fmt.Sprint(err) == ErrNotFound.Error()
}
