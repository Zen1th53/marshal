//go:build !governed

package app

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"github.com/Zen1th53/marshal/internal/worker"
)

func TestProviderDisableAppliesToNextAdmission(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
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
		"codex":  &extremeMockDriver{mode: marshal.Governed},
		"claude": &extremeMockDriver{mode: marshal.Governed},
		"agy":    &extremeMockDriver{mode: marshal.Governed},
	}
	s.Drivers = drivers
	s.GovernedDrivers = drivers

	// First task is admitted before codex is disabled.
	d1, err := s.Dispatch(ctx, "run", "a", "task a")
	if err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	if d1.Handle.Request().Task.Worker != "codex" {
		t.Fatalf("first dispatch worker = %s, want codex", d1.Handle.Request().Task.Worker)
	}

	// Disable codex and prefer agy over claude in project settings while task a is active.
	rec, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := settingscatalog.Apply(rec.Value, "codex-enabled", "off")
	if err != nil {
		t.Fatal(err)
	}
	updated, err = settingscatalog.Apply(updated, "failover-order", "codex,agy,claude")
	if err != nil {
		t.Fatal(err)
	}
	revSettings, _, err := s.Store.CommitMarshalSettings(ctx, s.ProjectID, updated, rec.Revision, "owner")
	if err != nil {
		t.Fatal(err)
	}

	// The already-admitted worker completes without disruption.
	if _, err := s.CollectHandIn(ctx, "run", d1); err != nil {
		t.Fatalf("collect already-admitted task: %v", err)
	}

	// Next admission uses the project snapshot: codex is disabled, so task b fails over to agy.
	d2, err := s.Dispatch(ctx, "run", "b", "task b")
	if err != nil {
		t.Fatalf("second dispatch after disabling codex: %v", err)
	}
	if got := d2.Handle.Request().Task.Worker; got != "agy" {
		t.Fatalf("second dispatch worker = %s, want agy from project failover snapshot", got)
	}

	// Reassignment helpers and explicit Reassign also honor the project snapshot.
	if got := s.otherWorkerFor(ctx, run.Settings, "claude", marshal.Governed); got != "agy" {
		t.Fatalf("otherWorkerFor = %s, want agy (skipping disabled codex)", got)
	}
	runNow, runRev, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	runNow.Tasks[1].State = marshal.Escalated
	if err := s.save(ctx, "run", runNow, runRev); err != nil {
		t.Fatal(err)
	}
	if err := s.Reassign(ctx, "run", "b", "codex"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("reassign to disabled provider = %v, want disabled error", err)
	}

	// Disabling every remaining provider refuses admission rather than launching a disabled worker.
	for _, key := range []string{"agy-enabled", "claude-enabled", "opencode-enabled"} {
		updated, err = settingscatalog.Apply(updated, key, "off")
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Store.CommitMarshalSettings(ctx, s.ProjectID, updated, revSettings, "owner"); err != nil {
		t.Fatal(err)
	}
	runNow, runRev, err = s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	runNow.Tasks[1].State = marshal.Queued
	runNow.Tasks[1].Worker = "codex"
	if err := s.save(ctx, "run", runNow, runRev); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(ctx, "run", "b", "task b blocked"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("dispatch with all providers disabled = %v, want disabled error", err)
	}
}

