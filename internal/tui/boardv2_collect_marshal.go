package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
	"github.com/Zen1th53/marshal/internal/usage"
)

// collectV2Marshal fills the Marshal pane, the Workers pane, the readiness checklist and the Marshal segment of the top bar of the Board v2 view model. A field left at its zero
// value renders as "not known yet".
//
// It reads memory only: the live chat, the last usage reading, the provider
// overview, the run panel and the last off-paint probe
// (boardv2_marshal_state.go), which it starts again when that is old.
func (w *Workspace) collectV2Marshal(b *vm.Board) {
	now := b.Now
	if now.IsZero() {
		now = time.Now()
	}
	in := w.v2MarshalInput(now)
	b.Marshal = buildV2Marshal(in)
	b.Workers = buildV2Workers(in)
	b.Limits = buildV2Limits(in)
	b.Ready = buildV2Ready(in)
	b.Settings = in.Probe.SettingsView
	b.Settings.Rows = append([]settingscatalog.Row(nil), b.Settings.Rows...)
	b.Settings.Changes = append([]settingscatalog.Change(nil), b.Settings.Changes...)
	if b.Settings.Rows == nil {
		b.Settings.Rows = settingscatalog.Rows(marshal.DefaultSettings(), 0)
	}
	w.addSessionSettings(&b.Settings)
	w.addV2ReadyActions(&b.Ready)
}

// v2MarshalIn is everything the builders read, gathered from memory.
type v2MarshalIn struct {
	Now    time.Time
	Chat   v2Chat
	Probe  v2Probe
	Probed bool
	// SignedOut is each provider known not signed in, with the plain reason;
	// SignedIn each one a read-only check found signed in.
	SignedOut map[string]string
	SignedIn  map[string]bool
	// Current is the model each provider's CLI said it would use.
	Current   map[string]string
	Quotas    map[string]usage.Quota
	Providers map[string]store.ProviderStatus
	// Installed is the first-use lookup, used until the first probe.
	Installed map[string]bool
	Run       bool
	RunState  marshal.RunState
	Lanes     map[string]int
	Milestone string
	Sessions  []scanSessions
}

func (w *Workspace) v2MarshalInput(now time.Time) v2MarshalIn {
	in := v2MarshalIn{Now: now, Chat: w.v2LiveChat()}
	var signedOut map[string]string
	in.Probe, in.Probed, signedOut, in.Current = w.v2MarshalSnapshot(now, in.Chat)
	in.SignedOut = map[string]string{}
	in.SignedIn = map[string]bool{}
	for p := range w.v2AuthUnavailable() {
		in.SignedOut[p] = notSignedInReason(p)
	}
	for _, p := range v2ProviderOrder {
		if obs, ok := GetAuthPreflightObservation(p, 0); ok {
			switch obs.Status {
			case "NOT_SIGNED_IN":
				in.SignedOut[p] = notSignedInReason(p)
			case "SIGNED_IN":
				in.SignedIn[p] = true
			}
		}
	}
	for p, reason := range signedOut {
		in.SignedOut[p] = reason
	}
	if qs, ok := w.usageQuotas(); ok {
		in.Quotas = map[string]usage.Quota{}
		for _, q := range qs {
			in.Quotas[q.Provider] = q
		}
	}
	if snap := w.prov.paint().Snap; snap.Loaded {
		in.Providers = map[string]store.ProviderStatus{}
		for _, p := range snap.Providers {
			in.Providers[providerWorker(p.Provider)] = p
		}
	}
	in.Installed = w.installedProviders()
	if p := w.marshalPanel(); p != nil {
		state := p.run.State
		if state == "" {
			state = p.State
		}
		in.RunState = state
		in.Run = state != marshal.Closed
		if in.Run {
			in.Probe.Settings.ReviewPolicy = p.run.EffectiveReviewPolicy()
		}
		in.Lanes = map[string]int{}
		for _, t := range p.Tasks {
			if t.State == marshal.Dispatched {
				if prov := providerOf(t.Worker); prov != "" {
					in.Lanes[prov]++
				}
			}
		}
	}
	if m := w.milestone.Load(); m != nil {
		in.Milestone = m.Now
	}
	w.launch.mu.Lock()
	in.Sessions = append([]scanSessions(nil), w.launch.scan.Sessions...)
	w.launch.mu.Unlock()
	return in
}

