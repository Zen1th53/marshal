//go:build !governed && unix

package antigravity_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
)

const probeFixtureHelp = "--print --output-format --conversation --mode --sandbox --new-project --disable-slash-commands --model"

func TestGovernedAntigravityProbeIndependentBoundedStartupPhases(t *testing.T) {
	calls := 0
	runner := &mockRunner{runFn: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Fatal("unbounded phase")
		}
		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
			return adapter.ProcessResult{ExitCode: -1, TimedOut: true}, ctx.Err()
		}
		if cmd.Args[0] == "--version" {
			return adapter.ProcessResult{Stdout: []byte("1.2.14")}, nil
		}
		return adapter.ProcessResult{Stdout: []byte(probeFixtureHelp)}, nil
	}}
	started := time.Now()
	if _, err := antigravity.NewGoverned("fixture-only", runner).Probe(t.Context()); err != nil {
		t.Fatalf("two valid finite startup phases share one deadline: %v", err)
	}
	if calls != 2 || time.Since(started) > 9*time.Second {
		t.Fatal("unexpected phase calls or unbounded total")
	}
}

func TestGovernedAntigravityProbePreservesParentDeadlineAndPrivateFailureMetadata(t *testing.T) {
	for _, phase := range []string{"version", "help"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			runner := &mockRunner{runFn: func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
				if phase == "help" && cmd.Args[0] == "--version" {
					return adapter.ProcessResult{Stdout: []byte("1.2.14")}, nil
				}
				<-ctx.Done()
				return adapter.ProcessResult{ExitCode: -1, TimedOut: true}, ctx.Err()
			}}
			started := time.Now()
			_, err := antigravity.NewGoverned("fixture-only", runner).Probe(ctx)
			if err == nil || time.Since(started) > time.Second {
				t.Fatal("parent deadline bypassed")
			}
			for _, s := range []string{"runner=deadline", "exit=-1", "timed_out=true", "cancelled=false", "output_truncated=false"} {
				if !strings.Contains(err.Error(), s) {
					t.Fatalf("bounded metadata missing %s: %v", s, err)
				}
			}
		})
	}
	runner := &mockRunner{runFn: func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
		return adapter.ProcessResult{ExitCode: 999, Cancelled: true, OutputTruncated: true}, errors.New("private credential marker never emit")
	}}
	_, err := antigravity.NewGoverned("fixture-only", runner).Probe(t.Context())
	if err == nil || strings.Contains(err.Error(), "credential marker") || !strings.Contains(err.Error(), "runner=error") || !strings.Contains(err.Error(), "exit=unknown") {
		t.Fatal("private failure was raw or numeric bound lost")
	}
}

func TestGovernedAntigravityProbeCancelledParentRemainsBounded(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	runner := &mockRunner{runFn: func(ctx context.Context, _ adapter.Command) (adapter.ProcessResult, error) {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("cancelled parent replaced")
		}
		return adapter.ProcessResult{ExitCode: -1, Cancelled: true}, ctx.Err()
	}}
	_, err := antigravity.NewGoverned("fixture-only", runner).Probe(ctx)
	if err == nil || !strings.Contains(err.Error(), "runner=cancelled") || !strings.Contains(err.Error(), "cancelled=true") {
		t.Fatal("cancel metadata missing")
	}
}
