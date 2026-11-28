package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// teamProviders is the Providers source of these tests.
type teamProviders struct {
	mu        sync.Mutex
	providers []store.ProviderStatus
	overviews int
	verifies  []string
	routes    []string
}

func teamProviderRows(now time.Time) []store.ProviderStatus {
	return []store.ProviderStatus{
		{Provider: "agy", Model: "gemini-3.8-flash-low", VerifiedAt: now.Add(-26 * time.Hour), ExpiresAt: now.Add(-2 * time.Hour), Expired: true},
		{Provider: "claude", Model: "claude-opus-5-5", VerifiedAt: now.Add(-4 * time.Hour), ExpiresAt: now.Add(20 * time.Hour)},
		{Provider: "codex", Model: "gpt-5.5-codex", VerifiedAt: now.Add(-6 * time.Hour), ExpiresAt: now.Add(18 * time.Hour), LastLimitAt: now.Add(-40 * time.Minute), LastLimitReason: "usage limit reached", LimitUntil: now.Add(5 * time.Hour)},
		{Provider: "opencode", Model: "deepseek", VerifiedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(22 * time.Hour)},
	}
}

func (f *teamProviders) ProviderOverview(context.Context) ([]store.ProviderStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overviews++
	return f.providers, nil
}

func (f *teamProviders) VerifyHarness(_ context.Context, provider, model string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verifies = append(f.verifies, provider+" "+model)
	return "Verified " + provider + " 1.0 in sandbox through proxy; evidence ev-1", nil
}

func (f *teamProviders) RouteTask(_ context.Context, task, worker string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes = append(f.routes, task+" "+worker)
	return "Task " + task + " reassigned to " + worker, nil
}

func (f *teamProviders) calls() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.overviews, append([]string(nil), f.verifies...)
}

func (f *teamProviders) routed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.routes...)
}

func teamCapability(w *Workspace, op, id string) (webboard.SettingCapability, bool) {
	for _, c := range w.webTeamCapabilities(w.workers.context()) {
		if c.Op == op && (id == "" || c.Target.ID == id) {
			return c, true
		}
	}
	return webboard.SettingCapability{}, false
}

func mustTeamCapability(t *testing.T, w *Workspace, op, id string) webboard.SettingCapability {
	t.Helper()
	c, ok := teamCapability(w, op, id)
	if !ok {
		t.Fatalf("missing %s %s in %+v", op, id, w.webTeamCapabilities(t.Context()))
	}
	return c
}

func teamReceipt(t *testing.T, w *Workspace, a webboard.Action) store.WebReceipt {
	return runReceipt(t, w, a)
}

// teamWorkspace is a workspace with a store, the fake provider source and
// a run whose tasks sit on codex, which is in a limit.
func teamWorkspace(t *testing.T, session string) (*Workspace, *teamProviders) {
	t.Helper()
	w, _ := receiptTestWorkspace(t, session)
	f := &teamProviders{providers: teamProviderRows(time.Now())}
	w.provSource = f
	run := marshal.Run{Tasks: []marshal.Task{
		{PlanTaskID: "T-023", Worker: "codex", State: marshal.Escalated},
		{PlanTaskID: "T-033", Worker: "codex", State: marshal.Queued},
		{PlanTaskID: "T-040", Worker: "claude", State: marshal.Dispatched},
	}}
	w.mu.Lock()
	w.state.Marshal = newMarshalPanel("R-1", "codex", run, "")
	w.mu.Unlock()
	return w, f
}

