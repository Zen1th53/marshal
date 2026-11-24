package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// The Team and Architecture pages' actions. Each one is offered only while
// the state it was offered for still holds (its attempt id is a digest of
// that state, checked again when the admitted action starts), names an
// existing terminal writer, and carries no path, command or credential from
// the browser:
//
//	team/provider.verify         the harness verify the Providers view runs
//	team/provider.route-around   the same reassign route, task by task
//	team/provider.signin.start   the Board's own agy sign-in window; the
//	                             sign-in itself happens in that terminal
//	team/session.import.request  an earlier-session candidate by opaque id,
//	                             read only under the operator's read grant
//	team/architecture.open       the accepted architecture on its read-only
//	                             review, never the review that can Accept
//	team/telemetry.choose        the usage counts choice the terminal asks

// webImportCandidate is one earlier-session source for this project.
type webImportCandidate struct {
	Provider string
	Folder   string
	Granted  bool
}

// webTeamParts are the effects and reads behind the team actions; tests
// replace them.
var webTeamParts struct {
	providers  func(ctx context.Context, w *Workspace) []store.ProviderStatus
	candidates func(ctx context.Context, w *Workspace) []webImportCandidate
	load       func(ctx context.Context, w *Workspace, provider, folder string) error
	request    func(w *Workspace, provider, folder string)
	accepted   func(ctx context.Context, w *Workspace) (int64, string)
	openReview func(ctx context.Context, w *Workspace, digest string) error
}

// The defaults are set in init: a literal would be an initialization cycle
// through the permission queue.
func init() {
	webTeamParts.providers = func(ctx context.Context, w *Workspace) []store.ProviderStatus {
		src := w.providerSrc()
		if src == nil {
			return nil
		}
		rows, err := src.ProviderOverview(ctx)
		if err != nil {
			return nil
		}
		w.prov.mu.Lock()
		installed := slices.Clone(w.prov.snap.Installed)
		w.prov.mu.Unlock()
		for _, p := range installed {
			if !slices.ContainsFunc(rows, func(r store.ProviderStatus) bool { return providerWorker(r.Provider) == providerWorker(p.Provider) }) {
				rows = append(rows, p)
			}
		}
		return rows
	}
	webTeamParts.candidates = func(ctx context.Context, w *Workspace) []webImportCandidate {
		if w.runtime == nil {
			return nil
		}
		var out []webImportCandidate
		for _, provider := range []string{"claude", "codex"} {
			if folder := w.runtime.EarlierWorkFolder(provider); folder != "" {
				out = append(out, webImportCandidate{provider, folder, w.runtime.HasReadGrant(folder)})
			}
		}
		return out
	}
	webTeamParts.load = func(ctx context.Context, w *Workspace, provider, folder string) error {
		_, err := w.continueEarlierWork(ctx, provider, folder)
		return err
	}
	webTeamParts.request = func(w *Workspace, provider, folder string) {
		w.permissions.mu.Lock()
		if w.permissions.continuations == nil {
			w.permissions.continuations = make(map[string]string)
		}
		w.permissions.continuations[folder] = provider
		w.permissions.mu.Unlock()
		w.queuePermission(permission.Request{Kind: "read", Object: folder, Scope: "this project, until revoked, read-only", Who: "Marshal", Reason: "Load earlier " + provider + " work for this project", ContinuationProvider: provider})
	}
	webTeamParts.accepted = func(ctx context.Context, w *Workspace) (int64, string) {
		if w.runtime == nil {
			return 0, ""
		}
		return w.runtime.AcceptedArchitecture(ctx)
	}
	webTeamParts.openReview = func(ctx context.Context, w *Workspace, digest string) error {
		if w.runtime == nil {
			return errNoRuntime
		}
		session, err := w.runtime.StartAcceptedArchitectureReview(ctx, "", w.sessionID, digest, app.ArchitectureReviewHooks{})
		if err != nil {
			return err
		}
		// The link goes to the browser opener only: never to a frame,
		// a receipt or Activity.
		if err = openArchitectureBrowser(ctx, session.URL); err != nil {
			_ = session.Close()
			return err
		}
		w.webTeam.mu.Lock()
		old := w.webTeam.review
		w.webTeam.review = session
		w.webTeam.mu.Unlock()
		if old != nil {
			_ = old.Close()
		}
		return nil
	}
}

// webTeamState is what the team actions keep between calls.
type webTeamState struct {
	mu     sync.Mutex
	review *app.ArchitectureReviewSession
}

func teamDigest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8])
}

// webCandidateID is a candidate's opaque id: the browser names it, the
// Board resolves it to a provider and folder itself.
func webCandidateID(project string, c webImportCandidate) string {
	return teamDigest("import", project, c.Provider, c.Folder)
}

func webProviderBinding(p store.ProviderStatus, now time.Time) string {
	return teamDigest("provider", providerWorker(p.Provider), p.Model, providerState(p, now), fmt.Sprint(p.VerifiedAt.Unix()), fmt.Sprint(p.LimitUntil.Unix()))
}

