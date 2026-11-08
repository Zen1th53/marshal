package antigravity_test

import (
	"context"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"testing"
	"time"
)

func TestOrdinaryAntigravityProbeKeepsSharedDeadlineAndErrors(t *testing.T) {
	var original time.Time
	calls := 0
	runner := &mockRunner{runFn: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("ordinary probe lost deadline")
		}
		if cmd.Args[0] == "--version" {
			original = deadline
			return adapter.ProcessResult{Stdout: []byte("ordinary-version")}, nil
		}
		if deadline != original {
			t.Fatal("ordinary probe deadline changed")
		}
		return adapter.ProcessResult{ExitCode: 7}, nil
	}}
	_, err := antigravity.New("fixture-only", runner).Probe(t.Context())
	if calls != 2 || err == nil || err.Error() != "unavailable: probe Antigravity help failed" {
		t.Fatal("ordinary probe behavior changed")
	}
}