func TestWebReadinessActionsHonorProviderAndEdition(t *testing.T) {
	w, f := teamWorkspace(t, "team-readiness")
	for _, name := range []string{"agy", "claude", "opencode"} {
		mustTeamCapability(t, w, "team/provider.verify", name)
	}
	if _, ok := teamCapability(w, "team/provider.verify", "codex"); ok {
		t.Fatal("verify offered for a provider in its limit window")
	}
	// Only the provider that has tasks to move and another ready provider
	// is offered a route; claude's task is already running.
	mustTeamCapability(t, w, "team/provider.route-around", "codex")
	for _, name := range []string{"agy", "claude", "opencode"} {
		if _, ok := teamCapability(w, "team/provider.route-around", name); ok {
			t.Fatalf("route-around offered for %s", name)
		}
	}
	// The sign-in window follows the readiness checklist's own button.
	if _, ok := teamCapability(w, "team/provider.signin.start", ""); ok {
		t.Fatal("sign-in offered when the checklist does not offer it")
	}
	s := w.v2ReadyState()
	s.mu.Lock()
	s.offered = true
	s.mu.Unlock()
	mustTeamCapability(t, w, "team/provider.signin.start", "agy")
	if _, ok := teamCapability(w, "team/provider.signin.start", "claude"); ok {
		t.Fatal("sign-in offered for a provider with no sign-in window")
	}

	// Verify runs the harness verify of the provider the capability named.
	verify := runTestAction(t, w, mustTeamCapability(t, w, "team/provider.verify", "agy"), "30112233445566778899aabbccddeeff", webboard.SettingPayload{Provider: "agy"})
	if r := w.webTeamCommand(verify); r.Status != "done" {
		t.Fatalf("verify: %+v", r)
	}
	if _, v := f.calls(); len(v) != 1 || !strings.HasPrefix(v[0], "agy ") {
		t.Fatalf("verified %v", v)
	}
	if r := teamReceipt(t, w, verify); r.Status != "applied" || !slices.Contains(r.Effects, "provider-verify") {
		t.Fatalf("verify receipt %+v", r)
	}

	// Route-around moves the tasks the edition's routing takes: a queued
	// task only where the edition lets the owner move it.
	route := runTestAction(t, w, mustTeamCapability(t, w, "team/provider.route-around", "codex"), "31112233445566778899aabbccddeeff", webboard.SettingPayload{Provider: "codex"})
	if r := w.webTeamCommand(route); r.Status != "done" {
		t.Fatalf("route: %+v", r)
	}
	want := []string{"T-023 claude"}
	if editionExtreme {
		want = append(want, "T-033 claude")
	}
	if got := f.routed(); !slices.Equal(got, want) {
		t.Fatalf("routed %v, want %v", got, want)
	}

	// An action offered for an earlier state is not run.
	stale := mustTeamCapability(t, w, "team/provider.verify", "claude")
	stale.AttemptID = "0000000000000000"
	_, before := f.calls()
	if r := w.webTeamCommand(runTestAction(t, w, stale, "32112233445566778899aabbccddeeff", webboard.SettingPayload{Provider: "claude"})); r.Status != "refused" {
		t.Fatalf("stale verify: %+v", r)
	}
	if _, after := f.calls(); len(after) != len(before) {
		t.Fatal("a stale verify read the providers")
	}
}

func TestWebSessionImportUsesAuthorizedCandidate(t *testing.T) {
	w, _ := teamWorkspace(t, "team-import")
	orig := webTeamParts
	t.Cleanup(func() { webTeamParts = orig })
	var mu sync.Mutex
	var loaded, asked []string
	candidates := []webImportCandidate{{"claude", "/earlier/claude-folder", true}, {"codex", "/earlier/codex-folder", false}}
	webTeamParts.candidates = func(context.Context, *Workspace) []webImportCandidate { return candidates }
	webTeamParts.load = func(_ context.Context, _ *Workspace, provider, folder string) error {
		mu.Lock()
		defer mu.Unlock()
		loaded = append(loaded, provider+" "+folder)
		return nil
	}
	webTeamParts.request = func(_ *Workspace, provider, folder string) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, provider+" "+folder)
	}
	caps := w.webTeamCapabilities(t.Context())
	var imports []webboard.SettingCapability
	for _, c := range caps {
		if c.Op == "team/session.import.request" {
			imports = append(imports, c)
		}
	}
	if len(imports) != 2 {
		t.Fatalf("import candidates: %+v", imports)
	}
	for _, c := range imports {
		if strings.ContainsAny(c.Target.ID, "/\\") || strings.Contains(c.Target.ID, "earlier") || len(c.Target.ID) != 16 {
			t.Fatalf("candidate id %q is not opaque", c.Target.ID)
		}
	}
	// The browser names the id; the Board resolves the folder itself.
	granted := runTestAction(t, w, imports[0], "40112233445566778899aabbccddeeff", webboard.SettingPayload{})
	if r := w.webTeamCommand(granted); r.Status != "done" {
		t.Fatalf("granted import: %+v", r)
	}
	// A stored grant is read directly where reads are not queued, and goes
	// through the owner's permission queue where they are.
	if launchEarlierWorkThroughQueue {
		if len(loaded) != 0 || !slices.Equal(asked, []string{"claude /earlier/claude-folder"}) {
			t.Fatalf("granted: loaded %v asked %v", loaded, asked)
		}
	} else if !slices.Equal(loaded, []string{"claude /earlier/claude-folder"}) || len(asked) != 0 {
		t.Fatalf("granted: loaded %v asked %v", loaded, asked)
	}
	loadedBefore, askedBefore := len(loaded), len(asked)
	ungranted := runTestAction(t, w, imports[1], "41112233445566778899aabbccddeeff", webboard.SettingPayload{})
	r := w.webTeamCommand(ungranted)
	if len(loaded) != loadedBefore {
		t.Fatalf("an import without a grant read the folder: %v", loaded)
	}
	// Without a grant the queueing edition leaves the owner's denial alone;
	// the other asks for the read.
	if launchEarlierWorkThroughQueue {
		if r.Status != "refused" || len(asked) != askedBefore {
			t.Fatalf("ungranted: %+v asked %v", r, asked)
		}
	} else if r.Status != "done" || !slices.Equal(asked, []string{"codex /earlier/codex-folder"}) {
		t.Fatalf("ungranted: %+v asked %v", r, asked)
	}
	for _, text := range []string{r.Text, teamReceipt(t, w, ungranted).Result} {
		if strings.Contains(text, "/earlier/") {
			t.Fatalf("an answer shows the folder: %q", text)
		}
	}
	// A candidate that is no longer offered is not imported.
	candidates = candidates[1:]
	again := runTestAction(t, w, imports[0], "42112233445566778899aabbccddeeff", webboard.SettingPayload{})
	if r := w.webTeamCommand(again); r.Status != "refused" || len(loaded) != loadedBefore {
		t.Fatalf("withdrawn candidate: %+v loaded %v", r, loaded)
	}
}