// providerRoutePlan is where the open run's routable tasks on from would go
// (the first other provider that is ready) and which tasks move.
func providerRoutePlan(rows []store.ProviderStatus, from string, tasks []marshal.Task, now time.Time) (string, []string) {
	to := ""
	for _, p := range rows {
		if to == "" && providerWorker(p.Provider) != providerWorker(from) && providerState(p, now) == "ready" {
			to = providerWorker(p.Provider)
		}
	}
	var ids []string
	for _, t := range tasks {
		if providerWorker(t.Worker) == providerWorker(from) && routableTask(t) {
			ids = append(ids, t.PlanTaskID)
		}
	}
	return to, ids
}

func (w *Workspace) webRunTasks() []marshal.Task {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if p := w.state.Marshal; p != nil {
		return slices.Clone(p.run.Tasks)
	}
	return nil
}

// webTeamCapabilities lists the team actions the Board takes now.
func (w *Workspace) webTeamCapabilities(ctx context.Context) []webboard.SettingCapability {
	var caps []webboard.SettingCapability
	add := func(op, id, binding string, rev int64, values ...string) {
		caps = append(caps, webboard.SettingCapability{Op: op, Target: webboard.Target{Kind: "team", ID: id}, Revision: rev, AttemptID: binding, Values: values})
	}
	now := time.Now()
	rows := webTeamParts.providers(ctx, w)
	tasks := w.webRunTasks()
	w.v2m.mu.Lock()
	probed, told, telemetry := !w.v2m.at.IsZero(), w.v2m.probe.Told, w.v2m.probe.TelemetryOn
	w.v2m.mu.Unlock()
	signIn := w.v2ReadyState()
	signIn.mu.Lock()
	offered, signing := signIn.offered, signIn.signingIn != ""
	signIn.mu.Unlock()
	for _, p := range rows {
		name := providerWorker(p.Provider)
		binding := webProviderBinding(p, now)
		if !providerInLimit(p, now) {
			add("team/provider.verify", name, binding, 0)
		}
		if to, ids := providerRoutePlan(rows, name, tasks, now); to != "" && len(ids) > 0 {
			add("team/provider.route-around", name, teamDigest(binding, to, strings.Join(ids, ",")), 0)
		}
		if name == "agy" && offered && !signing {
			add("team/provider.signin.start", name, teamDigest(binding, "signin"), 0)
		}
	}
	if probed && telemetry && !told {
		add("team/telemetry.choose", "telemetry", teamDigest("telemetry"), 0, "keep", "off")
	}
	project := w.webProjectID()
	for _, c := range webTeamParts.candidates(ctx, w) {
		add("team/session.import.request", webCandidateID(project, c), teamDigest("import", c.Provider, c.Folder, fmt.Sprint(c.Granted)), 0)
	}
	if rev, digest := webTeamParts.accepted(ctx, w); digest != "" {
		add("team/architecture.open", "architecture", digest, rev)
	}
	return caps
}

func (w *Workspace) webProjectID() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.ultraProjectLocked()
}

// webTeamCommand admits a team action against the capabilities offered now,
// journals its start and returns the effect to run after the commit.
func (w *Workspace) webTeamCommand(a webboard.Action) webboard.Result {
	if a.Command == nil || a.Commit == nil {
		return refused("Team authority is unavailable.")
	}
	i := *a.Command
	var payload webboard.SettingPayload
	if json.Unmarshal([]byte(i.Value), &payload) != nil {
		return refused("Invalid team payload.")
	}
	var execute func() webboard.Result
	result := a.Commit(func() webboard.Result {
		if w.store == nil {
			return refused("Team authority is unavailable.")
		}
		w.mu.RLock()
		current := w.ultraProjectLocked() == i.ProjectID && webboard.SettingsEpoch(w.webSettingsFenceLocked()) == i.ProjectEpoch
		w.mu.RUnlock()
		if !current {
			return refused("The project or session changed.")
		}
		var offered bool
		for _, c := range w.webTeamCapabilities(context.Background()) {
			if c.Op == i.Op && c.Target.ID == i.Key && c.AttemptID == i.AttemptID && c.Revision == i.Revision {
				offered = true
			}
		}
		if !offered {
			return refused("That action is not available now; refresh and look again.")
		}
		if err := w.store.StartWebRequest(context.Background(), i, i.AttemptID); err != nil {
			return refused("The team action journal could not be recorded.")
		}
		execute = func() webboard.Result {
			ctx := w.workers.context()
			res, effect := w.webTeamEffect(ctx, i, payload)
			status, code := "applied", "applied"
			if res.Status == "refused" {
				status, code = "refused", "refused"
			}
			effects := []string{}
			if res.Status != "refused" {
				effects = []string{effect}
			}
			_ = w.store.FinishWebRequest(context.Background(), i, store.WebReceipt{RequestID: i.RequestID, Status: status, Code: code, Revision: i.Revision, Effects: effects, Result: res.Text})
			w.requestRepaint()
			return res
		}
		return webboard.Result{Status: "started"}
	})
	if execute != nil {
		return execute()
	}
	return result
}

