package store

import (
	"testing"
	"time"
)

func webTestIntent() WebIntent {
	return WebIntent{ProjectID: "p", Owner: "owner", Session: "session", RequestID: "00112233445566778899aabbccddeeff", Digest: "exact-change", Op: "settings/set", Key: "rework-limit", Value: "5", Revision: 0}
}
func TestWebSettingsEffectAndReceiptCommitTogether(t *testing.T) {
	st := openTestStore(t)
	marshalTestProject(t, st)
	i := webTestIntent()
	if _, err := st.AdmitWebSetting(t.Context(), i); err != nil {
		t.Fatal(err)
	}
	_, err := st.db.Exec(`CREATE TRIGGER reject_web_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ApplyWebSetting(t.Context(), i); err == nil {
		t.Fatal("audit failure committed effect")
	}
	rec, _ := st.GetMarshalSettings(t.Context(), "p")
	r, _ := st.WebReceipt(t.Context(), "p", "owner", i.RequestID, i.Digest)
	if rec.Revision != 0 || r.Status != "queued" {
		t.Fatal(rec, r)
	}
	if _, err = st.db.Exec(`DROP TRIGGER reject_web_audit`); err != nil {
		t.Fatal(err)
	}
	r, err = st.ApplyWebSetting(t.Context(), i)
	if err != nil || r.Status != "applied" || r.Revision != 1 {
		t.Fatal(r, err)
	}
	if _, err = st.ApplyWebSetting(t.Context(), i); err == nil {
		t.Fatal("receipt replay reapplied effect")
	}
	rec, _ = st.GetMarshalSettings(t.Context(), "p")
	if rec.Revision != 1 || rec.Value.ReworkLimit != 5 {
		t.Fatal(rec)
	}
}
func TestWebSettingsReceiptTombstonePreventsReplay(t *testing.T) {
	st := openTestStore(t)
	marshalTestProject(t, st)
	i := webTestIntent()
	if _, err := st.AdmitWebSetting(t.Context(), i); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyWebSetting(t.Context(), i); err != nil {
		t.Fatal(err)
	}
	_, err := st.db.Exec(`UPDATE web_settings_actions SET created=?`, time.Now().Add(-8*24*time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.WebReceipt(t.Context(), "p", "owner", i.RequestID, i.Digest)
	if err != nil || r.Code != "expired" {
		t.Fatal(r, err)
	}
	var raw string
	if err = st.db.QueryRow(`SELECT intent FROM web_settings_actions`).Scan(&raw); err != nil || raw != "{}" {
		t.Fatal(raw, err)
	}
	if _, err = st.AdmitWebSetting(t.Context(), i); err == nil {
		t.Fatal("tombstone authorized a retry")
	}
}
func TestWebUltraRevisionIncludesTerminalChanges(t *testing.T) {
	st := openTestStore(t)
	marshalTestProject(t, st)
	i := webTestIntent()
	i.Op = "settings/ultra.set"
	i.Key = "ultra-execution"
	i.Value = "false"
	if _, err := st.AdmitWebSetting(t.Context(), i); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUltraExecutionPreference(t.Context(), "p", true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyWebSetting(t.Context(), i); err == nil {
		t.Fatal("old browser revision overwrote terminal preference")
	}
	value, _ := st.GetUltraExecutionPreference(t.Context(), "p")
	if value == nil || !*value {
		t.Fatal(value)
	}
}