// marshalProvider is the provider the Marshal runs on: the live chat's, else
// the project's saved choice.
func (in v2MarshalIn) marshalProvider() string {
	if in.Chat.Live && in.Chat.Provider != "" {
		return in.Chat.Provider
	}
	return in.Probe.Lead
}

// liveSession is the probed session of the live chat; zero when there is
// none.
func (in v2MarshalIn) liveSession() usage.Session {
	if !in.Probe.HasSession || !in.Chat.Live {
		return usage.Session{}
	}
	return in.Probe.Session
}

// marshalModel is the model the Marshal runs on, as the top bar names it
// ("Opus 5.5"): the live session's, the chat's arguments or its own record,
// before the chat runs the model its harness was verified on, then what the
// usage reading or the provider CLI said.
func (in v2MarshalIn) marshalModel(prov string) string {
	id := firstNonEmpty(in.liveSession().Model, chatModelArg(in.Chat.Args), in.Chat.Model)
	if id == "" && !in.Chat.Live {
		id = in.Providers[prov].Model
	}
	id = firstNonEmpty(id, in.Quotas[prov].Model, in.Current[prov])
	return v2ModelName(prov, id)
}

// v2MarshalModelIn gathers just what marshalProvider and marshalModel read:
// the live chat, the last probe, the provider statuses and usage readings.
// The flow collector names the Marshal on the NOW line from it, so the NOW
// line and the top bar's Marshal segment agree.
func (w *Workspace) v2MarshalModelIn(now time.Time) v2MarshalIn {
	in := v2MarshalIn{Now: now, Chat: w.v2LiveChat()}
	in.Probe, in.Probed, _, in.Current = w.v2MarshalSnapshot(now, in.Chat)
	if qs, ok := w.usageQuotas(); ok {
		in.Quotas = map[string]usage.Quota{}
		for _, q := range qs {
			in.Quotas[q.Provider] = q
		}
	}
	if snap := w.prov.paint().Snap; snap.Loaded {
		in.Providers = map[string]store.ProviderStatus{}
		for _, p := range snap.Providers {
			in.Providers[providerWorker(p.Provider)] = p
		}
	}
	return in
}

// v2ModelName is a model id as its provider names it: Claude's tier and
// version ("Opus 5.5"), any other id as its long display name.
func v2ModelName(provider, id string) string {
	long, short := modelDisplayName(id)
	if provider == "claude" {
		return short
	}
	return long
}

// chatEffortArg is the reasoning effort the chat was started with: Claude
// Code's --effort, or Codex's -c model_reasoning_effort=...; "" when none.
func chatEffortArg(args []string) string {
	for i, a := range args {
		if a == "--" {
			break
		}
		if v, ok := strings.CutPrefix(a, "--effort="); ok {
			return v
		}
		if a == "--effort" && i+1 < len(args) {
			return args[i+1]
		}
		if a == "-c" && i+1 < len(args) {
			if v, ok := strings.CutPrefix(args[i+1], "model_reasoning_effort="); ok {
				return strings.Trim(v, `"'`)
			}
		}
	}
	return ""
}

// windowLeft is the least percent left of the quota's known windows whose
// label ends in suffix ("5h", "wk"), with that window; -1 when none is known.
func windowLeft(q usage.Quota, suffix string) (int, usage.Window) {
	left, at := -1, usage.Window{}
	if !q.Known {
		return -1, at
	}
	for _, win := range q.Windows {
		if !win.Known() || !strings.HasSuffix(win.Label, suffix) {
			continue
		}
		if pct := usageLeft(win); left < 0 || pct < left {
			left, at = pct, win
		}
	}
	return left, at
}

