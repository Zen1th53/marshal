package workerterminal

import (
	"context"
	"github.com/Zen1th53/marshal/internal/processgroup"
	"os/exec"
)

type lifecycleKey struct{}
type Lifecycle func(processgroup.Reference) error

func WithLifecycle(ctx context.Context, observer Lifecycle) context.Context {
	return context.WithValue(ctx, lifecycleKey{}, observer)
}
func Started(ctx context.Context, cmd *exec.Cmd) error {
	observer, _ := ctx.Value(lifecycleKey{}).(Lifecycle)
	if observer == nil {
		return nil
	}
	ref, err := processgroup.Identify(cmd)
	if err != nil {
		return err
	}
	return observer(ref)
}
