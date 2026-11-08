package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func harnessReverifyTestProfile(harness, modelName string, probedAt, expiresAt time.Time) model.HarnessProfile {
	return model.HarnessProfile{
		Harness:          harness,
		InstalledVersion: "test-version",
		SupportedModels:  []string{modelName},
		DefaultModel:     modelName,
		ProbeEvidenceID:  "PROBE-test",
		ProbedAt:         probedAt,
		ExpiresAt:        expiresAt,
		FeatureSupport:   map[string]model.FeatureStatus{"headless": model.StatusNative, "sandbox": model.StatusEmulated},
	}
}

func TestHarnessProfileReverifyDueInsideMargin(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	profile := harnessReverifyTestProfile("codex", "gpt-test", now.Add(-23*time.Hour), now.Add(time.Hour))

	due := harnessProfilesDueForReverify([]model.HarnessProfile{profile}, nil, nil, now)
	if len(due) != 1 {
		t.Fatalf("due profiles = %d, want 1", len(due))
	}
	if due[0].Provider != "codex" || due[0].Model != "gpt-test" || due[0].Native {
		t.Fatalf("reverify candidate = %+v", due[0])
	}

	native := harnessReverifyTestProfile("antigravity", "agy-test", now.Add(-23*time.Hour), now.Add(time.Hour))
	native.FeatureSupport = map[string]model.FeatureStatus{"headless": model.StatusNative, "sandbox": model.StatusUnsupported}
	due = harnessProfilesDueForReverify([]model.HarnessProfile{native}, nil, nil, now)
	if len(due) != 1 || due[0].Provider != "agy" || due[0].Model != "agy-test" || !due[0].Native {
		t.Fatalf("native reverify candidate = %+v", due)
	}
}

func TestHarnessProfileReverifyNotDueOutsideMargin(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		profile model.HarnessProfile
	}{
		{"too early", harnessReverifyTestProfile("codex", "gpt-test", now.Add(-20*time.Hour), now.Add(3*time.Hour))},
		{"already expired", harnessReverifyTestProfile("codex", "gpt-test", now.Add(-25*time.Hour), now.Add(-time.Minute))},
	}
	neverVerified := harnessReverifyTestProfile("codex", "gpt-test", now.Add(-23*time.Hour), now.Add(time.Hour))
	neverVerified.ProbeEvidenceID = ""
	cases = append(cases, struct {
		name    string
		profile model.HarnessProfile
	}{"never verified", neverVerified})
	for _, tc := range cases {
		if due := harnessProfilesDueForReverify([]model.HarnessProfile{tc.profile}, nil, nil, now); len(due) != 0 {
			t.Fatalf("%s profile was due: %+v", tc.name, due)
		}
	}
}

func TestHarnessProfileReverifyNotDueDuringCooldownOrAfterFailure(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	profile := harnessReverifyTestProfile("codex", "gpt-test", now.Add(-23*time.Hour), now.Add(time.Hour))

	if due := harnessProfilesDueForReverify([]model.HarnessProfile{profile}, nil, map[string]bool{"codex": true}, now); len(due) != 0 {
		t.Fatalf("cooling provider was due: %+v", due)
	}
	if due := harnessProfilesDueForReverify([]model.HarnessProfile{profile}, map[string]harnessReverifyState{"codex": {InFlight: true}}, nil, now); len(due) != 0 {
		t.Fatalf("in-flight provider was due: %+v", due)
	}
	failed := map[string]harnessReverifyState{"codex": {LastFailed: true, LastAttempt: now.Add(-5 * time.Minute)}}
	if due := harnessProfilesDueForReverify([]model.HarnessProfile{profile}, failed, nil, now); len(due) != 0 {
		t.Fatalf("provider retried too soon after failure: %+v", due)
	}
	retry := map[string]harnessReverifyState{"codex": {LastFailed: true, LastAttempt: now.Add(-harnessReverifyRetryInterval - time.Minute)}}
	if due := harnessProfilesDueForReverify([]model.HarnessProfile{profile}, retry, nil, now); len(due) != 1 {
		t.Fatalf("provider not retried after retry interval: %+v", due)
	}
}

