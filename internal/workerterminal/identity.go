package workerterminal

import "context"

// Identity joins a displayed plan task to the runtime's canonical execution.
type Identity struct{ ExecutionRunID, CanonicalTaskID string }
type identityKey struct{}
type IdentityObserver func(Identity) error

func WithIdentity(ctx context.Context, observer IdentityObserver) context.Context {
	return context.WithValue(ctx, identityKey{}, observer)
}
func ReportIdentity(ctx context.Context, id Identity) error {
	observer, _ := ctx.Value(identityKey{}).(IdentityObserver)
	if observer == nil {
		return nil
	}
	return observer(id)
}
