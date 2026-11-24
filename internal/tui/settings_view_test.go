package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

func settingsWorkspace(t *testing.T) (*store.Store, *Workspace) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// UserConfigDir uses this location on systems that do not use XDG paths.
	// Pass preference paths directly in tests that exercise file failures.
	repo := testgit.New(t)
	if _, err := app.Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close() })
	w := NewWorkspace(rt.Store(), rt.ProjectID(), "settings-session")
	w.workDir = repo.Path()
	w.AttachRuntime(rt, projectid.ID(rt.ProjectIdentity()))
	t.Cleanup(w.Close)
	return rt.Store(), w
}

func TestSettingsViewShowsLocksAndActor(t *testing.T) {
	rows := append(settingscatalog.Rows(marshal.DefaultSettings(), 0), settingscatalog.PolicyRows()...)
	text := formatSettingsView(rows, []store.SettingChange{{Revision: 1, Key: "control", Old: "free", New: "strict", Actor: "Marshal", At: time.Now()}})
	for _, want := range []string{"locked:", "Marshal", "next run", "minor currency units", "Unknown usage"} {
		if !strings.Contains(text, want) {
			t.Fatal(want, text)
		}
	}
}

func TestSettingsMissingValueDoesNotWrite(t *testing.T) {
	st, w := settingsWorkspace(t)
	before, _ := st.GetMarshalSettings(t.Context(), w.projectID)
	for _, key := range []string{"control", "rework-limit", "global-cap", "web-auto-open", "notifications", "memory-channel", "session-import"} {
		out, err := w.marshalSettings(t.Context(), []string{key})
		if err == nil || !strings.Contains(err.Error(), "usage: /marshal settings "+key) {
			t.Fatal(out, err)
		}
	}
	after, _ := st.GetMarshalSettings(t.Context(), w.projectID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("usage wrote settings")
	}
	changes, _ := st.MarshalSettingsChanges(t.Context(), w.projectID)
	if len(changes) != 0 {
		t.Fatal(changes)
	}
}

func TestCatalogueEveryKeyResolvesStoreOverrideAndFailedWrite(t *testing.T) {
	st, w := settingsWorkspace(t)
	for _, d := range settingscatalog.Catalogue() {
		before, err := st.GetMarshalSettings(t.Context(), w.projectID)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := settingscatalog.Get(before.Value, d.Key)
		if d.LockReason != "" {
			if _, err = w.marshalSettings(t.Context(), []string{d.Key, raw}); err == nil {
				t.Fatal("policy edited", d.Key)
			}
			continue
		}
		value := raw
		if d.Kind == "integer" {
			value = "9"
		}
		if d.Key == "control" {
			value = "strict"
		}
		if d.Key == "execution-rights" {
			value = "small-tasks"
		}
		if d.Key == "acceptance-mode" {
			value = "user"
		}
		if d.Key == "failover-order" {
			value = "claude,codex"
		}
		if _, err = w.marshalSettings(t.Context(), []string{d.Key, value}); err != nil {
			t.Fatal(d.Key, err)
		}
		after, _ := st.GetMarshalSettings(t.Context(), w.projectID)
		got, _ := settingscatalog.Get(after.Value, d.Key)
		if got != value {
			t.Fatal(d.Key, got, value)
		}
		invalid := "invalid"
		if d.Key == "failover-order" {
			invalid = "unknown"
		}
		if _, err = w.marshalSettings(t.Context(), []string{d.Key, invalid}); err == nil {
			t.Fatal("invalid accepted", d.Key)
		}
		failed, _ := st.GetMarshalSettings(t.Context(), w.projectID)
		if !reflect.DeepEqual(after, failed) {
			t.Fatal("failed write changed", d.Key)
		}
	}
}

func TestSettingsScopePrecedenceAndFailedWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	p, err := settingscatalog.ApplyPreference(path, "web-auto-open", "on", "owner", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range settingscatalog.UserRows(p, func(string) string { return "0" }) {
		if row.Key == "web-auto-open" && (row.Value != "on" || row.Effective != "off" || row.Source != "launch environment") {
			t.Fatal(row)
		}
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err = os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = settingscatalog.ApplyPreference(filepath.Join(blocker, "preferences.json"), "web-auto-open", "off", "owner", 0); err == nil {
		t.Fatal("failed writer succeeded")
	}
	after, err := settingscatalog.ReadPreferences(path)
	if err != nil || !reflect.DeepEqual(after, p) {
		t.Fatal(after, err)
	}
}

func TestAllThreeDefaultsConsumersAgree(t *testing.T) {
	s := marshal.DefaultSettings()
	_, cap := marshal.EffectiveWorkerLimits(s)
	board := boardmodel.Project(boardmodel.Snapshot{RunID: "run", Run: marshal.Run{Settings: s}}, nil)
	if board.Run.LaneCap != cap {
		t.Fatal(board.Run.LaneCap, cap)
	}
	if v, ok := effectiveGlobalCap(s); ok && v != cap {
		t.Fatal(v, cap)
	}
	limits, global := app.WorkerConcurrency(s)
	if len(limits) > 0 {
		if global != cap {
			t.Fatal(global, cap)
		}
		for key, value := range limits {
			if !strings.Contains(formatExtremeSettings(s), fmt.Sprintf("%s-limit %d", key, value)) {
				t.Fatal(key, value)
			}
		}
	}
	ready := buildV2Ready(v2MarshalIn{Now: time.Now()})
	if len(ready.Defaults) == 0 {
		t.Fatal("initial readiness defaults missing")
	}
}

func TestMarshalSettingAuditReachesHomeAndChat(t *testing.T) {
	st, w := settingsWorkspace(t)
	var delivered []string
	w.draftTell = func(text string) { delivered = append(delivered, text) }
	if err := w.applyMarshalProposal(t.Context(), permission.Request{Kind: "marshal-command", Who: "Marshal", Object: "/marshal settings control strict"}); err != nil {
		t.Fatal(err)
	}
	changes, err := st.MarshalSettingsChanges(t.Context(), w.projectID)
	if err != nil || len(changes) != 1 || changes[0].Actor != "Marshal" {
		t.Fatal(changes, err)
	}
	if len(delivered) != 1 || !strings.Contains(delivered[0], changes[0].ID) {
		t.Fatal(delivered)
	}
	inbox, err := openMarshalInbox(w.providerRoot())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(inbox.path)
	if err != nil || !strings.Contains(string(data), changes[0].ID) {
		t.Fatal(string(data), err)
	}
	b := w.boardV2()
	found := false
	for _, e := range b.Activity {
		if strings.Contains(e.Text, changes[0].ID) {
			found = true
		}
	}
	if !found {
		t.Fatal("committed change absent from Activity")
	}
}

func TestSettingsWithoutProjectGivesRecoveryHint(t *testing.T) {
	w := NewWorkspace(nil, "", "settings-session")
	t.Cleanup(w.Close)
	for _, args := range [][]string{nil, {"control", "free"}} {
		if _, err := w.marshalSettings(t.Context(), args); err == nil || !strings.Contains(err.Error(), "reopen") {
			t.Fatalf("settings %v: %v", args, err)
		}
	}
	if _, err := w.marshalSettings(t.Context(), []string{"control"}); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("missing value: %v", err)
	}
}

func TestSettingsReadbackShowsCanonicalValueAndUnits(t *testing.T) {
	_, w := settingsWorkspace(t)
	for _, edit := range []struct{ key, value, want string }{
		{"execution-rights", "none", "execution-rights read-only · project · project · next run"},
		{"plan-money", "12", "plan-money 12 minor currency units · project · project · next run"},
	} {
		if _, err := w.marshalSettings(t.Context(), []string{edit.key, edit.value}); err != nil {
			t.Fatal(err)
		}
		out, err := w.marshalSettings(t.Context(), nil)
		if err != nil || !strings.Contains("\n"+out+"\n", "\n"+edit.want+"\n") {
			t.Fatalf("readback: %s %v", out, err)
		}
	}
}
