//go:build !governed

package workerterminal

import "context"

type heldCellStartKey struct{}

// WithHeldLifecycle requires the private supervisor to wait until the callback
// has persisted its exact identity and rechecked authority. It grants no
// authority itself; the authenticated cell service owns that callback.
func WithHeldLifecycle(ctx context.Context, observer Lifecycle) context.Context {
	return context.WithValue(WithLifecycle(ctx, observer), heldCellStartKey{}, true)
}

func HeldCellStart(ctx context.Context) bool {
	held, _ := ctx.Value(heldCellStartKey{}).(bool)
	return held
}