func TestHarnessProfileReverifyFailureRecordedWithoutExpiryChange(t *testing.T) {
	providerHealth.Clear("codex")
	defer providerHealth.Clear("codex")

	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	now := time.Now().UTC()
	profile := harnessReverifyTestProfile("codex", "gpt-test", now.Add(-23*time.Hour), now.Add(time.Hour))
	if err := runtime.Store().SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	saved, err := runtime.Store().GetHarnessProfile(t.Context(), "codex")
	if err != nil {
		t.Fatal(err)
	}

	reverifier := newHarnessReverifier(runtime)
	var calls int
	var seen harnessReverifyCandidate
	reverifier.probe = func(_ context.Context, candidate harnessReverifyCandidate) (model.HarnessProfile, error) {
		calls++
		seen = candidate
		return model.HarnessProfile{}, errors.New("probe refused by test")
	}
	var activity []string
	reverifier.activity = func(message string) { activity = append(activity, message) }

	reverifier.runOnce(t.Context())
	if calls != 1 {
		t.Fatalf("probe calls = %d, want 1", calls)
	}
	if seen.Provider != "codex" || seen.Model != "gpt-test" || seen.Native {
		t.Fatalf("reverify did not repeat the verified provider/model: %+v", seen)
	}
	after, err := runtime.Store().GetHarnessProfile(t.Context(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if !after.ExpiresAt.Equal(saved.ExpiresAt) || !after.ProbedAt.Equal(saved.ProbedAt) || after.ProbeEvidenceID != saved.ProbeEvidenceID {
		t.Fatalf("failed re-verify changed the profile: before=%+v after=%+v", saved, after)
	}
	events, err := runtime.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "HARNESS_VERIFY_FAILED" && event.Data["provider"] == "codex" && event.Data["model"] == "gpt-test" && strings.Contains(event.Data["error"].(string), "probe refused by test") {
			found = true
		}
	}
	if !found {
		t.Fatal("failed re-verify left no HARNESS_VERIFY_FAILED record")
	}
	if len(activity) == 0 || !strings.Contains(activity[0], "probe refused by test") {
		t.Fatalf("failed re-verify left no activity line: %v", activity)
	}

	reverifier.runOnce(t.Context())
	if calls != 1 {
		t.Fatalf("failed re-verify retried before the retry interval: %d calls", calls)
	}
}

func TestHarnessProfileReverifySuccessRenewsEvidence(t *testing.T) {
	providerHealth.Clear("codex")
	defer providerHealth.Clear("codex")
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	now := time.Now().UTC()
	profile := harnessReverifyTestProfile("codex", "gpt-test", now.Add(-23*time.Hour), now.Add(time.Hour))
	if err := runtime.Store().SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	reverifier := newHarnessReverifier(runtime)
	reverifier.now = func() time.Time { return now }
	calls := 0
	reverifier.probe = func(ctx context.Context, candidate harnessReverifyCandidate) (model.HarnessProfile, error) {
		calls++
		if candidate.Provider != "codex" || candidate.Model != "gpt-test" {
			t.Fatalf("unexpected probe: %+v", candidate)
		}
		fresh := harnessReverifyTestProfile(candidate.Harness, candidate.Model, now, now.Add(24*time.Hour))
		fresh.ProbeEvidenceID = "PROBE-renewed"
		return fresh, runtime.Store().SaveHarnessProfile(ctx, fresh)
	}
	var activity string
	reverifier.activity = func(message string) { activity = message }
	reverifier.runOnce(t.Context())
	reverifier.runOnce(t.Context())
	after, err := runtime.Store().GetHarnessProfile(t.Context(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || after.ProbeEvidenceID != "PROBE-renewed" || !after.ExpiresAt.Equal(now.Add(24*time.Hour)) || !strings.Contains(activity, "PROBE-renewed") {
		t.Fatalf("renewal: calls=%d profile=%+v activity=%q", calls, after, activity)
	}
}