func TestStallOffRetainsSignals(t *testing.T) {
	dir := t.TempDir()

	// Legacy absent value keeps the 15-minute default.
	legacyCmd := adapter.Command{Dir: dir, StallWindow: time.Minute}
	configureWorkerStall(withWorkerStall(t.Context(), marshal.Settings{}), &legacyCmd)
	if legacyCmd.StallWindow != 15*time.Minute || legacyCmd.StallWorktree != dir {
		t.Fatalf("legacy absent stall command = %+v, want 15m", legacyCmd)
	}

	// Turning stall off removes the stall trigger while keeping observation hooks.
	offSettings, err := settingscatalog.Apply(marshal.DefaultSettings(), "worker-stall-minutes", "off")
	if err != nil {
		t.Fatal(err)
	}
	if !offSettings.WorkerStallDisabled() || offSettings.EffectiveWorkerStallMinutes() != 0 {
		t.Fatalf("off settings = %+v", offSettings)
	}
	zeroSettings, err := settingscatalog.Apply(marshal.DefaultSettings(), "worker-stall-minutes", "0")
	if err != nil {
		t.Fatal(err)
	}
	if !zeroSettings.WorkerStallDisabled() || zeroSettings.EffectiveWorkerStallMinutes() != 0 {
		t.Fatalf("zero settings = %+v", zeroSettings)
	}

	var mu sync.Mutex
	seenStreams := map[string]string{}
	heartbeats := 0
	cmd := adapter.Command{
		Path:              "/bin/sh",
		Args:              []string{"-c", "sleep 0.08; printf 'out-signal'; printf 'err-signal' >&2"},
		Dir:               dir,
		StallWindow:       15 * time.Millisecond,
		StallScanInterval: 10 * time.Millisecond,
		HeartbeatInterval: 10 * time.Millisecond,
		OutputObserver: func(stream string, data []byte) {
			mu.Lock()
			seenStreams[stream] += string(data)
			mu.Unlock()
		},
		Heartbeat: func() {
			mu.Lock()
			heartbeats++
			mu.Unlock()
		},
	}
	configureWorkerStall(withWorkerStall(t.Context(), offSettings), &cmd)
	if cmd.StallWindow != 0 {
		t.Fatalf("StallWindow with stall off = %v, want 0", cmd.StallWindow)
	}
	if cmd.OutputObserver == nil || cmd.Heartbeat == nil {
		t.Fatal("stall off cleared observation callbacks")
	}

	res, err := worker.New(5*time.Second, 200*time.Millisecond, 64<<10).Run(t.Context(), cmd)
	if err != nil {
		t.Fatalf("worker run with stall off failed: %v", err)
	}
	if res.Stalled {
		t.Fatalf("worker unexpectedly stalled: %+v", res)
	}
	mu.Lock()
	gotOut, gotErr, gotBeats := seenStreams["stdout"], seenStreams["stderr"], heartbeats
	mu.Unlock()
	if !strings.Contains(gotOut, "out-signal") || !strings.Contains(gotErr, "err-signal") {
		t.Fatalf("output signals lost: stdout=%q stderr=%q", gotOut, gotErr)
	}
	if gotBeats == 0 {
		t.Fatal("heartbeat observation signals were not retained")
	}
	if !strings.Contains(string(res.Stdout), "out-signal") || !strings.Contains(string(res.Stderr), "err-signal") {
		t.Fatalf("captured buffers lost: stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
}

func TestRuntimeLimitsDistinguishAbsentAndZero(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	ctx := t.Context()

	// 1. Legacy absent JSON uses existing defaults.
	var legacy marshal.Settings
	if err := json.Unmarshal([]byte(`{"ExecutionRights":"read-only","AcceptanceMode":"marshal","ReworkLimit":2,"UltraConcurrency":4}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.InfraRetryLimit != nil || legacy.VerificationTimeoutSeconds != nil {
		t.Fatalf("absent JSON populated optional fields: %+v", legacy)
	}
	if legacy.EffectiveInfraRetryLimit() != 6 || legacy.EffectiveInfraFailoverStreak() != 2 || legacy.EffectiveInfraCooldownSeconds() != 600 {
		t.Fatalf("legacy absent infra defaults changed: limit=%d streak=%d cooldown=%d",
			legacy.EffectiveInfraRetryLimit(), legacy.EffectiveInfraFailoverStreak(), legacy.EffectiveInfraCooldownSeconds())
	}
	if got := effectiveVerificationTimeout(legacy); got != 0 {
		t.Fatalf("legacy absent verification timeout = %v, want 0", got)
	}

	// 2. Explicit zero survives JSON serialization and means unlimited/disabled.
	explicitZero := marshal.DefaultSettings()
	var err error
	for _, key := range []string{"infra-retry-limit", "infra-failover-streak", "infra-cooldown-seconds", "verification-timeout-seconds"} {
		explicitZero, err = settingscatalog.Apply(explicitZero, key, "0")
		if err != nil {
			t.Fatalf("apply %s=0: %v", key, err)
		}
	}
	raw, err := json.Marshal(explicitZero)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip marshal.Settings
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.InfraRetryLimit == nil || *roundTrip.InfraRetryLimit != 0 || roundTrip.EffectiveInfraRetryLimit() != 0 {
		t.Fatalf("explicit zero infra-retry-limit lost across JSON: %+v (%s)", roundTrip, raw)
	}
	if roundTrip.InfraFailoverStreak == nil || *roundTrip.InfraFailoverStreak != 0 || roundTrip.EffectiveInfraFailoverStreak() != 0 {
		t.Fatalf("explicit zero infra-failover-streak lost across JSON: %+v", roundTrip)
	}
	if roundTrip.InfraCooldownSeconds == nil || *roundTrip.InfraCooldownSeconds != 0 || roundTrip.EffectiveInfraCooldownSeconds() != 0 {
		t.Fatalf("explicit zero infra-cooldown-seconds lost across JSON: %+v", roundTrip)
	}
	if roundTrip.VerificationTimeoutSeconds == nil || *roundTrip.VerificationTimeoutSeconds != 0 || effectiveVerificationTimeout(roundTrip) != 0 {
		t.Fatalf("explicit zero verification-timeout-seconds lost across JSON: %+v", roundTrip)
	}

	// 3. Infra retry runtime behavior: absent escalates past 6; explicit 0 is unlimited; explicit 2 escalates past 2.
	runInfraFailures := func(t *testing.T, applySetting string, attempts int) marshal.Task {
		t.Helper()
		s, _, _ := parallelWaitFixture(t, 1)
		run, rev, err := s.load(ctx, "par")
		if err != nil {
			t.Fatal(err)
		}
		if applySetting != "" {
			run.Settings, err = settingscatalog.Apply(run.Settings, "infra-retry-limit", applySetting)
			if err != nil {
				t.Fatal(err)
			}
			run.Settings, err = settingscatalog.Apply(run.Settings, "infra-failover-streak", "0")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.save(ctx, "par", run, rev); err != nil {
				t.Fatal(err)
			}
		}
		for n := 0; n < attempts; n++ {
			cur, curRev, err := s.load(ctx, "par")
			if err != nil {
				t.Fatal(err)
			}
			if cur.Tasks[0].State == marshal.Escalated {
				break
			}
			if err := s.returnInfraFailure(ctx, "par", cur, curRev, 0, "stream: unexpected end of JSON input"); err != nil {
				t.Fatal(err)
			}
		}
		final, _, err := s.load(ctx, "par")
		if err != nil {
			t.Fatal(err)
		}
		return final.Tasks[0]
	}

	absentTask := runInfraFailures(t, "", 7)
	if absentTask.State != marshal.Escalated || absentTask.InfraRetries != 7 {
		t.Fatalf("absent infra limit should escalate on 7th failure: %+v", absentTask)
	}
	unlimitedTask := runInfraFailures(t, "0", 8)
	if unlimitedTask.State != marshal.Returned || unlimitedTask.InfraRetries != 8 {
		t.Fatalf("explicit zero infra limit should remain unlimited: %+v", unlimitedTask)
	}
	boundedTask := runInfraFailures(t, "2", 3)
	if boundedTask.State != marshal.Escalated || boundedTask.InfraRetries != 3 {
		t.Fatalf("explicit infra limit 2 should escalate on 3rd failure: %+v", boundedTask)
	}

	// 4. Separate rework-limit zero semantics stay intact (0 = no rework attempts, not unlimited).
	zeroRework := marshal.DefaultSettings()
	zeroRework.ReworkLimit = 0
	if next := marshal.NextAfterReturn(marshal.Task{Worker: "codex", ReturnsByAgent: map[string]int{"codex": 1}}, zeroRework.ReworkLimit); next != marshal.VerdictReassign {
		t.Fatalf("rework-limit 0 next verdict = %s, want %s", next, marshal.VerdictReassign)
	}

	// 5. Default verification timeout: absent=0, explicit 0=unlimited, positive bounds Runtime.Verify.
	repo := testgit.New(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close() })
	p := policy.Policy{ID: "verification", Version: 1, Default: policy.EffectDeny,
		Rules: []policy.Rule{{ID: "shell", Description: "allow verification shell", When: map[string]string{"action": "verify", "resource": "/bin/sh"}, Effect: policy.EffectAllow}}}
	if err := rt.store.PutPolicy(ctx, store.PolicyRecord{Policy: p,
		Binding: policy.PolicyBinding{Version: 1, Digest: mustPolicyDigest(t, p), Generation: 1}, State: policy.StateActive}); err != nil {
		t.Fatal(err)
	}
	rt.policyConfigured = true
	rt.runtimePolicy = RuntimePolicyConfig{PolicyID: p.ID, PolicyVersion: p.Version}
	if got := rt.effectiveVerificationTimeout(ctx); got != 0 {
		t.Fatalf("default Runtime verification timeout = %v, want 0", got)
	}
	rec, err := rt.Store().GetMarshalSettings(ctx, rt.ProjectID())
	if err != nil {
		t.Fatal(err)
	}
	bounded, err := settingscatalog.Apply(rec.Value, "verification-timeout-seconds", "1")
	if err != nil {
		t.Fatal(err)
	}
	rev1, _, err := rt.Store().CommitMarshalSettings(ctx, rt.ProjectID(), bounded, rec.Revision, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if got := rt.effectiveVerificationTimeout(ctx); got != time.Second {
		t.Fatalf("configured Runtime verification timeout = %v, want 1s", got)
	}
	start := time.Now()
	_, verifyErr := rt.Verify(ctx, VerifyRequest{Command: []string{"/bin/sh", "-c", "sleep 3"}})
	if verifyErr == nil {
		t.Fatal("expected bounded Runtime.Verify to time out")
	}
	if time.Since(start) >= 2800*time.Millisecond {
		t.Fatalf("bounded Runtime.Verify did not enforce 1s timeout (took %v)", time.Since(start))
	}
	unlimitedVerify, err := settingscatalog.Apply(bounded, "verification-timeout-seconds", "0")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := rt.Store().CommitMarshalSettings(ctx, rt.ProjectID(), unlimitedVerify, rev1, "owner"); err != nil {
		t.Fatal(err)
	}
	if got := rt.effectiveVerificationTimeout(ctx); got != 0 {
		t.Fatalf("explicit zero Runtime verification timeout = %v, want 0", got)
	}
	if res, err := rt.Verify(ctx, VerifyRequest{Command: []string{"/bin/sh", "-c", "printf ok"}}); err != nil || res.Stdout != "ok" {
		t.Fatalf("unlimited Runtime.Verify = %+v, %v", res, err)
	}
}
