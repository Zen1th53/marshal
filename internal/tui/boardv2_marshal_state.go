package tui

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter/providers"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
	"github.com/Zen1th53/marshal/internal/usage"
	"github.com/Zen1th53/marshal/internal/worker"
)

// The Board v2 Marshal collector runs every frame, so it only ever reads
// memory. Everything that costs a file read, a git query, a store read or a
// provider CLI is probed here, off the paint path, at most every
// v2MarshalEvery (and at once when the Marshal chat or its transcript
// changes); a frame paints with the last probe, or with "not known yet"
// before the first one finished.

// v2MarshalEvery is how often the collector's probes run again.
const v2MarshalEvery = 15 * time.Second

// v2CurrentModelEvery bounds how often the Marshal provider's CLI is asked
// which model it would use, when nothing it wrote says so.
const v2CurrentModelEvery = 30 * time.Minute

// v2ProviderOrder is the Workers pane's providers, in the design's order.
var v2ProviderOrder = []string{"claude", "agy", "codex", "opencode"}

// v2MarshalState is the collector's cache: the last probe and what verify
// results said about sign-in.
type v2MarshalState struct {
	mu      sync.Mutex
	probe   v2Probe
	at      time.Time // when the last probe finished; zero before the first
	key     string    // the chat the last probe read
	running bool
	// signedOut is each provider a verify found not signed in, with the
	// plain reason; a verify that succeeds clears it.
	signedOut map[string]string
	// current is the model each provider's CLI said it would use, and when.
	current   map[string]string
	currentAt map[string]time.Time
}

// v2Repo is the project repository as the checklist shows it.
type v2Repo struct {
	OK      bool
	Branch  string
	Head    string
	Changed int
}

// v2Probe is one off-paint reading.
type v2Probe struct {
	SettingsView settingscatalog.Snapshot
	Lead         string
	Installed    map[string]bool
	Settings     marshal.Settings
	HasSettings  bool
	Repo         v2Repo
	PlatformOK   bool
	Platform     string
	PlatformWhy  string
	Browser      bool
	BrowserWhy   string
	TelemetryOn  bool
	Told         bool
	Session      usage.Session
	HasSession   bool
}

// v2ProbeInput is what a probe reads besides the workspace's own state.
type v2ProbeInput struct {
	SettingsView func(context.Context) (settingscatalog.Snapshot, error)
	Root         string
	Chat         v2Chat
	Probing      bool // provider CLIs may be asked (a screen is up, not a test)
	Workdir      string
	Settings     func(context.Context) (marshal.Settings, bool)
}

// v2ProbeParts are the probes; tests replace them so no git, store or
// provider CLI runs.
var v2ProbeParts = struct {
	lead      func(root string) string
	lookPath  func(string) (string, error)
	repo      func(ctx context.Context, root string) v2Repo
	platform  func() (ok bool, host, why string)
	browser   func() (ok bool, why string)
	telemetry func(root string) (on, told bool)
	session   func(provider, path string) (usage.Session, bool)
	current   func(ctx context.Context, provider string) (string, bool)
}{
	lead:      loadDefaultProvider,
	lookPath:  func(name string) (string, error) { return lookPath(name) },
	repo:      defaultV2Repo,
	platform:  app.WorkerPlatform,
	browser:   defaultV2Browser,
	telemetry: app.TelemetryChoice,
	session:   usage.ReadSession,
	current:   defaultV2CurrentModel,
}

// v2Chat is the live Marshal chat, read from memory each frame.
type v2Chat struct {
	Live     bool
	Provider string
	Model    string // the native chat's model, or ""
	Args     []string
	History  string
}

// v2LiveChat is the Marshal chat now running: the tmux chat pane or the
// native chat, whichever is live.
func (w *Workspace) v2LiveChat() v2Chat {
	w.tmuxMu.Lock()
	a := w.tmuxActiveWins["marshal-chat"]
	var c v2Chat
	if a != nil && a.state != "done" && a.state != "failed" {
		c = v2Chat{Live: true, Provider: canonicalNeutralProvider(a.provider), Args: slices.Clone(a.args), History: a.historyPath}
	}
	w.tmuxMu.Unlock()
	if c.Live {
		return c
	}
	w.chat.mu.Lock()
	if w.chat.tr != nil {
		c = v2Chat{Live: true, Provider: canonicalNeutralProvider(w.chat.provider), Model: w.chat.model}
	}
	w.chat.mu.Unlock()
	return c
}