// buildV2Marshal is the Marshal pane and the top bar's Marshal segment.
func buildV2Marshal(in v2MarshalIn) vm.Marshal {
	m := vm.Marshal{ContextPct: -1, Limit5hPct: -1, WeeklyPct: -1}
	prov := in.marshalProvider()
	m.Provider = prov
	m.InUse = in.Chat.Live
	if prov == "" {
		m.Doing = "not chosen yet"
		return m
	}
	session := in.liveSession()
	q := in.Quotas[prov]
	m.Model = in.marshalModel(prov)
	m.Effort = firstNonEmpty(session.Effort, chatEffortArg(in.Chat.Args))
	if session.ContextUsed > 0 {
		m.ContextUsed = session.ContextUsed
		if session.ContextWindow > 0 {
			m.ContextWindow = session.ContextWindow
			m.ContextPct = min(100, session.ContextUsed*100/session.ContextWindow)
		}
	}
	m.Limit5hPct, _ = windowLeft(q, "5h")
	m.WeeklyPct, _ = windowLeft(q, "wk")
	m.Doing = v2Doing(in)
	return m
}

// v2Doing is what the Marshal does, in a few words.
func v2Doing(in v2MarshalIn) string {
	if in.Milestone != "" {
		return in.Milestone
	}
	if in.Run {
		switch in.RunState {
		case marshal.Drafting:
			return "planning the tasks"
		case marshal.Approved:
			return "starting the run"
		case marshal.Dispatching:
			return "watching the workers"
		case marshal.Reviewing:
			return "reviewing hand-ins"
		case marshal.Merging:
			return "merging"
		case marshal.Verifying:
			return "verifying the run"
		case marshal.AwaitingUser:
			return "waiting for you"
		}
	}
	if in.Chat.Live {
		return "ready"
	}
	return "not started"
}

// v2Provider is one provider's state as the Workers pane and the checklist
// read it.
type v2Provider struct {
	Name           string
	Installed      bool
	InstalledKnown bool
	SignedIn       bool
	// State is one of: checking, not installed, not signed in, limit,
	// in use, ready, unchecked.
	State  string
	Reason string
	Until  time.Time
	Left   int // percent left of the binding window; -1 unknown
	Window usage.Window
	Lanes  int
	Model  string
	// ModelID is the model id its workers run ("" unknown): the binding
	// window is that model's tightest, as dispatch reads it.
	ModelID string
}

func (in v2MarshalIn) provider(name string) v2Provider {
	p := v2Provider{Name: name, Left: -1, Lanes: in.Lanes[name]}
	installed := in.Probe.Installed
	if !in.Probed {
		installed = in.Installed
	}
	if installed != nil {
		p.Installed, p.InstalledKnown = installed[name], true
	}
	q, hasQuota := in.Quotas[name]
	st, listed := in.Providers[name]
	p.ModelID = in.workerModelID(name)
	if win, ok := modelBinding(q, p.ModelID); ok {
		p.Left, p.Window = usageLeft(win), win
	}
	p.Model = v2ModelName(name, firstNonEmpty(st.Model, q.Model))
	live := in.Chat.Live && in.Chat.Provider == name
	if live {
		// A live Marshal session on this provider proves it is installed
		// and signed in (board v2 U5).
		p.Installed, p.InstalledKnown = true, true
	}
	switch {
	case !p.InstalledKnown:
		p.State = "checking"
		return p
	case !p.Installed:
		p.State, p.Reason = "not installed", name+" is not installed: put its CLI on PATH, then press v"
		return p
	}
	if reason, out := in.SignedOut[name]; out && !live {
		p.State, p.Reason = "not signed in", reason
		return p
	}
	if q.Reason == usage.ReasonNotSignedIn && !live {
		// agy's limits report asked for authentication: signed out, not
		// at a limit. Only the plain reason is shown, never its sign-in URL.
		p.State, p.Reason = "not signed in", notSignedInReason(name)
		return p
	}
	p.SignedIn = live || in.SignedIn[name] || (listed && !st.VerifiedAt.IsZero() && !st.Expired) ||
		(hasQuota && q.Known && !q.Stale) || anyOffered(q)
	switch {
	case listed && providerInLimit(st, in.Now):
		p.State, p.Until = "limit", st.LimitUntil
		p.Reason = fmt.Sprintf("%s is at its limit until %s", name, providerUntil(st.LimitUntil, in.Now))
	case p.Left == 0 || q.OfferedNone():
		p.State, p.Until = "limit", p.Window.ResetsAt
		p.Reason = name + " has no quota left"
		if family, _ := usage.SplitLabel(p.Window.Label); family != "" && p.Left == 0 {
			p.Reason += " for " + family
		}
		if !p.Window.ResetsAt.IsZero() {
			p.Reason += " until " + providerUntil(p.Window.ResetsAt, in.Now)
		}
	case live:
		p.State = "in use"
	case p.SignedIn:
		p.State = "ready"
	default:
		p.State, p.Reason = "unchecked", name+" has not been checked yet: v checks it (one model call)"
	}
	return p
}

