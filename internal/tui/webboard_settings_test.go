package tui

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/webboard"
)

func admitTestSetting(t *testing.T, w *Workspace, key, value string, revision int64) webboard.Action {
	t.Helper()
	i := store.WebIntent{ProjectEpoch: webboard.SettingsEpoch(w.webSettingsState().Fence), ProjectID: "project", Owner: "owner", Session: "session", RequestID: "00112233445566778899aabbccddeeff", Digest: key + value, Op: "settings/set", Key: key, Value: value, Revision: revision}
	if _, err := w.store.AdmitWebSetting(t.Context(), i); err != nil {
		t.Fatal(err)
	}
	return webboard.Action{Kind: "cmd", Command: &i, Commit: func(f func() webboard.Result) webboard.Result { return f() }}
}
func TestWebSettingRejectsStaleRevisionAndLockedKey(t *testing.T) {
	st, _ := preferenceStore(t)
	w := NewWorkspace(st, "project", "session")
	defer w.Close()
	a := admitTestSetting(t, w, "rework-limit", "5", 0)
	rec, _ := st.GetMarshalSettings(t.Context(), "project")
	rec.Value.ReworkLimit = 3
	if _, err := st.SetMarshalSettings(t.Context(), "project", rec.Value, rec.Revision); err != nil {
		t.Fatal(err)
	}
	if r := w.webSetting(a); r.Status != "refused" {
		t.Fatal(r)
	}
	rec, _ = st.GetMarshalSettings(t.Context(), "project")
	if rec.Value.ReworkLimit != 3 {
		t.Fatal(rec)
	}
	// Locked edition policies and unknown keys never reach a writer capability.
	state := w.webSettingsState()
	for _, cap := range state.Capabilities {
		if cap.Op != "settings/set" {
			continue
		}
		d, err := settingscatalog.Find(cap.Target.ID)
		if err != nil || d.LockReason != "" {
			t.Fatal(cap)
		}
	}
	a.Command.RequestID = "10112233445566778899aabbccddeeff"
	a.Command.Key = "cross-review"
	a.Command.Revision = rec.Revision
	if _, err := st.AdmitWebSetting(t.Context(), *a.Command); err != nil {
		t.Fatal(err)
	}
	if r := w.webSetting(a); r.Status != "refused" {
		t.Fatal(r)
	}
}
func TestWebTargetChangeBeforeDequeueRefuses(t *testing.T) {
	st, _ := preferenceStore(t)
	w := NewWorkspace(st, "project", "session")
	defer w.Close()
	a := admitTestSetting(t, w, "rework-limit", "5", 0)
	w.mu.Lock()
	w.projectID = "other-project"
	w.mu.Unlock()
	if r := w.webAct(a).res; r.Status != "refused" {
		t.Fatal(r)
	}
	rec, _ := st.GetMarshalSettings(t.Context(), "project")
	if rec.Revision != 0 {
		t.Fatal(rec)
	}
}
func TestOldSessionQueueCannotMutateSwitchedProject(t *testing.T) {
	st, _ := preferenceStore(t)
	w := NewWorkspace(st, "project", "session")
	defer w.Close()
	a := admitTestSetting(t, w, "rework-limit", "5", 0)
	if err := st.FenceWebSettings(t.Context(), "project", "new-session"); err != nil {
		t.Fatal(err)
	}
	if r := w.webSetting(a); r.Status != "refused" {
		t.Fatal(r)
	}
	r, err := st.WebReceipt(t.Context(), "project", "owner", a.Command.RequestID, "")
	if err != nil || r.Code != "session_changed" {
		t.Fatal(r, err)
	}
	rec, _ := st.GetMarshalSettings(context.Background(), "project")
	if rec.Revision != 0 {
		t.Fatal(rec)
	}
}
func TestSettingsKeepsLegacyAndUnwiredOpsOutOfDurableQueue(t *testing.T) {
	st, _ := preferenceStore(t)
	w := NewWorkspace(st, "project", "session")
	defer w.Close()
	for _, cap := range w.webSettingsState().Capabilities {
		if cap.Op != "settings/set" && cap.Op != "settings/ultra.set" {
			t.Fatal(cap)
		}
		if cap.Target.Kind != "setting" {
			t.Fatal(cap)
		}
	}
	if r := w.webSetting(webboard.Action{Kind: "cmd"}); r.Status != "refused" {
		t.Fatal(r)
	}
	if _, err := st.WebReceipt(t.Context(), "project", "owner", "missing", ""); err == nil {
		t.Fatal("unwired operation created a receipt")
	}
}

func TestWebRequestReceiptPreservesUnknownOutcome(t *testing.T) {
	i := store.WebIntent{RequestID: "00112233445566778899aabbccddeeff", Revision: 0}
	binding := webRequestBinding{}
	o := cloud.RequestObservation{CommandDigest: webRequestDigest(binding), LocalAttemptID: "local-request", Generation: 1, State: "sending"}
	r := webRequestReceipt(i, binding, o, true, nil)
	if r.Status != "interrupted" || r.Code != "outcome_unknown" {
		t.Fatal(r)
	}
	o.State = "pending"
	o.Generation = 2
	r = webRequestReceipt(i, binding, o, true, nil)
	if r.Status != "applied" || r.Code != "pending" || r.Revision != 2 {
		t.Fatal(r)
	}
	o.CommandDigest = "other-request"
	if r = webRequestReceipt(i, binding, o, true, nil); r.Status != "interrupted" {
		t.Fatal(r)
	}
}

func TestWebSettingRefusesChangedProjectEpoch(t *testing.T) {
	st, _ := preferenceStore(t)
	w := NewWorkspace(st, "project", "session")
	defer w.Close()
	a := admitTestSetting(t, w, "rework-limit", "5", 0)
	w.mu.Lock()
	w.webAuthority++
	w.mu.Unlock()
	if r := w.webSetting(a); r.Status != "refused" {
		t.Fatal(r)
	}
	rec, _ := st.GetMarshalSettings(t.Context(), "project")
	if rec.Revision != 0 {
		t.Fatal(rec)
	}
}
