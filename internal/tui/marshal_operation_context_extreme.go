//go:build !governed

package tui

import "context"

func (w *Workspace) marshalOperationOwnerContext(ctx context.Context) (context.Context, error) {
	return w.nativePlanningOwnerContext(ctx)
}
