package secrets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
)

type LeaseStore interface {
	PutSecretLease(context.Context, Lease) error
	GetSecretLease(context.Context, string) (Lease, error)
	TransitionSecretLease(context.Context, string, LeaseState, LeaseState, time.Time) (Lease, error)
	ClaimSecretLease(context.Context, string, string, time.Time) (Lease, error)
	CompleteSecretLease(context.Context, string, string, time.Time) (Lease, error)
	ReleaseSecretLeaseClaim(context.Context, string, string) error
}

type EngineConfig struct {
	Store      LeaseStore
	Providers  map[string]Provider
	Capability capability.Broker
	EventStore events.Store
	Metrics    *evidence.MetricsRecorder
	Now        func() time.Time
}

type Engine struct {
	store      LeaseStore
	providers  map[string]Provider
	capability capability.Broker
	eventStore events.Store
	metrics    *evidence.MetricsRecorder
	now        func() time.Time
	owner      string
}

func NewEngine(config EngineConfig) (*Engine, error) {
	if config.Store == nil || len(config.Providers) == 0 || config.Capability == nil {
		return nil, ErrDenied
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	owner, err := model.NewID("secret-owner-")
	if err != nil {
		return nil, ErrProviderFailed
	}
	return &Engine{store: config.Store, providers: config.Providers, capability: config.Capability, eventStore: config.EventStore, metrics: config.Metrics, now: config.Now, owner: owner}, nil
}

func (e *Engine) Lease(ctx context.Context, request LeaseRequest) (Lease, error) {
	if request.ID == "" || request.IssuedAt.IsZero() || request.ExpiresAt.IsZero() {
		return Lease{}, ErrDenied
	}
	lease := Lease{ID: request.ID, Subject: request.Subject, TaskID: request.TaskID, Ref: request.Ref, Purpose: request.Purpose, IssuedAt: request.IssuedAt.UTC(), ExpiresAt: request.ExpiresAt.UTC(), State: StateRequested}
	if err := lease.Validate(); err != nil {
		return Lease{}, err
	}
	existing, lookupErr := e.store.GetSecretLease(ctx, lease.ID)
	if lookupErr == nil {
		if sameLeaseScope(existing, lease) && existing.State != StateRequested {
			return existing, nil
		}
		if !sameLeaseScope(existing, lease) {
			return Lease{}, ErrDenied
		}
		lease = existing
	} else if !isNotFound(lookupErr) {
		return Lease{}, ErrDenied
	}
	if lookupErr != nil {
		if err := e.store.PutSecretLease(ctx, lease); err != nil {
			return Lease{}, ErrDenied
		}
		if err := e.emit(ctx, events.EventTypeSecretLeaseRequested, lease, "requested"); err != nil {
			return Lease{}, err
		}
	}
	leased, err := e.store.TransitionSecretLease(ctx, lease.ID, StateRequested, StateLeased, e.now().UTC())
	if err != nil {
		return Lease{}, ErrDenied
	}
	if err := e.emit(ctx, events.EventTypeSecretLeaseIssued, leased, "issued"); err != nil {
		return Lease{}, err
	}
	return leased, nil
}

func (e *Engine) WithSecret(ctx context.Context, lease Lease, use func([]byte) error) error {
	if use == nil {
		return ErrDenied
	}
	started := time.Now()
	current, err := e.store.GetSecretLease(ctx, lease.ID)
	if err != nil || !sameLeaseScope(current, lease) {
		return ErrDenied
	}
	now := e.now().UTC()
	if !now.Before(current.ExpiresAt) {
		_, _ = e.store.TransitionSecretLease(ctx, current.ID, StateLeased, StateExpired, now)
		_ = e.emit(ctx, events.EventTypeSecretAccessDenied, current, string(CodeLeaseExpired))
		e.observe(evidence.MetricResultDenied, string(CodeLeaseExpired), started)
		return ErrLeaseExpired
	}
	if current.State != StateLeased && current.State != StateUsed {
		_ = e.emit(ctx, events.EventTypeSecretAccessDenied, current, string(CodeDenied))
		e.observe(evidence.MetricResultDenied, string(CodeDenied), started)
		return ErrDenied
	}
	resource, err := capability.NormalizeResource(capability.KindSecretUse, "secret://"+strings.Join([]string{current.Ref.Provider, current.Ref.Name, current.Ref.Version}, "/"))
	if err != nil {
		_ = e.emit(ctx, events.EventTypeSecretAccessDenied, current, string(CodeDenied))
		return ErrDenied
	}
	decision, err := e.capability.Authorize(ctx, capability.Query{
		Subject: capability.SubjectID(current.Subject), TaskID: capability.TaskID(current.TaskID),
		Kind: capability.KindSecretUse, Resource: resource, Action: "read", At: now,
	})
	if err != nil || decision.Outcome != capability.OutcomeAllow {
		if ctxErr := ctx.Err(); ctxErr != nil {
			e.observe(evidence.MetricResultCancelled, "CANCELLED", started)
			return ctxErr
		}
		e.observe(evidence.MetricResultDenied, string(CodeDenied), started)
		return ErrDenied
	}
	if current.State == StateUsed {
		if err := e.emit(ctx, events.EventTypeSecretAccessUsed, current, "used"); err != nil {
			return err
		}
		e.observe(evidence.MetricResultSuccess, "SECRET_USED", started)
		return nil
	}
	claimed, err := e.store.ClaimSecretLease(ctx, current.ID, e.owner, now)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			e.observe(evidence.MetricResultCancelled, "CANCELLED", started)
			return ctxErr
		}
		e.observe(evidence.MetricResultConflict, "SECRET_CLAIM_CONFLICT", started)
		return ErrDenied
	}
	if e.metrics != nil {
		e.metrics.AddActive(evidence.MetricOperationSecret, 1)
	}
	defer func() {
		if e.metrics != nil {
			e.metrics.AddActive(evidence.MetricOperationSecret, -1)
		}
	}()
	completed := false
	defer func() {
		if !completed {
			_ = e.store.ReleaseSecretLeaseClaim(context.Background(), claimed.ID, e.owner)
		}
	}()
	provider, ok := e.providers[claimed.Ref.Provider]
	if !ok {
		_ = e.emit(ctx, events.EventTypeSecretAccessDenied, current, string(CodeProviderFailed))
		e.observe(evidence.MetricResultError, string(CodeProviderFailed), started)
		return ErrProviderFailed
	}
	value, err := provider.Resolve(ctx, claimed.Ref)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			e.observe(evidence.MetricResultCancelled, "CANCELLED", started)
			return err
		}
		_ = e.emit(ctx, events.EventTypeSecretAccessDenied, current, string(CodeProviderFailed))
		e.observe(evidence.MetricResultError, string(CodeProviderFailed), started)
		return ErrProviderFailed
	}
	defer zero(value)
	if err := use(value); err != nil {
		_ = e.emit(ctx, events.EventTypeSecretAccessDenied, current, string(CodeDenied))
		e.observe(evidence.MetricResultDenied, string(CodeDenied), started)
		return ErrDenied
	}
	if _, err := e.store.CompleteSecretLease(ctx, claimed.ID, e.owner, now); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			e.observe(evidence.MetricResultCancelled, "CANCELLED", started)
			return ctxErr
		}
		e.observe(evidence.MetricResultConflict, "SECRET_COMPLETE_CONFLICT", started)
		return ErrDenied
	}
	completed = true
	if err := e.emit(ctx, events.EventTypeSecretAccessUsed, current, "used"); err != nil {
		return err
	}
	e.observe(evidence.MetricResultSuccess, "SECRET_USED", started)
	return nil
}

