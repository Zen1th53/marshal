package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/projectcontext"
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

// The final check excludes only providers that cannot take the role: not
// installed, no Marshal MCP, limited, or unable to name the model that
// answered. Doing the run's tasks does not exclude a provider; one that did
// none of them is preferred when one is eligible.
func TestVerifierProviderExcludesOnlyIneligibleProviders(t *testing.T) {
	r, _ := verifierProviderFixture(t, "claude", "codex", "agy", "opencode")
	for _, p := range []string{"claude", "codex", "agy", "opencode"} {
		providerHealth.Clear(p)
		t.Cleanup(func() { providerHealth.Clear(p) })
	}
	run := marshal.Run{Tasks: []marshal.Task{{Worker: "claude-code"}, {Worker: "antigravity"}}}
	providerHealth.Mark("codex", "quota exhausted", time.Now().Add(time.Hour))
	providerHealth.Mark("claude", "quota exhausted", time.Now().Add(time.Hour))
	// agy and opencode stay limited: this test is about the other two.
	providerHealth.Mark("agy", "rate limit", time.Now().Add(time.Hour))
	providerHealth.Mark("opencode", "rate limit", time.Now().Add(time.Hour))
	p, _, err := r.independentVerifierChoice(run)
	if err == nil || p != "" {
		t.Fatalf("limited verifier selected: %q %v", p, err)
	}
	for _, want := range []string{"claude", "codex", "quota exhausted", "agy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("exclusions missing %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "worker provider") {
		t.Errorf("a worker's provider is excluded for doing the tasks: %v", err)
	}
	// Claude Code did tasks; with its limit gone it checks them in a fresh
	// session, unless Codex (which did none) can take the role.
	r.clearProviderLimit("claude")
	p, worker, err := r.independentVerifierChoice(run)
	if err != nil || p != "claude" || !worker {
		t.Fatalf("worker-provider fallback: %q worker=%v %v", p, worker, err)
	}
	r.clearProviderLimit("codex")
	p, worker, err = r.independentVerifierChoice(run)
	if gap := requiredVerifierAttributionGap("codex"); gap != "" {
		if err != nil || p != "claude" || !worker {
			t.Fatalf("unattributable Codex displaced Claude Code: %q %v", p, err)
		}
	} else if err != nil || p != "codex" || worker {
		t.Fatalf("a provider that did none of the tasks was not preferred: %q worker=%v %v", p, worker, err)
	}
}

