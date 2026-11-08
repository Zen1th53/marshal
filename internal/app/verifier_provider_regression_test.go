package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/verification"
)

func verifierProviderFixture(t *testing.T, installed ...string) (*Runtime, *MarshalService) {
	t.Helper()
	fixture, _ := marshalFixture(t, 1)
	bin := t.TempDir()
	for _, p := range installed {
		if err := os.WriteFile(filepath.Join(bin, p), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	r := &Runtime{store: fixture.Store, layout: project.Layout{Root: fixture.Repository, Worktrees: fixture.Worktrees}}
	s, err := r.MarshalWired(MarshalWiring{Provider: "claude", Approver: func(context.Context, string, string) (string, error) { return "owner", nil }})
	if err != nil {
		t.Fatal(err)
	}
	return r, s
}

func TestVerifierProviderExcludesAllWorkersAndLimits(t *testing.T) {
	r, s := verifierProviderFixture(t, "claude", "codex", "agy", "opencode")
	for _, p := range []string{"claude", "codex", "agy", "opencode"} {
		providerHealth.Clear(p)
		t.Cleanup(func() { providerHealth.Clear(p) })
	}
	run := marshal.Run{Tasks: []marshal.Task{{Worker: "claude-code"}, {Worker: "antigravity"}}}
	providerHealth.Mark("codex", "quota exhausted", time.Now().Add(time.Hour))
	p, err := s.VerifierProvider(t.Context(), run)
	if err == nil || p != "" {
		t.Fatalf("unavailable independent verifier selected: %q %v", p, err)
	}
	for _, want := range []string{"claude", "worker", "codex", "quota exhausted", "agy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("exclusions missing %q: %v", want, err)
		}
	}
	if privateAGYEnabled() && !strings.Contains(err.Error(), "opencode") {
		t.Errorf("missing MCP exclusion: %v", err)
	}
	r.clearProviderLimit("codex")
	p, err = s.VerifierProvider(t.Context(), run)
	if err != nil || p != "codex" {
		t.Fatalf("successful harness verify did not restore independent Codex: %q %v", p, err)
	}
	run.Tasks = append(run.Tasks, marshal.Task{Worker: "codex"})
	if p, err = s.VerifierProvider(t.Context(), run); err == nil || p != "" {
		t.Fatalf("fell back to a worker: %q %v", p, err)
	}
}

func TestVerifierProviderNamesUninstalledProviders(t *testing.T) {
	_, s := verifierProviderFixture(t, "claude")
	p, err := s.VerifierProvider(t.Context(), marshal.Run{Tasks: []marshal.Task{{Worker: "claude"}}})
	if p != "" || err == nil {
		t.Fatalf("worker fallback: %q %v", p, err)
	}
	for _, want := range []string{"codex", "not installed", "claude", "worker", "agy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("exclusions missing %q: %v", want, err)
		}
	}
}

func TestUnavailableVerifierPausesRunWithExclusions(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.State = marshal.Verifying
	run.Tier = marshal.Ultra
	run.Tasks[0].Worker = "claude"
	if err = s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	r := &Runtime{}
	s.ModelProvider = "claude"
	s.VerifierProvider = func(_ context.Context, run marshal.Run) (string, error) { return r.independentVerifierProvider(run) }
	s.IndependentVerify = func(context.Context, marshal.Run, string, verification.Session) (marshal.VerifierEvidence, error) {
		t.Fatal("worker verifier ran")
		return marshal.VerifierEvidence{}, nil
	}
	if _, err = s.VerifyMerged(t.Context(), "run", knownCharge()); err == nil {
		t.Fatal("missing independent verifier accepted")
	}
	paused, err := s.Snapshot(t.Context(), "run")
	if err != nil || paused.State != marshal.AwaitingUser || paused.Pause == nil || !strings.Contains(paused.Pause.Reason, "codex: not installed") || paused.Pause.ResumeState != marshal.Verifying {
		t.Fatalf("no durable verifier pause: %+v %v", paused, err)
	}
}

// Provider independence is from the workers. The Marshal's provider remains
// eligible, so an unnecessary third provider cannot displace it.
func TestVerifierProviderCanUseMarshalProvider(t *testing.T) {
	_, s := verifierProviderFixture(t, "claude", "codex", "agy")
	for _, p := range []string{"claude", "codex", "agy"} {
		providerHealth.Clear(p)
		t.Cleanup(func() { providerHealth.Clear(p) })
	}
	provider, err := s.VerifierProvider(t.Context(), marshal.Run{Tasks: []marshal.Task{{Worker: "codex"}}})
	if err != nil || provider != "claude" {
		t.Fatalf("independent Marshal provider displaced: %q %v", provider, err)
	}
}
