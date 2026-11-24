//go:build governed

package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestGovernedVerificationTimeoutRemainsFixedAt15Minutes(t *testing.T) {
	ctx := t.Context()
	if verificationTimeout != 15*time.Minute {
		t.Fatalf("verificationTimeout = %v, want 15m", verificationTimeout)
	}
	// Even if legacy or external JSON includes a timeout value, governed keeps 15m.
	var s marshal.Settings
	if err := json.Unmarshal([]byte(`{"ExecutionRights":"read-only","AcceptanceMode":"marshal-then-user","ReworkLimit":2,"UltraConcurrency":3,"verification_timeout_seconds":0}`), &s); err != nil {
		t.Fatal(err)
	}
	if got := effectiveVerificationTimeout(s); got != 15*time.Minute {
		t.Fatalf("effectiveVerificationTimeout = %v, want 15m", got)
	}
	// Catalogue locks timeout, stall, failover, infra retry and opencode in governed edition.
	for _, key := range []string{"verification-timeout-seconds", "infra-retry-limit", "infra-failover-streak", "infra-cooldown-seconds", "failover-order", "worker-stall-minutes", "opencode-enabled"} {
		if _, err := settingscatalog.Apply(marshal.DefaultSettings(), key, "0"); err == nil {
			t.Fatalf("governed edition unlocked %s", key)
		}
	}
	if got := settingscatalog.Effective(marshal.DefaultSettings(), "verification-timeout-seconds"); got != "900" {
		t.Fatalf("catalogue effective verification-timeout-seconds = %q, want 900", got)
	}
	repo := testgit.New(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close() })
	if got := rt.effectiveVerificationTimeout(ctx); got != 15*time.Minute {
		t.Fatalf("Runtime.effectiveVerificationTimeout = %v, want 15m", got)
	}
}

func TestGovernedProviderDisableRefusesWithoutFailover(t *testing.T) {
	ctx := t.Context()
	s, _ := marshalFixture(t, 2)
	startIntegrityRun(t, s, marshal.Budget{})
	run, rev, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	for i := range run.Tasks {
		run.Tasks[i].Worker = "codex"
		run.Tasks[i].Mode = marshal.Governed
		run.Tasks[i].DependsOn = nil
	}
	if err := s.save(ctx, "run", run, rev); err != nil {
		t.Fatal(err)
	}
	drivers := map[string]driver.Driver{
		"codex":  &rateLimitedDriver{},
		"claude": &rateLimitedDriver{},
		"agy":    &rateLimitedDriver{},
	}
	s.Drivers = drivers
	s.GovernedDrivers = drivers

	// First task launches before codex is disabled.
	d1, err := s.Dispatch(ctx, "run", "a", "first task")
	if err != nil {
		t.Fatalf("initial dispatch: %v", err)
	}
	// Disable codex via project settings.
	rec, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := settingscatalog.Apply(rec.Value, "codex-enabled", "off")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Store.CommitMarshalSettings(ctx, s.ProjectID, updated, rec.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	// Already-admitted task finishes normally.
	if _, err := s.CollectHandIn(ctx, "run", d1); err != nil {
		t.Fatalf("collect first task: %v", err)
	}
	// Next admission on disabled codex refuses without automatic failover.
	if _, err := s.Dispatch(ctx, "run", "b", "second task"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("governed dispatch on disabled provider = %v, want disabled error", err)
	}
	after, _, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if after.Tasks[1].Worker != "codex" || after.Tasks[1].State != marshal.Queued {
		t.Fatalf("governed edition failed over disabled task: %+v", after.Tasks[1])
	}
	// Reassignment skips disabled provider and rejects reassigning onto it.
	if got := s.otherWorkerFor(ctx, run.Settings, "claude", marshal.Governed); got != "agy" {
		t.Fatalf("otherWorkerFor = %s, want agy", got)
	}
	after, rev, _ = s.load(ctx, "run")
	after.Tasks[1].Worker = "claude"
	after.Tasks[1].State = marshal.Escalated
	if err := s.save(ctx, "run", after, rev); err != nil {
		t.Fatal(err)
	}
	if err := s.Reassign(ctx, "run", "b", "codex"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("reassign to disabled provider = %v, want disabled error", err)
	}
}
