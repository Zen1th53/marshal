//go:build governed

package tui

import "context"

func (*Workspace) marshalOperationOwnerContext(ctx context.Context) (context.Context, error) {
	return ctx, nil
}
