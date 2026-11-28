package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/board/vm"
	"github.com/Zen1th53/marshal/internal/hostwatch"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
	"github.com/Zen1th53/marshal/internal/store"
)

// heatSampler is a portable sampler that reports only a package
// temperature, a second apart.
type heatSampler struct {
	mu  sync.Mutex
	at  time.Time
	pkg float64
}

func (s *heatSampler) Read(context.Context) (hostwatch.Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.at = s.at.Add(time.Second)
	return hostwatch.Sample{At: s.at, CPUPackageTempC: s.pkg, CPUPackageTempCKnown: true}, nil
}

func (s *heatSampler) set(pkg float64) { s.mu.Lock(); s.pkg = pkg; s.mu.Unlock() }

func heatSteps(h *hostWatcher, s *heatSampler, pkg float64, n int) {
	s.set(pkg)
	for range n {
		h.step(context.Background())
	}
}

// thermalStates are the effective thermal states the Host view and the Web
// Board report.
func thermalStates(w *Workspace) (hostGuardView, vm.System, string) {
	var b vm.Board
	w.collectV2System(&b)
	snap := w.hostWatch.Load().snapshot()
	top := strings.Join((boardVisual{theme: NewTheme(ThemeNoColor, true, true)}).hostTop(snap, 200), "\n")
	return snap.Guard, b.System, top
}

