package tui

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/settingscatalog"
	"github.com/Zen1th53/marshal/internal/store"
)

type settingsActorKey struct{}

func formatSettingsView(rows []settingscatalog.Row, changes []store.SettingChange) string {
	var out strings.Builder
	about := false
	for _, r := range rows {
		if r.Scope == "build" && !about {
			out.WriteString("About this build:\n")
			about = true
		}
		fmt.Fprintf(&out, "%s %s", r.Key, r.Effective)
		if r.Units != "" {
			fmt.Fprintf(&out, " %s", r.Units)
		}
		fmt.Fprintf(&out, " · %s · %s", r.Scope, r.Source)
		if r.Value != r.Effective {
			fmt.Fprintf(&out, " · configured %q", r.Value)
		}
		if r.LockedReason != "" {
			fmt.Fprintf(&out, " · locked: %s", r.LockedReason)
		}
		fmt.Fprintf(&out, " · %s\n", r.Applies)
	}
	out.WriteString("Run ceilings: zero is unlimited. Unknown usage is not zero and does not bypass quota.\nGoal ceilings belong to the approved goal revision.\n")
	if len(changes) > 0 {
		out.WriteString("Recent changes:\n")
	}
	for _, c := range changes {
		fmt.Fprintf(&out, "r%d %s: %s → %s · %s · %s\n", c.Revision, c.Key, c.Old, c.New, c.Actor, c.At.Format("2006-01-02 15:04:05Z07:00"))
	}
	return strings.TrimSpace(out.String())
}

func settingChangeNotice(c store.SettingChange) string {
	applies := c.Applies
	if applies == "" {
		applies = "next run"
	}
	return fmt.Sprintf("Settings r%d: %s changed %s → %s by %s. Applies to %s. [%s]", c.Revision, c.Key, c.Old, c.New, c.Actor, applies, c.ID)
}

func (w *Workspace) publishSettingChange(c store.SettingChange) {
	w.mu.Lock()
	if w.settingNotices == nil {
		w.settingNotices = map[string]bool{}
	}
	if w.settingNotices[c.ID] {
		w.mu.Unlock()
		return
	}
	w.settingNotices[c.ID] = true
	w.mu.Unlock()
	text := settingChangeNotice(c)
	w.RecordActivity(text)
	if w.runtime != nil {
		if inbox, err := openMarshalInbox(w.runtime.ProjectRoot()); err == nil {
			if err = inbox.write(text + "\n"); err != nil {
				w.RecordActivity("Settings notice delivery failed: " + err.Error())
			}
		}
	}
	w.tellMarshal(text)
}

func readSettingsView(ctx context.Context, st *store.Store, projectID, root string) (settingscatalog.Snapshot, error) {
	rec, err := st.GetMarshalSettings(ctx, projectID)
	if err != nil {
		return settingscatalog.Snapshot{}, err
	}
	changes, err := st.MarshalSettingsChanges(ctx, projectID)
	if err != nil {
		return settingscatalog.Snapshot{}, err
	}
	rows := settingscatalog.Rows(rec.Value, rec.Revision)
	telemetry, _ := app.TelemetryChoice(root)
	telemetryValue := fmt.Sprint(telemetry)
	rows = append(rows, settingscatalog.Row{Key: "telemetry", Value: telemetryValue, Effective: telemetryValue, Next: telemetryValue, Scope: "machine", Source: "cloud telemetry state and environment", Applies: "this launch", Help: "marshal telemetry enable|disable; cloud endpoint off keeps telemetry off."})

	rows = append(rows, app.SettingsPolicies()...)
	preference, err := st.GetUltraExecutionPreference(ctx, projectID)
	if err != nil {
		return settingscatalog.Snapshot{}, err
	}
	raw := "default"
	if preference != nil {
		raw = fmt.Sprint(*preference)
	}
	rows = append(rows, settingscatalog.Row{Key: "ultra-execution", Value: raw, Next: raw, Scope: "project execution preference", Source: "saved project preference", Applies: "next admission", Help: "Entitlement is checked independently; running workers drain under their admitted policy."})
	path, err := settingscatalog.PreferencesPath()
	if err != nil {
		return settingscatalog.Snapshot{}, err
	}
	prefs, err := settingscatalog.ReadPreferences(path)
	if err != nil {
		return settingscatalog.Snapshot{}, err
	}
	rows = append(rows, settingscatalog.UserRows(prefs, os.Getenv)...)
	for _, c := range prefs.Changes {
		changes = append(changes, store.SettingChange{ID: c.ID, Revision: c.Revision, Key: c.Key, Old: c.Old, New: c.New, Actor: c.Actor, At: c.At})
	}
	for _, key := range []string{"notifications", "memory-channel", "session-import"} {
		path, _ := projectFileSetting(root, key)
		p, err := settingscatalog.ReadScopedPreference(path, key)
		if err != nil {
			return settingscatalog.Snapshot{}, err
		}
		value := settingscatalog.Preference(p, key)
		help := ""
		if key == "session-import" {
			help = "Launch preference only; provider history still requires its read grant."
		}
		rows = append(rows, settingscatalog.Row{Key: key, Value: value, Effective: value, Next: value, Scope: "project file", Source: "project file or default", Applies: "next chat or launch", Help: help})
		for _, c := range p.Changes {
			changes = append(changes, store.SettingChange{ID: c.ID, Revision: c.Revision, Key: key, Old: c.Old, New: c.New, Actor: c.Actor, At: c.At})
		}
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].At.After(changes[j].At) })
	if len(changes) > 100 {
		changes = changes[:100]
	}
	return settingscatalog.Snapshot{Revision: rec.Revision, Preset: settingscatalog.PresetFor(rec.Value), Rows: rows, Changes: changes}, nil
}

