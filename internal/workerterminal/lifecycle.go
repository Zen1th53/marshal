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

// Completion records the driver's outcome independently of the terminal relay.
type completionKey struct{}
type Completion func(int) error

func WithCompletion(ctx context.Context, observer Completion) context.Context {
	return context.WithValue(ctx, completionKey{}, observer)
}
func Completed(ctx context.Context, exitCode int) error {
	if observer, _ := ctx.Value(completionKey{}).(Completion); observer != nil {
		return observer(exitCode)
	}
	return nil
}