func TestThermalOverlayClearsWithoutPersistingCap(t *testing.T) {
	st, w := settingsWorkspace(t)
	w.mu.Lock()
	w.state.Marshal = &MarshalPanel{RunID: "R-1", State: marshal.Dispatching, run: marshal.Run{State: marshal.Dispatching}}
	w.mu.Unlock()
	before, err := st.GetMarshalSettings(t.Context(), w.projectID)
	if err != nil {
		t.Fatal(err)
	}
	newWatcher := func(policy hostwatch.Policy) (*hostWatcher, *heatSampler) {
		s := &heatSampler{at: time.Now(), pkg: 60}
		h := newHostWatcher(s)
		h.policy = validHostPolicy(policy)
		h.guard = &hostLaneGuard{sink: workspaceLaneCapSink{w}}
		w.hostWatch.Store(h)
		return h, s
	}
	unchanged := func() {
		t.Helper()
		after, err := st.GetMarshalSettings(t.Context(), w.projectID)
		if err != nil || after.Revision != before.Revision {
			t.Fatalf("settings were written: %+v %v", after, err)
		}
		if changes, _ := st.MarshalSettingsChanges(t.Context(), w.projectID); len(changes) != 0 {
			t.Fatalf("settings changes %+v", changes)
		}
	}
	configured, hasCap := effectiveGlobalCap(before.Value)

	h, s := newWatcher(hostwatch.DefaultPolicy())
	heatSteps(h, s, 96, 35)
	batch := w.permissions.queue.Take(false)
	if !hasCap {
		// This edition has no global lane cap: nothing is asked or applied.
		guard, sys, _ := thermalStates(w)
		if len(batch) != 0 || !guard.Active || sys.ThermalCap != 0 || !sys.ThermalGuard || w.thermalLaneCap(12) != 12 {
			t.Fatalf("batch %+v guard %+v system %+v", batch, guard, sys)
		}
		heatSteps(h, s, 60, 35)
		if guard, sys, _ := thermalStates(w); guard.Active || sys.ThermalGuard || sys.ThermalCap != 0 {
			t.Fatalf("cooled guard %+v system %+v", guard, sys)
		}
		unchanged()
		return
	}
	want := max(2, configured/2)
	if len(batch) != 1 || batch[0].Who != hostWatchActor {
		t.Fatalf("batch %+v", batch)
	}
	// Undecided: both surfaces report the configured cap.
	if guard, sys, _ := thermalStates(w); !guard.Pending || guard.Temporary != 0 || sys.ThermalCap != 0 || !sys.ThermalGuard || w.thermalLaneCap(configured) != configured {
		t.Fatalf("pending guard %+v system %+v", guard, sys)
	}
	if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	guard, sys, top := thermalStates(w)
	if guard.Temporary != want || sys.ThermalCap != want || w.thermalLaneCap(configured) != want || w.thermalLaneCap(1) != 1 {
		t.Fatalf("approved guard %+v system %+v", guard, sys)
	}
	if !strings.Contains(top, "temporary lane cap") {
		t.Fatalf("host view does not show the temporary cap:\n%s", top)
	}
	unchanged()

	// Cooling clears the overlay and asks for nothing.
	heatSteps(h, s, 60, 35)
	guard, sys, top = thermalStates(w)
	if guard.Active || guard.Temporary != 0 || sys.ThermalCap != 0 || sys.ThermalGuard || w.thermalLaneCap(configured) != configured || strings.Contains(top, "temporary lane cap") {
		t.Fatalf("cooled guard %+v system %+v", guard, sys)
	}
	if batch := w.permissions.queue.Take(false); len(batch) != 0 {
		t.Fatalf("cooling queued %+v", batch)
	}
	unchanged()

	// The watcher's exit clears an approved overlay too.
	heatSteps(h, s, 96, 35)
	batch = w.permissions.queue.Take(false)
	if len(batch) != 1 {
		t.Fatalf("second heat batch %+v", batch)
	}
	if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	if w.thermalLaneCap(configured) != want {
		t.Fatal("second approval not applied")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.run(ctx)
	if guard, sys, _ := thermalStates(w); guard.Temporary != 0 || sys.ThermalCap != 0 || w.thermalLaneCap(configured) != configured {
		t.Fatalf("exit left guard %+v system %+v", guard, sys)
	}
	// A late decision after the exit is refused.
	if err := w.takeHostCapRequest(batch[0]); err == nil {
		t.Fatal("request admitted after the watcher stopped")
	}
	unchanged()

	// Thermal off never raises the guard.
	off := hostwatch.DefaultPolicy()
	off.ThermalDisabled = true
	h, s = newWatcher(off)
	heatSteps(h, s, 99, 35)
	if guard, sys, _ := thermalStates(w); guard.Active || sys.ThermalGuard || len(w.permissions.queue.Take(false)) != 0 {
		t.Fatalf("thermal off guard %+v system %+v", guard, sys)
	}

	// Unusable thresholds fall back to the defaults.
	bad := hostwatch.DefaultPolicy()
	bad.PackageTempTriggerC, bad.PackageTempReleaseC, bad.PackageTempWindowSeconds = 80, 85, 0
	if p := validHostPolicy(bad); p.PackageTempTriggerC != 90 || p.PackageTempReleaseC != 85 || p.ThermalWindow() != 30*time.Second {
		t.Fatalf("invalid policy kept: %+v", p)
	}
	unchanged()
}

func TestSessionImportOffPreservesReadGrantAuthority(t *testing.T) {
	_, w := settingsWorkspace(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	rt := w.runtime
	root := rt.ProjectRoot()
	folder := rt.EarlierWorkFolder("claude")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"user","sessionId":"earlier","cwd":"` + root + `","message":{"role":"user","content":"earlier content"}}` + "\n"
	if err := os.WriteFile(filepath.Join(folder, "earlier.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	codex := rt.EarlierWorkFolder("codex")
	if err := os.MkdirAll(codex, 0o700); err != nil {
		t.Fatal(err)
	}
	// The owner grants the Claude history read through the ordinary path.
	grant := permission.Request{Kind: "read", Object: folder, Scope: "this project, until revoked, read-only", Who: "Marshal", Reason: "Load earlier claude work for this project", ContinuationProvider: "claude"}
	if err := w.decidePermission(t.Context(), grant, true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	if !rt.HasReadGrant(folder) {
		t.Fatal("grant not stored")
	}
	w.permissions.queue.Take(false)
	if _, err := w.marshalSettings(t.Context(), []string{"session-import", "off"}); err != nil {
		t.Fatal(err)
	}
	decisions := func() int {
		events, err := rt.Store().ListEvents(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range events {
			if e.Type == "PERMISSION_DECIDED" {
				n++
			}
		}
		return n
	}
	decided := decisions()
	// Off: the launch scan neither loads, asks nor decides anything.
	if sessions := w.scanEarlierSessions(t.Context(), root); len(sessions) != 0 {
		t.Fatalf("import off scanned %+v", sessions)
	}
	if !w.permissions.queue.Empty() {
		t.Fatalf("import off queued %+v", w.permissions.queue.Take(false))
	}
	w.permissions.mu.Lock()
	pending := len(w.permissions.continuations) + len(w.permissions.continuationDone)
	w.permissions.mu.Unlock()
	if pending != 0 {
		t.Fatal("import off registered a continuation")
	}
	// The stored grant still decides reads: it stays, nothing was decided
	// for Codex, and an explicit continuation under the grant still loads.
	if !rt.HasReadGrant(folder) || decisions() != decided {
		t.Fatalf("grant authority changed: claude %v decisions %d → %d", rt.HasReadGrant(folder), decided, decisions())
	}
	if !launchEarlierWorkThroughQueue && rt.HasReadGrant(codex) {
		t.Fatal("import off granted the Codex history")
	}
	text, err := w.continueEarlierWork(t.Context(), "claude", folder)
	if err != nil || !strings.Contains(text, "Granted source") {
		t.Fatalf("explicit continuation under the grant = %q, %v", text, err)
	}
	// The preference is a launch reader only and can be turned back on.
	if _, err := w.marshalSettings(t.Context(), []string{"session-import", "on"}); err != nil {
		t.Fatal(err)
	}
	p, err := settingscatalog.ReadScopedPreference(filepath.Join(root, ".marshal", "session-import"), "session-import")
	if err != nil || settingscatalog.Preference(p, "session-import") != "on" {
		t.Fatalf("preference %+v %v", p, err)
	}
}

// fakeThermalStore is a portable settings and audit store.
type fakeThermalStore struct {
	rec       store.MarshalRecord[marshal.Settings]
	changes   []store.SettingChange // newest first
	events    []model.Event
	commitErr error
	commits   []fakeThermalCommit
}

type fakeThermalCommit struct {
	settings marshal.Settings
	expected int64
	actor    string
}

func (f *fakeThermalStore) GetMarshalSettings(context.Context, string) (store.MarshalRecord[marshal.Settings], error) {
	return f.rec, nil
}
func (f *fakeThermalStore) MarshalSettingsChanges(context.Context, string) ([]store.SettingChange, error) {
	return f.changes, nil
}
func (f *fakeThermalStore) ListEvents(context.Context) ([]model.Event, error) { return f.events, nil }
func (f *fakeThermalStore) CommitMarshalSettings(_ context.Context, _ string, s marshal.Settings, expected int64, actor string) (int64, []store.SettingChange, error) {
	if f.commitErr != nil {
		return 0, nil, f.commitErr
	}
	if expected != f.rec.Revision {
		return 0, nil, errors.New("revision conflict")
	}
	f.commits = append(f.commits, fakeThermalCommit{s, expected, actor})
	f.rec.Value, f.rec.Revision = s, f.rec.Revision+1
	return f.rec.Revision, nil, nil
}

func TestLegacyThermalCapMigrationPreservesOwnerEdits(t *testing.T) {
	const project = "P-1"
	t0 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	decision := func(m int, value, reason string) model.Event {
		return model.Event{ID: "E-" + value, Type: "PERMISSION_DECIDED", ProjectID: project, Timestamp: at(m), Data: map[string]any{
			"kind": "marshal-command", "object": "/marshal settings global-cap " + value, "scope": "this project, next Marshal run; run R-1 keeps its approved cap",
			"requester": hostWatchActor, "reason": reason, "allow": true, "actor": "owner"}}
	}
	lowering := "CPU package 30 s avg 95.0 °C > 90; halve lanes 12 → 6. Host watch asks to restore when < 85 °C."
	capChange := func(rev int64, m int, old, new string) store.SettingChange {
		return store.SettingChange{Revision: rev, Key: "global-cap", Old: old, New: new, Actor: "owner", At: at(m)}
	}
	six, err := settingscatalog.Apply(marshal.DefaultSettings(), "global-cap", "6")
	if err != nil {
		// This edition has no global lane cap, so there is nothing to repair.
		f := &fakeThermalStore{rec: store.MarshalRecord[marshal.Settings]{Value: marshal.DefaultSettings(), Revision: 3}, changes: []store.SettingChange{capChange(3, 2, "0", "6")}, events: []model.Event{decision(1, "6", lowering)}}
		restored, notice, err := repairLegacyThermalCap(t.Context(), f, project)
		if restored || notice != "" || err != nil || len(f.commits) != 0 {
			t.Fatalf("governed repair %v %q %v %+v", restored, notice, err, f.commits)
		}
		return
	}
	eight, _ := settingscatalog.Apply(marshal.DefaultSettings(), "global-cap", "8")
	rec := func(s marshal.Settings, rev int64) store.MarshalRecord[marshal.Settings] {
		return store.MarshalRecord[marshal.Settings]{Value: s, Revision: rev}
	}

	t.Run("unapplied decision followed by an explicit matching cap is retained", func(t *testing.T) {
		f := &fakeThermalStore{rec: rec(six, 3), changes: []store.SettingChange{capChange(3, 2, "0", "6")}, events: []model.Event{decision(1, "6", lowering)}}
		restored, notice, err := repairLegacyThermalCap(t.Context(), f, project)
		if restored || !strings.Contains(notice, "global-cap 6") || err != nil || len(f.commits) != 0 || projectGlobalCap(f.rec.Value) != 6 || f.rec.Revision != 3 {
			t.Fatalf("repair %v %q %v %+v", restored, notice, err, f.rec)
		}
	})
	t.Run("later owner edit to another cap is kept silently", func(t *testing.T) {
		f := &fakeThermalStore{rec: rec(eight, 4), changes: []store.SettingChange{capChange(4, 3, "6", "8"), capChange(3, 2, "0", "6")}, events: []model.Event{decision(1, "6", lowering)}}
		if restored, notice, err := repairLegacyThermalCap(t.Context(), f, project); restored || notice != "" || err != nil || len(f.commits) != 0 {
			t.Fatalf("repair %v %q %v", restored, notice, err)
		}
	})
	t.Run("owner setting the same value again is kept silently", func(t *testing.T) {
		f := &fakeThermalStore{rec: rec(six, 5), changes: []store.SettingChange{capChange(5, 4, "8", "6"), capChange(4, 3, "6", "8"), capChange(3, 2, "0", "6")}, events: []model.Event{decision(1, "6", lowering)}}
		if restored, notice, err := repairLegacyThermalCap(t.Context(), f, project); restored || notice != "" || err != nil || len(f.commits) != 0 {
			t.Fatalf("repair %v %q %v", restored, notice, err)
		}
	})
	t.Run("an accepted restore is not a reduction", func(t *testing.T) {
		f := &fakeThermalStore{rec: rec(six, 3), changes: []store.SettingChange{capChange(3, 2, "3", "6")}, events: []model.Event{decision(1, "6", "CPU package back below 85 °C; restore the lane cap host watch lowered to 3.")}}
		if restored, notice, err := repairLegacyThermalCap(t.Context(), f, project); restored || notice != "" || err != nil || len(f.commits) != 0 {
			t.Fatalf("repair %v %q %v", restored, notice, err)
		}
	})
	t.Run("an owner value with no thermal decision is kept", func(t *testing.T) {
		f := &fakeThermalStore{rec: rec(six, 3), changes: []store.SettingChange{capChange(3, 2, "0", "6")}}
		if restored, notice, err := repairLegacyThermalCap(t.Context(), f, project); restored || notice != "" || err != nil || len(f.commits) != 0 {
			t.Fatalf("repair %v %q %v", restored, notice, err)
		}
	})
	t.Run("ambiguous value is kept with a one-time notice", func(t *testing.T) {
		later := store.SettingChange{Revision: 4, Key: "control", Old: "free", New: "strict", Actor: "owner", At: at(3)}
		f := &fakeThermalStore{rec: rec(six, 4), changes: []store.SettingChange{later, capChange(3, 2, "0", "6")}, events: []model.Event{decision(1, "6", lowering)}}
		restored, notice, err := repairLegacyThermalCap(t.Context(), f, project)
		if restored || err != nil || len(f.commits) != 0 || !strings.Contains(notice, "global-cap 6") {
			t.Fatalf("repair %v %q %v", restored, notice, err)
		}
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(thermalRepairNoticePath(root)), 0o700); err != nil {
			t.Fatal(err)
		}
		if thermalRepairOnce(root, 4, notice) == "" {
			t.Fatal("first notice not shown")
		}
		if thermalRepairOnce(root, 4, notice) != "" {
			t.Fatal("notice repeated")
		}
	})
	t.Run("a reduction saved before changes were audited gets a notice", func(t *testing.T) {
		f := &fakeThermalStore{rec: rec(six, 1), events: []model.Event{decision(1, "6", lowering)}}
		restored, notice, err := repairLegacyThermalCap(t.Context(), f, project)
		if restored || err != nil || len(f.commits) != 0 || !strings.Contains(notice, "global-cap 6") {
			t.Fatalf("repair %v %q %v", restored, notice, err)
		}
	})
	t.Run("the real store retains a matching explicit setting after an unapplied decision", func(t *testing.T) {
		st, w := settingsWorkspace(t)
		legacy := permission.Request{Kind: "marshal-command", Object: "/marshal settings global-cap 6", Scope: "this project, next Marshal run; run R-1 keeps its approved cap", Who: hostWatchActor, Reason: lowering}
		// Simulate a crash after the decision was recorded, before its
		// setting was applied. The later command is an independent edit.
		_ = w.decidePermission(t.Context(), legacy, true, "operator popup")
		if _, err := w.marshalSettings(t.Context(), []string{"global-cap", "6"}); err != nil {
			t.Fatal(err)
		}
		if msg := w.repairThermalCap(t.Context()); !strings.Contains(msg, "global-cap 6 may be") {
			t.Fatalf("repair said %q", msg)
		}
		after, err := st.GetMarshalSettings(t.Context(), w.projectID)
		if err != nil || projectGlobalCap(after.Value) != 6 {
			t.Fatalf("settings %+v %v", after, err)
		}
		changes, _ := st.MarshalSettingsChanges(t.Context(), w.projectID)
		if len(changes) == 0 || changes[0].Actor != "owner" || changes[0].New != "6" {
			t.Fatalf("changes %+v", changes)
		}
		// A second launch at this revision does not repeat the notice.
		if msg := w.repairThermalCap(t.Context()); msg != "" {
			t.Fatalf("second repair said %q", msg)
		}
	})
}
