package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

type cleanupWrapper struct{ cleaned bool }

func (w *cleanupWrapper) Wrap(model.SandboxRequest, []string) (model.CommandSpec, error) {
	return model.CommandSpec{Path: "/wrapped", Cleanup: func() error { w.cleaned = true; return nil }}, nil
}

type failingProcess struct{ err error }

func (p failingProcess) Run(context.Context, adapter.Command) (adapter.ProcessResult, error) {
	return adapter.ProcessResult{}, p.err
}
func TestSandboxedRunnerCleansStorageAfterSuccessAndFailure(t *testing.T) {
	for _, failure := range []error{nil, errors.New("launch failed"), context.Canceled} {
		wrapper := &cleanupWrapper{}
		_, err := NewSandboxed(failingProcess{err: failure}, wrapper, model.SandboxRequest{}).Run(context.Background(), adapter.Command{Path: "/command"})
		if err != failure || !wrapper.cleaned {
			t.Fatalf("cleanup=%v error=%v", wrapper.cleaned, err)
		}
	}
}