func TestVerifierProviderNamesUninstalledProviders(t *testing.T) {
	_, s := verifierProviderFixture(t)
	p, err := s.VerifierProvider(t.Context(), marshal.Run{Tasks: []marshal.Task{{Worker: "claude"}}})
	if p != "" || !errors.Is(err, errIndependentVerifierUnavailable) {
		t.Fatalf("verifier without an installed provider: %q %v", p, err)
	}
	for _, want := range []string{"codex", "not installed", "claude", "agy"} {
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
	if err != nil || paused.State != marshal.AwaitingUser || paused.Pause == nil || !strings.Contains(paused.Pause.Detail, "codex: not installed") || strings.Contains(paused.Pause.Reason, "/marshal") || !strings.Contains(paused.Pause.Resolutions[0], "press Enter") || paused.Pause.ResumeState != marshal.Verifying {
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

// A verification stop whose cause is only a refusal code reads in plain words
// with Enter as the next step; the code stays in the pause detail and the
// recorded event's reason.
func TestBareCodeVerificationStopReadsPlain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		pause func(*MarshalService, string, marshal.Run, int64, error) error
	}{
		{"verifier review pending", projectcontext.ErrReviewPending, func(s *MarshalService, id string, run marshal.Run, rev int64, cause error) error {
			return s.pauseUnavailableVerifier(t.Context(), id, run, rev, cause)
		}},
		{"verification changed", projectcontext.Changed("x.go:1"), func(s *MarshalService, id string, _ marshal.Run, _ int64, cause error) error {
			return s.pauseVerificationFailure(t.Context(), id, cause)
		}},
		{"verifier output", fmt.Errorf("%w: 1 assessments for 2 criteria", errRequiredVerifierOutput), func(s *MarshalService, id string, run marshal.Run, rev int64, cause error) error {
			return s.pauseUnavailableVerifier(t.Context(), id, run, rev, cause)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			startIntegrityRun(t, s, marshal.Budget{})
			run, rev, err := s.load(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			run.State = marshal.Verifying
			if err = s.save(t.Context(), "run", run, rev); err != nil {
				t.Fatal(err)
			}
			run, rev, _ = s.load(t.Context(), "run")
			if err = tc.pause(s, "run", run, rev, tc.cause); !errors.Is(err, tc.cause) {
				t.Fatalf("cause lost: %v", err)
			}
			paused, err := s.Snapshot(t.Context(), "run")
			if err != nil || paused.Pause == nil {
				t.Fatal(err)
			}
			p := paused.Pause
			if !strings.HasPrefix(p.Reason, "Marshal could not finish checking the run: ") || strings.Contains(p.Reason, "REQUIRED_") || len(p.Resolutions) != 1 || p.Resolutions[0] != marshal.VerificationRetry || !strings.Contains(p.Detail, strings.SplitN(tc.cause.Error(), " ", 2)[0]) || p.Code != marshal.CodeVerificationStopped || p.ResumeState != marshal.Verifying {
				t.Fatalf("stop words: %+v", p)
			}
			decisions, err := s.Store.MarshalDecisions(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			recorded := false
			for _, ev := range decisions {
				if ev.Type == events.EventTypeMarshalEscalated && ev.Data["summary"] == p.Reason && strings.Contains(fmt.Sprint(ev.Data["reason"]), strings.SplitN(tc.cause.Error(), " ", 2)[0]) {
					recorded = true
				}
			}
			if !recorded {
				t.Fatal("escalation lost the raw reason or the summary")
			}
		})
	}
}

func verifyingRunOn(t *testing.T, workers ...string) *MarshalService {
	t.Helper()
	// An ULTRA run whose tasks are accepted and merged, ready for the
	// final check; the workers are then named as the providers given.
	s, _ := marshalFixture(t, len(workers))
	s.Gate = marshalTestGate(true)
	s.ModelProvider = "test"
	s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
		return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "second", EvidenceRefs: []string{"diff"}}, "other", nil
	}
	startIntegrityRun(t, s, marshal.Budget{})
	for i := range workers {
		id := string(rune('a' + i))
		acceptIntegrityTask(t, s, id)
		if err := s.Merge(t.Context(), "run", id); err != nil {
			t.Fatal(err)
		}
	}
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	for i, w := range workers {
		run.Tasks[i].Worker = w
	}
	if err = s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, p := range []string{"claude", "codex"} {
		providerHealth.Clear(p)
		t.Cleanup(func() { providerHealth.Clear(p) })
		if err := os.WriteFile(filepath.Join(bin, p), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	s.ModelProvider = "claude"
	return s
}

// The Mac demo: Claude Code and Codex did the run's tasks and only Claude
// Code can do the final check. It checks them in a fresh session of its
// own, the check passes, and the evidence records the fresh session and the
// worker-provider fallback. The pause "Claude Code did this run's tasks, so
// it cannot check them" no longer occurs.
func TestFinalCheckByWorkerProviderInFreshSessionPasses(t *testing.T) {
	s := verifyingRunOn(t, "claude", "codex")
	r := &Runtime{}
	s.VerifierProvider = func(_ context.Context, run marshal.Run) (string, error) { return r.independentVerifierProvider(run) }
	var verifier string
	s.IndependentVerify = func(ctx context.Context, run marshal.Run, head string, session verification.Session) (marshal.VerifierEvidence, error) {
		provider, err := s.VerifierProvider(ctx, run)
		if err != nil {
			return marshal.VerifierEvidence{}, err
		}
		verifier = provider
		return marshal.VerifierEvidence{Reviewer: "verifier:" + provider, Provider: provider, SessionID: "fresh-verifier-session", FreshSession: true, Commit: head, Verdict: "pass", InputDigest: verifierInputDigest(run, head, session)}, nil
	}
	result, err := s.VerifyMerged(t.Context(), "run", knownCharge())
	if err != nil || result != verification.VerifiedComplete {
		t.Fatalf("final check by Claude Code in a fresh session: %s %v", result, err)
	}
	// Where Codex names its model too (Community), the first eligible
	// provider, Codex, checks instead; it also did tasks.
	if want := map[bool]string{true: "claude", false: "codex"}[requiredVerifierAttributionGap("codex") != ""]; verifier != want {
		t.Fatalf("verifier %q, want %s", verifier, want)
	}
	decisions, err := s.Store.MarshalDecisions(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	recorded := false
	for _, ev := range decisions {
		if ev.Type == events.EventTypeMarshalVerifierRecorded && ev.Data["verifier_same_provider_fallback"] == true && ev.Data["verifier_fresh_session"] == true && ev.Data["verification_error"] == nil {
			recorded = true
		}
		if ev.Type == events.EventTypeMarshalEscalated && strings.Contains(fmt.Sprint(ev.Data["summary"]), "cannot check them") {
			t.Fatalf("the old pause occurred: %v", ev.Data)
		}
	}
	if !recorded {
		t.Fatal("verifier evidence does not record the fresh session and the fallback")
	}
}

// A verifier that reuses a worker's session, or of a worker's provider
// without a fresh session of its own, is refused.
func TestFinalCheckRefusesVerifierReusingWorkerSession(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record marshal.VerifierEvidence
		want   string
	}{
		{"resumed session", marshal.VerifierEvidence{SessionID: "resumed", FreshSession: false}, "did not run in a fresh session"},
		{"no session id", marshal.VerifierEvidence{FreshSession: true}, "did not run in a fresh session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := verifyingRunOn(t, "claude")
			s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "claude", nil }
			s.IndependentVerify = func(_ context.Context, run marshal.Run, head string, session verification.Session) (marshal.VerifierEvidence, error) {
				record := tc.record
				record.Reviewer, record.Provider, record.Commit, record.Verdict, record.InputDigest = "verifier:claude", "claude", head, "pass", verifierInputDigest(run, head, session)
				return record, nil
			}
			if result, err := s.VerifyMerged(t.Context(), "run", knownCharge()); err == nil || result == verification.VerifiedComplete || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("verifier without a fresh session accepted: %s %v", result, err)
			}
		})
	}
	for _, record := range []marshal.VerifierEvidence{
		{Provider: "codex", SessionID: "resumed"},
		{Provider: "codex", FreshSession: true},
	} {
		s := &MarshalService{}
		if err := s.checkVerifierFreshSession(record, []string{"claude"}, nil, map[string]any{}); err == nil || !strings.Contains(err.Error(), "did not run in a fresh session") {
			t.Fatalf("different-provider verifier accepted: %v", err)
		}
	}
	// The session check itself: a worker's observed session is refused for
	// any provider, as is the Marshal's own conversation.
	s := &MarshalService{Model: &MarshalCLI{ConversationID: "marshal-session"}}
	data := map[string]any{}
	if err := s.checkVerifierFreshSession(marshal.VerifierEvidence{Provider: "codex", SessionID: "worker-session", FreshSession: true}, []string{"claude"}, []string{"worker-session"}, data); err == nil || !strings.Contains(err.Error(), "reused a worker's session") {
		t.Fatalf("worker session reuse accepted: %v", err)
	}
	if err := s.checkVerifierFreshSession(marshal.VerifierEvidence{Provider: "codex", SessionID: "marshal-session", FreshSession: true}, []string{"claude"}, nil, data); err == nil || !strings.Contains(err.Error(), "the Marshal's session") {
		t.Fatalf("Marshal session reuse accepted: %v", err)
	}
	if err := s.checkVerifierFreshSession(marshal.VerifierEvidence{Provider: "claude", SessionID: "new", FreshSession: true}, []string{"claude"}, []string{"worker-session"}, data); err != nil || data["verifier_same_provider_fallback"] != true {
		t.Fatalf("fresh worker-provider verifier refused: %v %v", err, data)
	}
}