func (e *Engine) observe(result evidence.MetricResult, reason string, started time.Time) {
	if e.metrics != nil {
		e.metrics.Observe(evidence.MetricOperationSecret, result, reason, time.Since(started))
	}
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
	if err := e.emit(ctx, events.EventTypeSecretLeaseRevoked, current, "revoked"); err != nil {
		return err
	}
	return nil
}

func (e *Engine) emit(ctx context.Context, typ events.EventType, lease Lease, reason string) error {
	if e.eventStore == nil {
		return nil
	}
	key := string(typ) + "/" + lease.ID
	sum := sha256.Sum256([]byte(key))
	event := events.Event{
		ID: "secret-" + hex.EncodeToString(sum[:]), Type: typ,
		Subject: lease.Subject, TaskID: lease.TaskID,
		ResourceID: secretResourceID(lease.Ref), At: e.now().UTC(),
		IdempotencyKey: key,
		Data:           map[string]any{"provider": lease.Ref.Provider, "version": lease.Ref.Version, "purpose": lease.Purpose, "reason": reason},
	}
	if _, err := e.eventStore.Append(ctx, event); err != nil {
		return ErrDenied
	}
	return nil
}

func secretResourceID(ref Ref) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{ref.Provider, ref.Name, ref.Version}, "\x00")))
	return "secret-ref-" + hex.EncodeToString(sum[:])
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
	return errors.Is(err, ErrNotFound) || errors.Is(err, model.ErrNotFound)
}
