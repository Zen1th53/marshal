package cli

import (
	"context"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/model"
)

type nativeOneShotRuntime interface {
	HasReadGrant(string) bool
	ProposeContinuation(context.Context, importer.SessionTranscript) ([]model.MemoryRecordV2, bool, error)
	Close() error
}

type appOneShotRuntime struct{ runtime *app.Runtime }

func (r appOneShotRuntime) HasReadGrant(path string) bool { return r.runtime.HasReadGrant(path) }
func (r appOneShotRuntime) ProposeContinuation(ctx context.Context, tr importer.SessionTranscript) ([]model.MemoryRecordV2, bool, error) {
	return r.runtime.ProposeContinuation(ctx, tr)
}
func (r appOneShotRuntime) Close() error { return r.runtime.Close() }

var openNativeOneShotRuntime = func(ctx context.Context, root string) (nativeOneShotRuntime, error) {
	runtime, err := app.Open(ctx, root)
	if err != nil {
		return nil, err
	}
	return appOneShotRuntime{runtime: runtime}, nil
}
