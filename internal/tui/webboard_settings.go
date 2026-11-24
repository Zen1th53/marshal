package tui

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Zen1th53/marshal/internal/settingscatalog"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// webSettingsState advertises only project settings with a ready local writer.
func (w *Workspace) webSettingsState() webboard.SettingsState {
	w.mu.RLock()
	project := w.ultraProjectLocked()
	state := webboard.SettingsState{ProjectID: project, Fence: w.webSettingsFenceLocked(), Capabilities: []webboard.SettingCapability{}}
	if w.store == nil {
		w.mu.RUnlock()
		return state
	}
	rec, err := w.store.GetMarshalSettings(context.Background(), project)
	if err != nil {
		w.mu.RUnlock()
		return state
	}
	for _, d := range settingscatalog.Catalogue() {
		if d.LockReason != "" {
			continue
		}
		state.Capabilities = append(state.Capabilities, webboard.SettingCapability{Op: "settings/set", Target: webboard.Target{Kind: "setting", ID: d.Key}, Revision: rec.Revision, Values: d.Values})
	}
	revision, err := w.store.UltraPreferenceRevision(context.Background(), project)
	if err == nil {
		state.Capabilities = append(state.Capabilities, webboard.SettingCapability{Op: "settings/ultra.set", Target: webboard.Target{Kind: "setting", ID: "ultra-execution"}, Revision: revision, Values: []string{"off", "on"}})
	}
	if cap := w.webRequestCapability(context.Background()); cap != nil {
		state.Capabilities = append(state.Capabilities, *cap)
	}
	w.mu.RUnlock()
	state.Capabilities = append(state.Capabilities, w.webWorkCapabilities(context.Background())...)
	return state
}

// webSetting executes on the input loop, with a final authority check around the
// short local transaction. Neither the browser nor the queue supplies an actor.
func (w *Workspace) webSetting(a webboard.Action) webboard.Result {
	if a.Command == nil || a.Commit == nil {
		return refused("Settings authority is unavailable.")
	}
	if a.Command.Op == "settings/ultra.request" {
		return w.webSettingRequest(a)
	}
	i := *a.Command
	res := a.Commit(func() webboard.Result {
		w.ultraAdmission.Lock()
		defer w.ultraAdmission.Unlock()
		w.mu.Lock()
		if w.store == nil || w.ultraProjectLocked() != i.ProjectID || webboard.SettingsEpoch(w.webSettingsFenceLocked()) != i.ProjectEpoch {
			w.mu.Unlock()
			return refused("The project changed before the setting was applied.")
		}
		st := w.store
		if i.Op == "settings/ultra.set" && i.Value == "true" && !w.ultra.Entitled() {
			w.mu.Unlock()
			_, _ = st.RefuseWebSetting(context.Background(), i, "entitlement_required")
			return refused("ULTRA execution requires current entitlement.")
		}
		receipt, err := st.ApplyWebSetting(context.Background(), i)
		if err != nil {
			w.mu.Unlock()
			_, _ = st.RefuseWebSetting(context.Background(), i, "stale_or_locked")
			return refused("The setting changed or is locked; refresh the page.")
		}
		if i.Op == "settings/ultra.set" {
			enabled, _ := strconv.ParseBool(i.Value)
			w.ultraExecution = enabled
			w.state.UltraExecution = enabled
			w.ultraStopAsked = false
		}
		w.mu.Unlock()
		w.requestRepaint()
		return webboard.Result{Status: "done", Text: receipt.Result}
	})
	if res.Status == "done" {
		w.mu.RLock()
		st := w.store
		project := w.ultraProjectLocked()
		w.mu.RUnlock()
		if st != nil && project == i.ProjectID {
			if changes, err := st.MarshalSettingsChanges(context.Background(), project); err == nil {
				for _, c := range changes {
					w.publishSettingChange(c)
				}
			}
		}
	}
	return res
}

func (w *Workspace) webSettingsFenceLocked() string {
	return fmt.Sprintf("%s:%d", w.sessionID, w.webAuthority)
}

func (w *Workspace) lockWebSettingsTarget() func() {
	state := w.webBoard()
	state.mu.Lock()
	srv := state.server
	state.mu.Unlock()
	if srv == nil {
		return func() {}
	}
	return srv.LockSettingsTarget()
}