// v2MarshalSnapshot is the last probe, a copy of what verify said about
// sign-in, and the models the provider CLIs reported. It starts a probe in
// the background when the last one is old or read another chat; it never
// waits for one.
func (w *Workspace) v2MarshalSnapshot(now time.Time, chat v2Chat) (p v2Probe, probed bool, signedOut, current map[string]string) {
	key := chat.Provider + "\x00" + chat.History
	s := &w.v2m
	s.mu.Lock()
	p, probed = s.probe, !s.at.IsZero()
	signedOut, current = cloneStrings(s.signedOut), cloneStrings(s.current)
	start := !s.running && (s.at.IsZero() || now.Sub(s.at) >= v2MarshalEvery || s.key != key)
	if start {
		s.running = true
	}
	s.mu.Unlock()
	if start {
		in := w.v2ProbeInput(chat)
		if !w.startBackground(context.Background(), func(ctx context.Context) { w.refreshV2Marshal(ctx, in) }) {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}
	}
	return p, probed, signedOut, current
}

func cloneStrings(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// v2ProbeInput gathers, under the workspace's locks, what a probe needs.
func (w *Workspace) v2ProbeInput(chat v2Chat) v2ProbeInput {
	w.mu.RLock()
	rt, st, projectID, wd := w.runtime, w.store, w.projectID, w.workDir
	w.mu.RUnlock()
	in := v2ProbeInput{Root: w.providerRoot(), Chat: chat, Workdir: wd, Probing: w.screenShown() && os.Getenv("MARSHAL_NO_USAGE_PROBES") == ""}
	switch {
	case rt != nil && rt.Marshal() != nil:
		svc := rt.Marshal()
		in.SettingsView = func(ctx context.Context) (settingscatalog.Snapshot, error) {
			return readSettingsView(ctx, svc.Store, svc.ProjectID, in.Root)
		}
		in.Settings = func(ctx context.Context) (marshal.Settings, bool) {
			rec, err := svc.Store.GetMarshalSettings(ctx, svc.ProjectID)
			return rec.Value, err == nil
		}
	case st != nil && projectID != "":
		in.SettingsView = func(ctx context.Context) (settingscatalog.Snapshot, error) {
			return readSettingsView(ctx, st, projectID, in.Root)
		}
		in.Settings = func(ctx context.Context) (marshal.Settings, bool) {
			rec, err := st.GetMarshalSettings(ctx, projectID)
			return rec.Value, err == nil
		}
	}
	return in
}

// refreshV2Marshal runs one probe and stores it. Tests call it directly.
func (w *Workspace) refreshV2Marshal(ctx context.Context, in v2ProbeInput) {
	parts := v2ProbeParts
	var p v2Probe
	if in.SettingsView != nil {
		var err error
		p.SettingsView, err = in.SettingsView(ctx)
		if err != nil {
			p.SettingsView = settingscatalog.Snapshot{Rows: []settingscatalog.Row{{Key: "settings", Effective: "unavailable", Help: err.Error()}}, Changes: []settingscatalog.Change{}}
		}
	}
	p.Lead = parts.lead(in.Root)
	p.Installed = map[string]bool{}
	for _, name := range v2ProviderOrder {
		_, err := parts.lookPath(name)
		p.Installed[name] = err == nil
	}
	if in.Settings != nil {
		p.Settings, p.HasSettings = in.Settings(ctx)
	}
	if !p.HasSettings {
		p.Settings = marshal.DefaultSettings()
	}
	repoCtx, cancel := context.WithTimeout(ctx, launchScanPartTimeout)
	p.Repo = parts.repo(repoCtx, firstNonEmpty(in.Root, in.Workdir))
	cancel()
	p.PlatformOK, p.Platform, p.PlatformWhy = parts.platform()
	p.Browser, p.BrowserWhy = parts.browser()
	p.TelemetryOn, p.Told = parts.telemetry(in.Root)
	if in.Chat.Live && in.Chat.History != "" {
		p.Session, p.HasSession = parts.session(in.Chat.Provider, in.Chat.History)
	}
	// Before the chat runs, the model its CLI would use is asked through the
	// provider registry, at most every v2CurrentModelEvery and never from a
	// test. Claude Code answers only by starting a session (a model call),
	// and a model call is never made without the owner's key, so Claude's
	// model is read from its transcript once the chat runs, or from its last
	// verified harness profile.
	marshalProvider := firstNonEmpty(in.Chat.Provider, p.Lead)
	ask := in.Probing && !in.Chat.Live && marshalProvider != "" && marshalProvider != "claude" && p.Installed[marshalProvider]
	if ask {
		w.v2m.mu.Lock()
		ask = time.Since(w.v2m.currentAt[marshalProvider]) >= v2CurrentModelEvery
		if ask {
			if w.v2m.currentAt == nil {
				w.v2m.currentAt = map[string]time.Time{}
			}
			w.v2m.currentAt[marshalProvider] = time.Now()
		}
		w.v2m.mu.Unlock()
	}
	var current string
	if ask {
		if m, ok := parts.current(ctx, marshalProvider); ok {
			current = m
		}
	}
	// The provider overview (verified models, lanes, cooldowns) is read
	// again when it is old, by the same read the Providers view makes.
	w.prov.mu.Lock()
	stale := time.Since(w.prov.snap.At) >= 30*time.Second
	w.prov.mu.Unlock()
	if stale && w.providerSrc() != nil {
		w.refreshProviders(ctx)
	}
	s := &w.v2m
	s.mu.Lock()
	s.probe, s.at, s.key, s.running = p, time.Now(), in.Chat.Provider+"\x00"+in.Chat.History, false
	if current != "" {
		if s.current == nil {
			s.current = map[string]string{}
		}
		s.current[marshalProvider] = current
	}
	s.mu.Unlock()
	w.requestRepaint()
}

// noteV2Verify records what a verify of provider said about its sign-in: a
// failure classified as a missing sign-in marks it not signed in, and a
// success clears that.
func (w *Workspace) noteV2Verify(provider string, err error) {
	if w == nil {
		return
	}
	name := firstNonEmpty(canonicalNeutralProvider(provider), providerWorker(provider))
	if name == "" {
		return
	}
	s := &w.v2m
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case err == nil:
		delete(s.signedOut, name)
	case verifySignedOut(name, err):
		if s.signedOut == nil {
			s.signedOut = map[string]string{}
		}
		s.signedOut[name] = notSignedInReason(name)
	}
}