// anyOffered is whether a provider that lists its model offer (agy) offers
// any family now: it answered, so it is signed in.
func anyOffered(q usage.Quota) bool {
	for _, g := range q.Offer {
		if g.Offered {
			return true
		}
	}
	return false
}

// buildV2Workers is the Workers pane: one row per provider.
func buildV2Workers(in v2MarshalIn) []vm.Worker {
	out := make([]vm.Worker, 0, len(v2ProviderOrder))
	for _, name := range v2ProviderOrder {
		p := in.provider(name)
		row := vm.Worker{Provider: name, Glyph: providerGlyph(name, true), LeftPct: p.Left, Installed: p.Installed, SignedIn: p.SignedIn, Reason: p.Reason, Detail: p.Model}
		switch {
		case p.Lanes > 0 && p.Installed:
			row.Tag, row.TagTone = fmt.Sprintf("%d lanes", p.Lanes), vm.ToneOK
			if p.Lanes == 1 {
				row.Tag = "1 lane"
			}
		case p.State == "in use":
			row.Tag, row.TagTone = "in use", vm.ToneOK
		case p.State == "ready" && in.Run:
			row.Tag, row.TagTone = "spare", vm.ToneMuted
		case p.State == "ready":
			row.Tag, row.TagTone = "ready", vm.ToneOK
		case p.State == "not installed":
			row.Tag, row.TagTone = "not installed", vm.ToneMuted
		case p.State == "not signed in":
			row.Tag, row.TagTone = "not signed in", vm.ToneWarn
		case p.State == "limit":
			row.Tag, row.TagTone = "at limit", vm.ToneBad
			if !p.Until.IsZero() {
				row.Detail = "until " + resetClock(p.Until, in.Now)
			}
		case p.State == "unchecked":
			row.Tag, row.TagTone = "not checked", vm.ToneWarn
		default:
			row.Tag, row.TagTone = "checking", vm.ToneMuted
		}
		if p.State != "limit" && p.Installed {
			if wk, _ := windowLeft(modelQuota(in.Quotas[name], p.ModelID), "wk"); wk >= 0 && wk <= 30 {
				row.Detail = fmt.Sprintf("wk %d%%", wk)
				if row.TagTone == vm.ToneOK || row.TagTone == vm.ToneMuted {
					row.TagTone = vm.ToneWarn
				}
			}
		}
		out = append(out, row)
	}
	return out
}

