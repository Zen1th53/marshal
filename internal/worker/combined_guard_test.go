package worker

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"testing"
)

type combinedGuardProcess struct {
	t              *testing.T
	supervised     bool
	callerObserved *bool
}

func (p combinedGuardProcess) Run(ctx context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	p.t.Helper()
	if command.Supervised != p.supervised {
		p.t.Fatalf("supervision: %+v", command)
	}
	if p.supervised {
		if command.SupervisorSocket == "" || command.Refusal == nil {
			p.t.Fatal("missing refusal boundary")
		}
		if err := command.Refusal(ctx, sandbox.Refusal{}); err != nil {
			p.t.Fatal(err)
		}
	}
	command.OutputObserver("stdout", []byte("token beyond capture limit"))
	if ctx.Err() != context.Canceled || !*p.callerObserved {
		p.t.Fatal("output hit did not cancel or reach caller")
	}
	return adapter.ProcessResult{Cancelled: true, OutputTruncated: true}, nil
}

func TestHoneypotAndSocketObserverBothRetained(t *testing.T) {
	for _, supervised := range []bool{false, true} {
		t.Run(map[bool]string{false: "injected", true: "supervised"}[supervised], func(t *testing.T) {
			observed, checked, refused, callerObserved := false, false, false, false
			var refusal func(context.Context, sandbox.Refusal) error
			if supervised {
				refusal = func(context.Context, sandbox.Refusal) error { refused = true; return nil }
			}
			runner := NewGuardedSandboxed(combinedGuardProcess{t, supervised, &callerObserved}, &captureWrapper{}, model.SandboxRequest{Worktree: "/task", ScratchHome: "/private-home"},
				func(stream string, data []byte) bool {
					observed = stream == "stdout" && string(data) == "token beyond capture limit"
					return observed
				},
				func(ctx context.Context, result *adapter.ProcessResult) error {
					if ctx.Err() != nil || !observed || !result.Cancelled || !result.OutputTruncated {
						t.Fatal("post-run check lost raw evidence or received cancelled context")
					}
					checked = true
					result.Stdout = []byte("redacted")
					return ErrHoneypot
				}, refusal)
			result, err := runner.Run(t.Context(), adapter.Command{Path: "/bin/true", OutputObserver: func(string, []byte) { callerObserved = true }})
			if !errors.Is(err, ErrHoneypot) || result.ExitCode != -1 || string(result.Stdout) != "redacted" || !checked || refused != supervised {
				t.Fatalf("guard lost: %+v %v checked=%v refused=%v", result, err, checked, refused)
			}
		})
	}
}