// A cross-review by the worker's own provider is accepted only from a fresh
// session distinct from the worker's, and records the fallback.
func TestCrossReviewByWorkerProviderNeedsFreshSession(t *testing.T) {
	h := marshal.HandIn{Worker: "claude", Provider: "claude", RuntimeObserved: []marshal.CommandRecord{{Command: "process05 provider-observation EV-1", Output: `{"provider":"claude","run_id":"r","task_id":"a","attempt":1,"session_id":"worker-session","timing_basis":"x","usage":{}}` + "\nRuntime lifecycle observation; paid inference intervals are not recorded."}}}
	if got := handInWorkerSessions(h); len(got) != 1 || got[0] != "worker-session" {
		t.Fatalf("worker sessions: %v", got)
	}
	data := map[string]any{}
	if err := checkCrossReviewFreshSession(marshal.Review{SessionID: "worker-session", FreshSession: true}, "claude", h, data); err == nil {
		t.Fatal("cross-review in the worker's session accepted")
	}
	if err := checkCrossReviewFreshSession(marshal.Review{SessionID: "resumed"}, "claude", h, data); err == nil {
		t.Fatal("cross-review without a fresh session accepted")
	}
	data = map[string]any{}
	if err := checkCrossReviewFreshSession(marshal.Review{SessionID: "new", FreshSession: true}, "claude", h, data); err != nil || data["cross_review_same_provider_fallback"] != true || data["cross_review_session_id"] != "new" {
		t.Fatalf("fresh same-provider cross-review: %v %v", err, data)
	}
	data = map[string]any{}
	if err := checkCrossReviewFreshSession(marshal.Review{}, "codex", h, data); err != nil || data["cross_review_same_provider_fallback"] != nil {
		t.Fatalf("other-provider cross-review: %v %v", err, data)
	}
}