func userSettingUsage(key string) (string, bool) {
	for _, r := range settingscatalog.UserRows(settingscatalog.Preferences{}, os.Getenv) {
		if r.Key == key {
			values := "on|off"
			if key == "display-theme" {
				values = "default|monochrome|high-contrast|no-color"
			}
			if r.Units != "" {
				values = "number in " + r.Units
			}
			return fmt.Sprintf("usage: /marshal settings %s <%s> · user · next launch", key, values), true
		}
	}
	return "", false
}

func (w *Workspace) applyUserSetting(key, value, actor string) (string, error) {
	path, err := settingscatalog.PreferencesPath()
	if err != nil {
		return "", err
	}
	p, err := settingscatalog.ReadPreferences(path)
	if err != nil {
		return "", err
	}
	next, err := settingscatalog.ApplyPreference(path, key, value, actor, p.Revision)
	if err != nil {
		return "", err
	}
	c := next.Changes[len(next.Changes)-1]
	w.publishSettingChange(store.SettingChange{ID: c.ID, Revision: c.Revision, Key: c.Key, Old: c.Old, New: c.New, Actor: c.Actor, At: c.At, Scope: "user", Applies: "next launch"})
	return fmt.Sprintf("%s set to %s for the next launch.", key, value), nil
}

func projectFileSetting(root, key string) (string, bool) {
	switch key {
	case "notifications":
		return attentionPath(root), true
	case "memory-channel":
		return injectChannelPath(root), true
	case "session-import":
		return filepath.Join(root, ".marshal", "session-import"), true
	}
	return "", false
}

func (w *Workspace) applyFileSetting(key, value, actor string) (string, error) {
	path, _ := projectFileSetting(w.providerRoot(), key)
	if err := statepath.CheckPath(w.providerRoot(), path); err != nil {
		return "", err
	}
	next, err := settingscatalog.WriteScopedPreference(path, key, value, actor)
	if err != nil {
		return "", err
	}
	c := next.Changes[len(next.Changes)-1]
	w.publishSettingChange(store.SettingChange{ID: c.ID, Revision: c.Revision, Key: key, Old: c.Old, New: c.New, Actor: c.Actor, At: c.At, Scope: "project file", Applies: "next chat or launch"})
	return fmt.Sprintf("%s set to %s for this project.", key, value), nil
}