func TestFirstRunMarshalModelEffortAndTelemetry(t *testing.T) {
	// The Marshal's line in the checklist names its model and effort.
	in := v2MarshalIn{Now: time.Now(), Probed: true,
		Chat:      v2Chat{Live: true, Provider: "codex", History: "x", Args: []string{"-c", `model_reasoning_effort="high"`}},
		Providers: map[string]store.ProviderStatus{"codex": {Provider: "codex", Model: "gpt-5.5-codex", VerifiedAt: time.Now()}},
		SignedIn:  map[string]bool{"codex": true}, Installed: map[string]bool{"codex": true},
		Probe: v2Probe{Installed: map[string]bool{"codex": true}, TelemetryOn: true}}
	ready := buildV2Ready(in)
	if m := buildV2Marshal(in); m.Provider != "codex" || m.Effort != "high" || len(ready.Checks) == 0 || !strings.Contains(ready.Checks[0].Text, "Final check needs Claude Code") || !ready.TelemetryAsk {
		t.Fatalf("first-run checklist: %+v", ready)
	}

	w, _ := teamWorkspace(t, "team-first-run")
	if _, ok := teamCapability(w, "team/telemetry.choose", ""); ok {
		t.Fatal("telemetry asked before the first probe")
	}
	w.v2m.mu.Lock()
	w.v2m.at, w.v2m.probe.TelemetryOn, w.v2m.probe.Told = time.Now(), true, false
	w.v2m.mu.Unlock()
	origParts := v2TelemetryParts
	t.Cleanup(func() { v2TelemetryParts = origParts })
	var mu sync.Mutex
	var saved []string
	v2TelemetryParts.disable = func(context.Context, string) error {
		mu.Lock()
		defer mu.Unlock()
		saved = append(saved, "off")
		return nil
	}
	v2TelemetryParts.keep = func(context.Context, string) error {
		mu.Lock()
		defer mu.Unlock()
		saved = append(saved, "keep")
		return nil
	}
	c := mustTeamCapability(t, w, "team/telemetry.choose", "telemetry")
	if !slices.Equal(c.Values, []string{"keep", "off"}) {
		t.Fatalf("telemetry values %v", c.Values)
	}
	a := runTestAction(t, w, c, "50112233445566778899aabbccddeeff", webboard.SettingPayload{Value: "off"})
	if r := w.webTeamCommand(a); r.Status != "done" {
		t.Fatalf("telemetry: %+v", r)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := slices.Equal(saved, []string{"off"})
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	if !slices.Equal(saved, []string{"off"}) {
		t.Fatalf("saved choice %v", saved)
	}
	mu.Unlock()
	if r := teamReceipt(t, w, a); r.Status != "applied" || !slices.Contains(r.Effects, "telemetry-choice") {
		t.Fatalf("receipt %+v", r)
	}
	// The readiness card no longer asks, and the capability is gone.
	w.v2m.mu.Lock()
	told, on := w.v2m.probe.Told, w.v2m.probe.TelemetryOn
	w.v2m.mu.Unlock()
	if !told || on {
		t.Fatalf("readiness still asks: told %v on %v", told, on)
	}
	if _, ok := teamCapability(w, "team/telemetry.choose", ""); ok {
		t.Fatal("telemetry asked again after the choice")
	}
}

func TestBoardLinkRecoveryRequiresOwner(t *testing.T) {
	webBoardSeams(t)
	origGOOS := v2GOOS
	t.Cleanup(func() { v2GOOS = origGOOS })
	v2GOOS = "darwin"
	w := emptyBoardWorkspace(t)
	w.endOpening()
	defer w.Close()
	if _, err := w.marshalWeb(); err != nil {
		t.Fatal(err)
	}
	srv, err := w.webBoardServer()
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	base := "http://" + srv.Addr()
	recovery := regexp.MustCompile(`(?i)press w .*\/marshal web`)
	try := func(method, path, bearer string) string {
		t.Helper()
		q, _ := http.NewRequest(method, base+path, strings.NewReader("{}"))
		q.Header.Set("Origin", base)
		if bearer != "" {
			q.Header.Set("Authorization", "Bearer "+bearer)
		}
		r, err := client.Do(q)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		text, _ := io.ReadAll(r.Body)
		if r.StatusCode != http.StatusUnauthorized || strings.Contains(string(text), `"session"`) {
			t.Fatalf("%s %s with %q: %d %s", method, path, bearer, r.StatusCode, text)
		}
		return string(text)
	}
	// Nothing without an owner's link mints another: every refusal tells
	// the owner where a new one-time link comes from.
	for _, bearer := range []string{"", "not-a-link", strings.Repeat("a", 43)} {
		for _, p := range []struct{ method, path string }{{"GET", "/api/board"}, {"POST", "/api/act"}, {"GET", "/api/events"}, {"POST", "/api/session"}} {
			if text := try(p.method, p.path, bearer); !recovery.MatchString(text) && p.path != "/api/session" {
				t.Fatalf("%s %s gives no recovery: %s", p.method, p.path, text)
			}
		}
	}
	// A spent link tells the owner the same, and replaying it opens nothing.
	link, err := srv.Link()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link)
	q, _ := http.NewRequest("POST", base+"/api/session", nil)
	q.Header.Set("Origin", base)
	q.Header.Set("Authorization", "Bearer "+u.Query().Get("t"))
	r, err := client.Do(q)
	if err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("spend: %v %v", r, err)
	}
	r.Body.Close()
	if text := try("POST", "/api/session", u.Query().Get("t")); !recovery.MatchString(text) {
		t.Fatalf("a spent link gives no recovery: %s", text)
	}
	// The owner's own w (or /marshal web) is what mints a fresh link.
	if _, err := srv.Link(); err != nil {
		t.Fatal(err)
	}
}

