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

func TestGovernedAntigravityDefaultRefusesUnverifiedNewerAndUnqualified(t *testing.T) {
	// In default / community edition, any unqualified version (including unverified newer)
	// MUST be refused for governed mode.
	for _, tc := range []struct {
		version string
		desc    string
	}{
		{"1.5.0", "unverified newer"},
		{"2.0.0", "major newer"},
		{"1.3.2", "divergent contract"},
		{"1.2.13", "older"},
		{"1.2.14 arbitrary", "malformed"},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			runner := &mockRunner{runFn: func(_ context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
				if len(cmd.Args) == 1 && cmd.Args[0] == "--version" {
					return adapter.ProcessResult{Stdout: []byte(tc.version)}, nil
				}
				if len(cmd.Args) == 1 && cmd.Args[0] == "--help" {
					return adapter.ProcessResult{Stdout: []byte("--print --output-format --conversation --mode --sandbox --new-project --disable-slash-commands --model")}, nil
				}
				return adapter.ProcessResult{}, nil
			}}
			client := antigravity.NewGovernedProbe("agy", runner)
			if _, err := client.Probe(t.Context()); !errors.Is(err, model.ErrUnavailable) {
				t.Fatalf("default edition admitted unqualified governed version %s: %v", tc.version, err)
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
