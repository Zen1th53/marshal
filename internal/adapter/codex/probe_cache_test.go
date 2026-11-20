package codex

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
)

func TestProbeReusesHelpAndCatalogForSameInstalledBinary(t *testing.T) {
	binary := fakeProbeBinary(t, true)
	var calls []string
	runner := probeRunnerFunc(func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
		calls = append(calls, strings.Join(cmd.Args, " "))
		return (&capturingRunner{probeCompatible: true}).Run(ctx, cmd)
	})
	first := New(binary, runner)
	if _, err := first.Probe(t.Context()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "--version,exec --help,debug models" {
		t.Fatalf("first probe calls = %q", calls)
	}
	calls = nil
	second := New(binary, runner)
	if _, err := second.Probe(t.Context()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "--version" {
		t.Fatalf("second probe of the same binary calls = %q", calls)
	}
	models, def, err := second.Models(t.Context())
	if err != nil || len(models) != 2 || def != first.defaultModel {
		t.Fatalf("cached catalog = %v %q %v", models, def, err)
	}

	// A replaced binary is probed in full again.
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n# replaced\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(binary, later, later); err != nil {
		t.Fatal(err)
	}
	calls = nil
	if _, err := New(binary, runner).Probe(t.Context()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "--version,exec --help,debug models" {
		t.Fatalf("replaced binary calls = %q", calls)
	}
}

func TestProbeNeverCachesIncompatibleHelp(t *testing.T) {
	binary := fakeProbeBinary(t, false)
	for range 2 {
		calls := 0
		runner := probeRunnerFunc(func(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
			calls++
			return (&capturingRunner{probeCompatible: false}).Run(ctx, cmd)
		})
		if _, err := New(binary, runner).Probe(t.Context()); err == nil || calls != 2 {
			t.Fatalf("incompatible CLI accepted or help skipped: calls=%d err=%v", calls, err)
		}
	}
}