// v2AuthUnavailable is each provider this workspace found not signed in
// while opening a chat or running a worker, with its message.
func (w *Workspace) v2AuthUnavailable() map[string]string {
	w.mu.RLock()
	sessionID, workDir := w.sessionID, w.workDir
	w.mu.RUnlock()
	keys := authWorkspaceKeys(sessionID, workDir, w.providerRoot())
	out := map[string]string{}
	authUnavailableMu.RLock()
	for _, k := range keys {
		for p, msg := range authUnavailableState[k] {
			out[p] = msg
		}
	}
	authUnavailableMu.RUnlock()
	return out
}

// defaultV2Repo is the repository's branch, short commit and how many paths
// differ from it, by the launch scan's read-only git runner.
func defaultV2Repo(ctx context.Context, root string) v2Repo {
	if root == "" {
		return v2Repo{}
	}
	branch, err := scanGitOutput(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return v2Repo{}
	}
	r := v2Repo{OK: true, Branch: strings.TrimSpace(string(branch))}
	if head, err := scanGitOutput(ctx, root, "rev-parse", "--short", "HEAD"); err == nil {
		r.Head = strings.TrimSpace(string(head))
	}
	if status, err := scanGitOutput(ctx, root, "status", "--porcelain"); err == nil {
		for _, l := range strings.Split(string(status), "\n") {
			if strings.TrimSpace(l) != "" {
				r.Changed++
			}
		}
	}
	return r
}

// defaultV2Browser is whether a link can open in a browser here, the way
// openArchitectureBrowser opens one, and why not when it cannot.
func defaultV2Browser() (bool, string) {
	switch runtime.GOOS {
	case "darwin":
		if os.Getenv("SSH_CONNECTION") != "" && os.Getenv("DISPLAY") == "" {
			return false, "no display"
		}
		if _, err := exec.LookPath("open"); err != nil {
			return false, "no open command"
		}
		return true, ""
	case "windows":
		return true, ""
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return false, "no display"
	}
	if _, err := exec.LookPath("xdg-open"); err != nil {
		return false, "no xdg-open"
	}
	return true, ""
}

// defaultV2CurrentModel asks the provider registry which model the
// installed CLI would use now; a provider whose CLI does not report one
// answers false.
func defaultV2CurrentModel(ctx context.Context, provider string) (string, bool) {
	p, ok := providers.Get(provider)
	if !ok {
		return "", false
	}
	if _, reports := p.(providers.CurrentModelReporter); !reports {
		return "", false
	}
	binary, err := project.FindBinary(p.Spec().Binary)
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	current, ok, err := providers.CurrentModel(ctx, provider, binary, worker.New(60*time.Second, 2*time.Second, 8<<20))
	if err != nil {
		return "", false
	}
	return current, ok && current != ""
}
