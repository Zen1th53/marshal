package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

const agyAuthFailure = "Marshal model turn: exit status 1: Authentication required. Please visit the URL to log in (authentication failed or timed out)"

func crossReviewFixture(t *testing.T, tools ...string) (*Runtime, *MarshalService) {
	t.Helper()
	r := openNetpolRuntime(t)
	bin := t.TempDir()
	for _, name := range tools {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	s := r.Marshal()
	if s == nil {
		t.Fatal("no marshal service")
	}
	return r, s
}

func runCrossReview(t *testing.T, r *Runtime, s *MarshalService, worker string, turn func(string) (marshal.Review, error)) (marshal.Review, string, []string, error) {
	var ran []string
	review, provider, err := r.crossReviewFailover(t.Context(), s, "claude", marshal.Task{PlanTaskID: "sub"}, marshal.HandIn{Provider: worker}, func(p string) (marshal.Review, error) {
		ran = append(ran, p)
		return turn(p)
	})
	return review, provider, ran, err
}

func TestCrossReviewFailsOverFromAuthFailure(t *testing.T) {
	if !providerLimitFallback {
		t.Skip("review failover is a private-edition behaviour")
	}
	r, s := crossReviewFixture(t, "agy", "claude", "codex")
	var activity []string
	s.RecordActivity = func(line string) { activity = append(activity, line) }
	review, provider, ran, err := runCrossReview(t, r, s, "codex", func(p string) (marshal.Review, error) {
		if p == "agy" {
			return marshal.Review{}, errors.New(agyAuthFailure)
		}
		return marshal.Review{Verdict: marshal.VerdictAccept}, nil
	})
	if err != nil || provider == "agy" || provider == "codex" || len(ran) != 2 || ran[0] != "agy" || review.Verdict != marshal.VerdictAccept {
		t.Fatalf("review=%v provider=%q ran=%v err=%v", review.Verdict, provider, ran, err)
	}
	if len(activity) != 1 || !strings.Contains(activity[0], "authentication required") {
		t.Fatalf("activity = %q", activity)
	}
}

func TestCrossReviewPausesWithoutEligibleAlternative(t *testing.T) {
	r, s := crossReviewFixture(t, "agy", "codex")
	_, _, ran, err := runCrossReview(t, r, s, "codex", func(string) (marshal.Review, error) {
		return marshal.Review{}, errors.New(agyAuthFailure)
	})
	if err == nil || !strings.Contains(err.Error(), "Authentication required") || len(ran) != 1 {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if providerLimitFallback && !strings.Contains(err.Error(), "no other reviewer provider is eligible") {
		t.Fatalf("reason not stated: %v", err)
	}
}

func TestCrossReviewSkipsDisabledProviderInSameRun(t *testing.T) {
	r, s := crossReviewFixture(t, "agy", "claude", "codex")
	_, first, _, _ := runCrossReview(t, r, s, "codex", func(string) (marshal.Review, error) { return marshal.Review{Verdict: marshal.VerdictAccept}, nil })
	if first != "agy" {
		t.Skipf("agy is not the preferred reviewer here (%s)", first)
	}
	settings, err := s.Store.GetMarshalSettings(t.Context(), s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.Value.SetProviderEnabled("agy", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.SetMarshalSettings(t.Context(), s.ProjectID, settings.Value, settings.Revision); err != nil {
		t.Fatal(err)
	}
	_, second, ran, err := runCrossReview(t, r, s, "codex", func(string) (marshal.Review, error) { return marshal.Review{Verdict: marshal.VerdictAccept}, nil })
	if err != nil || second == "agy" || len(ran) != 1 {
		t.Fatalf("provider=%q ran=%v err=%v", second, ran, err)
	}
}

func TestCrossReviewReturnVerdictIsNotOverridden(t *testing.T) {
	r, s := crossReviewFixture(t, "agy", "claude", "codex")
	review, provider, ran, err := runCrossReview(t, r, s, "codex", func(string) (marshal.Review, error) {
		return marshal.Review{Verdict: marshal.VerdictReturn}, nil
	})
	if err != nil || review.Verdict != marshal.VerdictReturn || len(ran) != 1 || provider != ran[0] {
		t.Fatalf("review=%v provider=%q ran=%v err=%v", review.Verdict, provider, ran, err)
	}
}
