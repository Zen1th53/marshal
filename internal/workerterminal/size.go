package workerterminal

import "context"

type sizeKey struct{}

// Size returns the current hosted terminal rows and columns, never provider defaults.
type Size func(context.Context) (uint16, uint16, error)

func WithSize(ctx context.Context, size Size) context.Context {
	return context.WithValue(ctx, sizeKey{}, size)
}