func TestArchitectureCardOpensCurrentPrivateReview(t *testing.T) {
	w, _ := teamWorkspace(t, "team-architecture")
	orig := webTeamParts
	t.Cleanup(func() { webTeamParts = orig })
	var mu sync.Mutex
	revision, digest := int64(3), "digest-a"
	var opened []string
	webTeamParts.accepted = func(context.Context, *Workspace) (int64, string) {
		mu.Lock()
		defer mu.Unlock()
		return revision, digest
	}
	webTeamParts.openReview = func(_ context.Context, _ *Workspace, want string) error {
		mu.Lock()
		defer mu.Unlock()
		opened = append(opened, want)
		return nil
	}
	// A pending review's private link must not reach this route.
	m := w.marshalSession()
	m.mu.Lock()
	m.architecture = &app.ArchitectureReviewSession{URL: "http://127.0.0.1:1/?token=pending-owner-secret"}
	m.mu.Unlock()

	c := mustTeamCapability(t, w, "team/architecture.open", "architecture")
	if c.Revision != 3 || c.AttemptID != "digest-a" {
		t.Fatalf("capability %+v", c)
	}
	a := runTestAction(t, w, c, "60112233445566778899aabbccddeeff", webboard.SettingPayload{})
	r := w.webTeamCommand(a)
	if r.Status != "done" || r.Link != "" || !slices.Equal(opened, []string{"digest-a"}) {
		t.Fatalf("open: %+v opened %v", r, opened)
	}
	rec := teamReceipt(t, w, a)
	raw, _ := json.Marshal([]any{r, rec, webboard.FromBoard(w.webBoardFrame())})
	for _, bad := range []string{"pending-owner-secret", "token=", "http://"} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("%q reached an answer, receipt or frame: %s", bad, raw)
		}
	}
	if rec.Status != "applied" || !slices.Contains(rec.Effects, "architecture-review-opened") {
		t.Fatalf("receipt %+v", rec)
	}

	// A newer accepted revision is not the one the card showed.
	mu.Lock()
	revision, digest = 4, "digest-b"
	mu.Unlock()
	stale := runTestAction(t, w, c, "61112233445566778899aabbccddeeff", webboard.SettingPayload{})
	if r := w.webTeamCommand(stale); r.Status != "refused" || len(opened) != 1 {
		t.Fatalf("stale open: %+v opened %v", r, opened)
	}
	if now := mustTeamCapability(t, w, "team/architecture.open", "architecture"); now.AttemptID != "digest-b" {
		t.Fatalf("current capability %+v", now)
	}
	// With nothing accepted there is nothing to open.
	mu.Lock()
	revision, digest = 0, ""
	mu.Unlock()
	if _, ok := teamCapability(w, "team/architecture.open", ""); ok {
		t.Fatal("architecture offered with nothing accepted")
	}
}