// webTeamEffect runs an admitted action. Its answer never carries a link.
func (w *Workspace) webTeamEffect(ctx context.Context, i store.WebIntent, p webboard.SettingPayload) (webboard.Result, string) {
	now := time.Now()
	switch i.Op {
	case "team/provider.verify", "team/provider.route-around":
		src := w.providerSrc()
		if src == nil {
			return refused("Providers are unavailable."), ""
		}
		var row store.ProviderStatus
		rows := webTeamParts.providers(ctx, w)
		for _, r := range rows {
			if providerWorker(r.Provider) == i.Key {
				row = r
			}
		}
		if i.Op == "team/provider.verify" {
			w.prov.mu.Lock()
			busy := w.prov.busy != "" || w.prov.routing != ""
			if !busy {
				w.prov.busy = row.Provider
			}
			w.prov.mu.Unlock()
			if busy {
				return refused("Another provider action is running; try again when it finishes."), ""
			}
			text, err := src.VerifyHarness(ctx, row.Provider, row.Model)
			w.prov.mu.Lock()
			w.prov.busy = ""
			w.prov.mu.Unlock()
			msg, isErr := w.recordVerifyNote(row.Provider, text, err)
			w.wakeProviders()
			if isErr {
				return refused(noteSummary(msg)), ""
			}
			return webboard.Result{Status: "done", Text: noteSummary(msg)}, "provider-verify"
		}
		to, ids := providerRoutePlan(rows, i.Key, w.webRunTasks(), now)
		if to == "" || len(ids) == 0 {
			return refused("No task waits to move, or no other provider is ready."), ""
		}
		w.prov.mu.Lock()
		busy := w.prov.busy != "" || w.prov.routing != ""
		if !busy {
			w.prov.routing = row.Provider
		}
		w.prov.mu.Unlock()
		if busy {
			return refused("Another provider action is running; try again when it finishes."), ""
		}
		var moved, failed []string
		for _, id := range ids {
			if _, err := src.RouteTask(ctx, id, to); err != nil {
				failed = append(failed, id+": "+err.Error())
			} else {
				moved = append(moved, id)
			}
		}
		w.prov.mu.Lock()
		w.prov.routing = ""
		w.prov.mu.Unlock()
		w.wakeProviders()
		if len(failed) > 0 {
			return refused("Route around " + i.Key + " failed for " + RedactContent(strings.Join(failed, "; "), nil)), ""
		}
		return webboard.Result{Status: "done", Text: fmt.Sprintf("Routed %s from %s to %s; /marshal resume to continue.", strings.Join(moved, ", "), i.Key, to)}, "provider-route"
	case "team/provider.signin.start":
		w.openAgySignIn(ctx)
		return webboard.Result{Status: "done", Text: "agy opened in its own terminal window: sign in there; the Board checks agy again when it closes. " + signInHint("agy") + "."}, "provider-signin-window"
	case "team/telemetry.choose":
		w.chooseTelemetry(ctx, p.Value == "keep")
		return webboard.Result{Status: "done", Text: "Usage counts choice saved: " + p.Value + "."}, "telemetry-choice"
	case "team/session.import.request":
		project := w.webProjectID()
		for _, c := range webTeamParts.candidates(ctx, w) {
			if webCandidateID(project, c) != i.Key {
				continue
			}
			switch {
			case c.Granted:
				if err := webTeamParts.load(ctx, w, c.Provider, c.Folder); err != nil {
					return refused("Earlier " + c.Provider + " work could not be loaded: " + RedactContent(err.Error(), nil)), ""
				}
				return webboard.Result{Status: "done", Text: "Loaded earlier " + c.Provider + " sessions under the read grant."}, "earlier-session-read"
			case launchEarlierWorkThroughQueue:
				webTeamParts.request(w, c.Provider, c.Folder)
				return webboard.Result{Status: "done", Text: "Asked for the read grant on earlier " + c.Provider + " sessions. Answer it in the terminal; nothing was read."}, "read-grant-request"
			}
			return refused("Earlier " + c.Provider + " sessions need the operator's stored read grant first; nothing was read."), ""
		}
		return refused("That earlier-session source is no longer offered."), ""
	case "team/architecture.open":
		if err := webTeamParts.openReview(ctx, w, i.AttemptID); err != nil {
			return refused("The accepted architecture could not be opened: " + RedactContent(err.Error(), nil)), ""
		}
		return webboard.Result{Status: "done", Text: "Opened the accepted architecture, read only, in your browser."}, "architecture-review-opened"
	}
	return refused("This team operation is unavailable."), ""
}
