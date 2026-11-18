//go:build governed || !unix

package antigravity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"github.com/Zen1th53/marshal/internal/model"
)

// The community edition never runs a governed agy (every governed invocation
// is refused as unavailable), whatever its version. The version number is not
// the gate: a plain version probes, only an unreadable one is refused.
func TestGovernedAntigravityDefaultRefusesGovernedUseAtAnyVersion(t *testing.T) {
	for _, tc := range []struct {
		version string
		probes  bool
	}{
		{"1.5.0", true}, {"2.0.0", true}, {"1.3.2", true}, {"1.2.13", true},
		{"1.2.14 arbitrary", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			runner := &mockRunner{runFn: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
				if len(cmd.Args) == 1 && cmd.Args[0] == "--version" {
					return adapter.ProcessResult{Stdout: []byte(tc.version)}, nil
				}
				if len(cmd.Args) == 1 && cmd.Args[0] == "--help" {
					return adapter.ProcessResult{Stdout: []byte("--print --output-format --conversation --mode --sandbox --new-project --disable-slash-commands --model")}, nil
				}
				return adapter.ProcessResult{}, nil
			}}
			_, err := antigravity.NewGovernedProbe("agy", runner).Probe(t.Context())
			if tc.probes && err != nil {
				t.Fatalf("version %s refused for its number: %v", tc.version, err)
			}
			if !tc.probes && !errors.Is(err, model.ErrUnavailable) {
				t.Fatalf("unreadable version %q admitted: %v", tc.version, err)
			}
			req := adapter.Request{TaskID: "T", Title: "t", Worktree: t.TempDir(), Model: "gemini-3.8-flash-high"}
			if _, err := antigravity.NewGoverned("agy", runner).Run(t.Context(), req); !errors.Is(err, model.ErrUnavailable) {
				t.Fatalf("community edition ran a governed agy at %s: %v", tc.version, err)
			}
		})
	}
}

func TestGovernedAntigravityDefaultAcceptsSupportedRangeInProbe(t *testing.T) {
	// Qualified versions pass probe validation in default edition
	for _, version := range []string{"1.2.14", "1.4.0", "1.4.2"} {
		t.Run(version, func(t *testing.T) {
			runner := &mockRunner{runFn: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
				if len(cmd.Args) == 1 && cmd.Args[0] == "--version" {
					return adapter.ProcessResult{Stdout: []byte(version)}, nil
				}
				if len(cmd.Args) == 1 && cmd.Args[0] == "--help" {
					return adapter.ProcessResult{Stdout: []byte("--print --output-format --conversation --mode --sandbox --new-project --disable-slash-commands --model")}, nil
				}
				return adapter.ProcessResult{}, nil
			}}
			client := antigravity.NewGovernedProbe("agy", runner)
			probe, err := client.Probe(t.Context())
			if err != nil {
				t.Fatalf("default edition probe failed for supported version %s: %v", version, err)
			}
			if probe.Version != version {
				t.Fatalf("probe version reported dishonestly: got %q, want %q", probe.Version, version)
			}
		})
	}
}

func TestGovernedAntigravityDefaultRefusesGovernedExecution(t *testing.T) {
	// In default edition, governed execution is unavailable
	runner := &mockRunner{runFn: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		if len(cmd.Args) == 1 && cmd.Args[0] == "--version" {
			return adapter.ProcessResult{Stdout: []byte("1.4.2")}, nil
		}
		if len(cmd.Args) == 1 && cmd.Args[0] == "--help" {
			return adapter.ProcessResult{Stdout: []byte("--print --output-format --conversation --mode --sandbox --new-project --disable-slash-commands --model")}, nil
		}
		return adapter.ProcessResult{Stdout: []byte(`{"status":"SUCCESS","response":"ok"}`)}, nil
	}}
	client := antigravity.NewGoverned("agy", runner)
	req := nativeRequest()
	req.Model = "gemini-3.8-flash-high"
	if _, err := client.Run(t.Context(), req); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("default edition allowed governed execution: %v", err)
	}
}