// addSessionSettings reads only workspace memory. Saved choices remain visible
// alongside the approved snapshot and current launch state.
func (w *Workspace) addSessionSettings(view *settingscatalog.Snapshot) {
	w.mu.RLock()
	enabled := w.ultraExecution
	goal := w.state.Goal
	panel := w.state.Marshal
	theme := w.theme
	w.mu.RUnlock()
	for i := range view.Rows {
		if view.Rows[i].Key == "ultra-execution" {
			view.Rows[i].Effective = fmt.Sprint(enabled)
		}
	}
	if panel != nil && panel.run.State != "" && panel.run.State != marshal.Closed {
		runSettings := panel.run.Settings
		runSettings.ReviewPolicy = panel.run.EffectiveReviewPolicy()
		conformanceLocked := false
		if w.runtime != nil && w.runtime.Marshal() != nil {
			if mode, locked := w.runtime.Marshal().EffectiveConformancePolicy(context.Background(), panel.run); locked {
				runSettings.ReviewPolicy.Conformance = mode
				conformanceLocked = true
			}
		}
		for i := range view.Rows {
			r := &view.Rows[i]
			if r.Scope != "project" {
				continue
			}
			if _, err := settingscatalog.Get(runSettings, r.Key); err == nil {
				r.Effective = settingscatalog.Effective(runSettings, r.Key)
				r.Source = "approved run snapshot"
				r.Applies = "next run"
				if r.Key == "architecture-conformance" && conformanceLocked {
					r.LockedReason = "mandatory architecture obligation on approved binding"
				}
			}
		}
	}
	add := func(key, value, units, scope, source, applies, help string) {
		view.Rows = append(view.Rows, settingscatalog.Row{Key: key, Value: value, Effective: value, Next: value, Units: units, Scope: scope, Source: source, Applies: applies, Help: help})
	}
	if goal.ID != "" {
		source := fmt.Sprintf("goal revision %d", goal.Revision)
		if goal.BudgetLimits == nil {
			add("goal-ceilings", "unlimited", "", "goal", source, "approved goal revision", "Separate from project run ceilings.")
		} else {
			limits := goal.BudgetLimits
			tokens, cost := "unlimited", "unlimited"
			if limits.MaxTotalTokens != nil {
				tokens = fmt.Sprint(*limits.MaxTotalTokens)
			}
			if limits.MaxCostUSD != nil {
				cost = fmt.Sprint(*limits.MaxCostUSD)
			}
			add("goal-tokens", tokens, "tokens", "goal", source, "approved goal revision", "")
			add("goal-money", cost, "USD", "goal", source, "approved goal revision", "")
			add("goal-wall-time", limits.MaxDuration.String(), "duration", "goal", source, "approved goal revision", "")
			add("goal-model-calls", fmt.Sprint(limits.MaxModelCalls), "calls", "goal", source, "approved goal revision", "")
			add("goal-handoffs", fmt.Sprint(limits.MaxHandoffs), "handoffs", "goal", source, "approved goal revision", "")
			add("goal-retries", fmt.Sprint(limits.MaxRetries), "retries", "goal", source, "approved goal revision", "")
		}
	} else {
		add("goal-ceilings", "no active goal", "", "goal", "workspace", "approved goal revision", "")
	}
	if theme != nil {
		add("current-theme", string(theme.Mode), "", "launch", "CLI, environment and saved user default", "this launch", "")
		add("current-animation", fmt.Sprint(theme.AnimationEnabled), "", "launch", "CLI, environment and saved user default", "this launch", "")
	}
	chat := w.v2LiveChat()
	if chat.Live {
		add("current-provider", chat.Provider, "", "launch", "role picker", "chat restart", "")
		add("current-model", firstNonEmpty(chat.Model, "provider default"), "", "launch", "role picker", "chat restart", "")
		add("current-effort", firstNonEmpty(chatEffortArg(chat.Args), "provider default"), "", "launch", "role picker", "chat restart", "")
	}
	add("usage-probes", map[bool]string{true: "suppressed; admission usage unknown", false: "enabled"}[os.Getenv("MARSHAL_NO_USAGE_PROBES") != ""], "", "launch", "environment", "this launch", "Unknown usage does not bypass quota.")
	for _, key := range []string{"MARSHAL_CLOUD_ENDPOINT", "MARSHAL_CHAT_PERMISSION_CONFIG", "MARSHAL_OPENCODE_DB", "MARSHAL_MCP_BINARY", "MARSHAL_MCP_TOKEN", "MARSHAL_PROPOSAL_MCP_CONFIG", "MARSHAL_PROVIDER_PATH_ONLY", "MARSHAL_CODEX_TRUST_PROFILE"} {
		value := "not configured"
		if os.Getenv(key) != "" {
			value = "configured (redacted)"
		}
		if key == "MARSHAL_CLOUD_ENDPOINT" && os.Getenv(key) == "off" {
			value = "off"
		}
		add(key, value, "", "launch", "environment", "this launch", "Security and transport configuration is read-only here.")
		view.Rows[len(view.Rows)-1].LockedReason = "launch configuration"
	}
}

func (w *Workspace) publishFileSetting(path, key string) {
	p, err := settingscatalog.ReadScopedPreference(path, key)
	if err != nil || len(p.Changes) == 0 {
		return
	}
	c := p.Changes[len(p.Changes)-1]
	w.publishSettingChange(store.SettingChange{ID: c.ID, Revision: c.Revision, Key: c.Key, Old: c.Old, New: c.New, Actor: c.Actor, At: c.At, Scope: "project file", Applies: "next chat or launch"})
}