// buildV2Ready is the first-open checklist: the Marshal, each installed
// worker provider, the platform, the repository and the browser, then the
// telemetry choice and the project's defaults. Checks that need a probe are
// left out until the first probe finished.
func buildV2Ready(in v2MarshalIn) vm.Readiness {
	var r vm.Readiness
	add := func(c vm.Check) { r.Checks = append(r.Checks, c) }
	settings := in.Probe.Settings
	if !in.Probe.HasSettings {
		settings = marshal.DefaultSettings()
	}
	limits, globalCap := app.WorkerConcurrency(settings)
	if in.Probed {
		verdict := in.readiness()
		if verdict.Blocker != "" {
			add(vm.Check{Mark: "bad", Label: "Readiness", Text: verdict.Blocker, Tag: "action"})
		}
		guaranteeMark := "ok"
		if verdict.NeedsConfirmation {
			guaranteeMark = "warn"
		}
		add(vm.Check{Mark: guaranteeMark, Label: "Guarantee", Text: verdict.Guarantee})
	}

	// The Marshal.
	if prov := in.marshalProvider(); prov == "" {
		if in.Probed {
			add(vm.Check{Mark: "bad", Label: "Marshal", Text: "no Marshal provider chosen yet", Tag: "choose", Hint: "m picks the provider the Marshal runs on"})
		}
	} else {
		p := in.provider(prov)
		name := prov
		if model := buildV2Marshal(in).Model; model != "" {
			name += " · " + model
		}
		switch p.State {
		case "in use":
			add(vm.Check{Mark: "ok", Label: "Marshal", Text: name + " · signed in", Tag: "in use"})
		case "ready":
			add(vm.Check{Mark: "ok", Label: "Marshal", Text: name + " · signed in", Tag: "ready"})
		case "not installed":
			add(vm.Check{Mark: "bad", Label: "Marshal", Text: prov + " is not installed", Tag: "install", Hint: "put " + prov + " on PATH, or m picks another provider"})
		case "not signed in":
			add(vm.Check{Mark: "warn", Label: "Marshal", Text: prov + " is installed but not signed in", Tag: "action", Hint: signInHint(prov)})
		case "limit":
			add(vm.Check{Mark: "warn", Label: "Marshal", Text: p.Reason, Tag: "limit", Hint: "m picks another provider"})
		case "checking":
			add(vm.Check{Mark: "warn", Label: "Marshal", Text: name + " · checking", Tag: "checking"})
		default:
			add(vm.Check{Mark: "warn", Label: "Marshal", Text: name + " · not checked yet", Tag: "check", Hint: "v checks it (one model call)"})
		}
	}

	// Workers: each installed provider.
	installed := 0
	for _, name := range v2ProviderOrder {
		p := in.provider(name)
		if !p.Installed {
			continue
		}
		installed++
		head := name
		if p.Model != "" {
			head += " " + p.Model
		}
		if n := limits[name]; n > 0 {
			head += fmt.Sprintf(" · up to %d at once", n)
		}
		wk, wkWin := windowLeft(modelQuota(in.Quotas[name], p.ModelID), "wk")
		switch {
		case p.State == "not signed in":
			add(vm.Check{Mark: "warn", Label: "Workers", Text: name + " is installed but not signed in", Tag: "action", Hint: signInHint(name)})
		case p.State == "limit":
			add(vm.Check{Mark: "warn", Label: "Workers", Text: p.Reason, Tag: "limit"})
		case p.State == "unchecked":
			add(vm.Check{Mark: "warn", Label: "Workers", Text: name + " is installed, not checked yet", Tag: "check", Hint: "v checks it (one model call)"})
		case wk >= 0 && wk <= 30:
			text := fmt.Sprintf("%s signed in · weekly %d%%", name, wk)
			if !wkWin.ResetsAt.IsZero() {
				text += " · resets " + resetClock(wkWin.ResetsAt, in.Now)
			}
			add(vm.Check{Mark: "warn", Label: "Workers", Text: text, Tag: "low"})
		default:
			add(vm.Check{Mark: "ok", Label: "Workers", Text: head, Tag: "ready"})
		}
	}
	if !in.Probed {
		return v2Count(r, in, limits, globalCap)
	}
	if installed == 0 {
		add(vm.Check{Mark: "bad", Label: "Workers", Text: "no provider CLI is installed", Tag: "install", Hint: "install claude, codex, agy or opencode, then press v"})
	}

	// Platform: the confinement refusal a worker would stop on.
	if in.Probe.PlatformOK {
		add(vm.Check{Mark: "ok", Label: "Platform", Text: in.Probe.Platform + " · workers can run here", Tag: "ready"})
	} else {
		add(vm.Check{Mark: "bad", Label: "Platform", Text: in.Probe.Platform + " · workers cannot run here", Tag: "blocked", Hint: in.Probe.PlatformWhy})
	}

	// Repo.
	if repo := in.Probe.Repo; !repo.OK {
		add(vm.Check{Mark: "warn", Label: "Repo", Text: "not a git repository", Tag: "action", Hint: "workers start from a commit: git init and commit first"})
	} else {
		text, mark, tag, hint := "clean", "ok", "ready", ""
		if repo.Changed > 0 {
			text = fmt.Sprintf("%d changed", repo.Changed)
			mark, tag, hint = "warn", "changed", "workers start from the last commit; commit what they should see"
		}
		text += " · " + firstNonEmpty(repo.Branch, "detached")
		if repo.Head != "" {
			text += " @ " + repo.Head
		}
		if s := v2EarlierSessions(in.Sessions); s != "" {
			text += " · " + s
		}
		add(vm.Check{Mark: mark, Label: "Repo", Text: text, Tag: tag, Hint: hint})
	}

	// Browser: no display is not a fault; links are shown instead.
	if in.Probe.Browser {
		add(vm.Check{Mark: "ok", Label: "Browser", Text: "links open in your browser", Tag: "ready"})
	} else {
		add(vm.Check{Mark: "ok", Label: "Browser", Text: "no display: links are shown on the Board", Tag: "ready"})
	}
	return v2Count(r, in, limits, globalCap)
}

