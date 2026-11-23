package settingscatalog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestEditionDefaultsAndApplyBoundaries(t *testing.T) {
	s := marshal.DefaultSettings()
	for _, d := range Catalogue() {
		raw, err := Get(s, d.Key)
		if err != nil {
			t.Fatal(err)
		}
		if d.LockReason != "" {
			if _, err = Apply(s, d.Key, raw); err == nil {
				t.Fatalf("unlocked %s", d.Key)
			}
			continue
		}
		next, err := Apply(s, d.Key, raw)
		if err != nil || !reflect.DeepEqual(next, s) {
			t.Fatalf("round trip %s: %+v %v", d.Key, next, err)
		}
	}
	if next, err := Apply(s, "rework-limit", "-1"); err == nil || !reflect.DeepEqual(next, s) {
		t.Fatal("invalid edit changed settings")
	}
	if _, err := Apply(s, "ultra-concurrency", "0"); err == nil {
		t.Fatal("zero workers accepted")
	}
	next, err := Apply(s, "execution-rights", "none")
	if err != nil || next.ExecutionRights != marshal.RightsReadOnly {
		t.Fatal(next, err)
	}
	s.ExecutionRights = marshal.RightsNone
	if Effective(s, "execution-rights") != "read-only" {
		t.Fatal("legacy rights were not mapped")
	}
}

func TestPreferenceAtomicRecordAndOptOuts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	p, err := ApplyPreference(path, "web-auto-open", "off", "owner", 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Revision != 1 || len(p.Changes) != 1 || p.Changes[0].ID == "" {
		t.Fatal(p)
	}
	if _, err = ApplyPreference(path, "thermal-release", "100", "owner", 1); err == nil {
		t.Fatal("invalid hysteresis accepted")
	}
	after, err := ReadPreferences(path)
	if err != nil || !reflect.DeepEqual(p, after) {
		t.Fatal("failed write changed preference", after, err)
	}
	if _, err = ApplyPreference(path, "web-auto-open", "on", "owner", 0); err == nil {
		t.Fatal("stale preference accepted")
	}
	for _, r := range UserRows(p, func(k string) string {
		if k == "MARSHAL_NO_UPDATE_CHECK" {
			return "1"
		}
		if k == "MARSHAL_WEB_OPEN" {
			return "0"
		}
		return ""
	}) {
		if r.Key == "update-check" || r.Key == "web-auto-open" {
			if r.Effective != "off" {
				t.Fatal(r)
			}
		}
	}
}

func TestScopedPreferenceKeepsLegacyValueAndReceiptTogether(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify")
	if err := os.WriteFile(path, []byte("off\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := ReadScopedPreference(path, "notifications")
	if err != nil || Preference(p, "notifications") != "off" {
		t.Fatal(p, err)
	}
	p, err = WriteScopedPreference(path, "notifications", "on", "owner")
	if err != nil || len(p.Changes) != 1 || p.Changes[0].Old != "off" {
		t.Fatal(p, err)
	}
	saved, err := ReadScopedPreference(path, "notifications")
	if err != nil || !reflect.DeepEqual(p, saved) {
		t.Fatal(saved, err)
	}
	if _, err = WriteScopedPreference(path, "notifications", "invalid", "owner"); err == nil {
		t.Fatal("invalid preference committed")
	}
	saved, err = ReadScopedPreference(path, "notifications")
	if err != nil || !reflect.DeepEqual(p, saved) {
		t.Fatal("failure changed prior value", saved, err)
	}
}

func TestProviderAndRuntimeLimitRowsExposeEffectiveValues(t *testing.T) {
	s := marshal.DefaultSettings()
	next, err := Apply(s, "codex-enabled", "off")
	if err != nil {
		t.Fatal(err)
	}
	if Effective(next, "codex-enabled") != "off" || Effective(next, "claude-enabled") != "on" {
		t.Fatalf("provider effective values wrong: codex=%s claude=%s", Effective(next, "codex-enabled"), Effective(next, "claude-enabled"))
	}
	if _, err := Apply(s, "codex-enabled", "maybe"); err == nil {
		t.Fatal("invalid provider enablement accepted")
	}
	rows := map[string]Row{}
	for _, r := range Rows(next, 1) {
		rows[r.Key] = r
	}
	for _, key := range []string{"codex-enabled", "claude-enabled", "agy-enabled", "opencode-enabled", "worker-stall-minutes", "infra-retry-limit", "verification-timeout-seconds"} {
		if _, ok := rows[key]; !ok {
			t.Fatalf("missing catalogue row %s", key)
		}
	}
	if rows["codex-enabled"].Applies != "next admission" {
		t.Fatalf("codex-enabled applies = %q, want next admission", rows["codex-enabled"].Applies)
	}
}