// v2Count finishes the checklist: the counts, telemetry and defaults.
func v2Count(r vm.Readiness, in v2MarshalIn, limits map[string]int, globalCap int) vm.Readiness {
	for _, c := range r.Checks {
		if c.Mark == "ok" {
			r.Ready++
		}
	}
	r.Total = len(r.Checks)
	r.TelemetryOn = in.Probe.TelemetryOn
	// TelemetryAsk: the owner has not been told yet, so the choice is asked.
	r.TelemetryAsk = in.Probed && in.Probe.TelemetryOn && !in.Probe.Told
	r.Defaults = v2Defaults(in, limits, globalCap)
	return r
}

// v2EarlierSessions is how many earlier provider sessions the launch scan
// found for this project: "2 earlier Codex sessions".
func v2EarlierSessions(ss []scanSessions) string {
	var parts []string
	for _, s := range ss {
		if s.Found <= 0 {
			continue
		}
		word := "sessions"
		if s.Found == 1 {
			word = "session"
		}
		parts = append(parts, fmt.Sprintf("%d earlier %s %s", s.Found, app.ProviderDisplayName(s.Provider), word))
	}
	return strings.Join(parts, " · ")
}

// v2Defaults is the project's defaults as the checklist lists them.
func v2Defaults(in v2MarshalIn, limits map[string]int, globalCap int) [][2]string {
	s := in.Probe.Settings
	if !in.Probe.HasSettings {
		s = marshal.DefaultSettings()
	}
	var rows [][2]string
	if len(limits) > 0 {
		var parts []string
		for _, name := range v2ProviderOrder {
			if (!in.Probed || in.provider(name).Installed) && limits[name] > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", name, limits[name]))
			}
		}
		if len(parts) > 0 && globalCap > 0 {
			parts = append(parts, fmt.Sprintf("total %d", globalCap))
		}
		if len(parts) > 0 {
			rows = append(rows, [2]string{"workers", strings.Join(parts, " · ")})
		}
	}
	var order []string
	for _, p := range app.GetEffectiveFailoverOrder(s) {
		if name := providerWorker(p); in.provider(name).Installed {
			order = append(order, name)
		}
	}
	if len(order) > 0 {
		rows = append(rows, [2]string{"fallback", strings.Join(order, " → ")})
	}
	// Every hand-in is reviewed by a provider other than its worker's.
	rows = append(rows, [2]string{"review", "other provider"})
	approval := "you: architecture"
	switch s.AcceptanceMode {
	case marshal.AcceptUser:
		approval += " · tasks: you"
	case marshal.AcceptMarshalThenUser:
		approval += " · tasks: Marshal, then you"
	case marshal.AcceptMarshal:
		approval += " · tasks: Marshal"
	}
	rows = append(rows, [2]string{"approval", approval})
	return rows
}
