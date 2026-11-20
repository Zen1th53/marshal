//go:build linux

package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
	"github.com/Zen1th53/marshal/internal/usage"
)

// v2Fixture is the readiness screen's machine: claude runs the Marshal
// (live chat, never verified), agy is installed but not signed in, codex is
// signed in with 11% of its week left, opencode is not installed, and there
// is no display.
func v2Fixture(t *testing.T, now time.Time) *Workspace {
	t.Helper()
	w := emptyBoardWorkspace(t)
	history := filepath.Join(t.TempDir(), "chat.jsonl")
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", provider: "claude", state: "working", historyPath: history}
	w.v2m.probe = v2Probe{
		Lead:      "claude",
		Installed: map[string]bool{"claude": true, "agy": true, "codex": true, "opencode": false},
		Settings:  marshal.DefaultSettings(), HasSettings: true,
		Repo:       v2Repo{OK: true, Branch: "main", Head: "abb617e"},
		PlatformOK: true, Platform: "Linux amd64",
		Browser: false, BrowserWhy: "no display",
		TelemetryOn: true, Told: false,
		Session:    usage.Session{Model: "claude-opus-5-5", Effort: "medium", ContextUsed: 82_000},
		HasSession: true,
	}
	w.v2m.at = now
	w.v2m.key = "claude\x00" + history
	w.noteV2Verify("agy", errors.New("harness probe did not produce the exact reply: exit code 1"))
	reset := now.Add(36 * time.Hour)
	w.usage.quotas = []usage.Quota{
		{Provider: "claude", Known: true, AsOf: now, Windows: []usage.Window{{Label: "5h", LeftPercent: 58, ResetsAt: now.Add(2 * time.Hour)}, {Label: "wk", LeftPercent: 71, ResetsAt: reset}}},
		{Provider: "codex", Known: true, AsOf: now, Windows: []usage.Window{{Label: "5h", LeftPercent: 90, ResetsAt: now.Add(time.Hour)}, {Label: "wk", LeftPercent: 11, ResetsAt: reset}}},
	}
	w.usage.read = true
	w.launch.scan.Sessions = []scanSessions{{Provider: "codex", Found: 2}}
	return w
}

func v2Worker(t *testing.T, b vm.Board, name string) vm.Worker {
	t.Helper()
	for _, r := range b.Workers {
		if r.Provider == name {
			return r
		}
	}
	t.Fatalf("no Workers row for %s: %+v", name, b.Workers)
	return vm.Worker{}
}

func v2Check(r vm.Readiness, label, text string) (vm.Check, bool) {
	for _, c := range r.Checks {
		if c.Label == label && strings.Contains(c.Text, text) {
			return c, true
		}
	}
	return vm.Check{}, false
}

func TestV2MarshalClaudeInUse(t *testing.T) {
	now := time.Now()
	w := v2Fixture(t, now)
	var b vm.Board
	b.Now = now
	w.collectV2Marshal(&b)
	m := b.Marshal
	if m.Provider != "claude" || m.Model != "Opus 5.5" || m.Effort != "medium" || !m.InUse {
		t.Fatalf("Marshal %+v", m)
	}
	// Claude Code writes the tokens in context but not the window.
	if m.ContextUsed != 82_000 || m.ContextWindow != 0 || m.ContextPct != -1 {
		t.Fatalf("context %+v", m)
	}
	if m.Limit5hPct != 58 || m.WeeklyPct != 71 {
		t.Fatalf("limits 5h %d wk %d", m.Limit5hPct, m.WeeklyPct)
	}
	if m.Doing != "ready" {
		t.Fatalf("doing %q", m.Doing)
	}
	// U5: never verified, but the live Marshal session proves it works.
	c := v2Worker(t, b, "claude")
	if c.Tag != "in use" || !c.SignedIn || !c.Installed || c.LeftPct != 58 {
		t.Fatalf("claude row %+v", c)
	}
	if chk, ok := v2Check(b.Ready, "Marshal", "claude · Opus 5.5 · signed in"); !ok || chk.Mark != "ok" || chk.Tag != "in use" {
		t.Fatalf("Marshal check %+v in %+v", chk, b.Ready.Checks)
	}
}

func TestV2MarshalCodexContextWindow(t *testing.T) {
	now := time.Now()
	in := v2MarshalIn{Now: now, Probed: true, Chat: v2Chat{Live: true, Provider: "codex", History: "x", Args: []string{"-c", `model_reasoning_effort="high"`}},
		Probe: v2Probe{Lead: "codex", Installed: map[string]bool{"codex": true}, HasSession: true, Session: usage.Session{Model: "gpt-6.1-sol", ContextUsed: 64_600, ContextWindow: 258_400}}}
	m := buildV2Marshal(in)
	if m.Model != "GPT-6.1-Sol" || m.Effort != "high" || m.ContextPct != 25 || m.ContextWindow != 258_400 {
		t.Fatalf("Marshal %+v", m)
	}
	if m.Limit5hPct != -1 || m.WeeklyPct != -1 {
		t.Fatalf("unknown limits read as %d/%d", m.Limit5hPct, m.WeeklyPct)
	}
}

func TestV2WorkersAgyNotSignedIn(t *testing.T) {
	now := time.Now()
	w := v2Fixture(t, now)
	var b vm.Board
	b.Now = now
	w.collectV2Marshal(&b)
	a := v2Worker(t, b, "agy")
	want := "agy is not signed in: run agy once in a terminal and sign in"
	if a.Tag != "not signed in" || a.SignedIn || !a.Installed || a.Reason != want || a.TagTone != vm.ToneWarn {
		t.Fatalf("agy row %+v", a)
	}
	chk, ok := v2Check(b.Ready, "Workers", "agy is installed but not signed in")
	if !ok || chk.Mark != "warn" || chk.Tag != "action" || !strings.Contains(chk.Hint, "run agy once") {
		t.Fatalf("agy check %+v in %+v", chk, b.Ready.Checks)
	}
	// A verify that then succeeds clears it.
	w.noteV2Verify("agy", nil)
	b = vm.Board{Now: now}
	w.collectV2Marshal(&b)
	if a := v2Worker(t, b, "agy"); a.Tag == "not signed in" {
		t.Fatalf("a passing verify left agy %+v", a)
	}
}

func TestV2WorkersCodexLowOpencodeMissingNoDisplay(t *testing.T) {
	now := time.Now()
	w := v2Fixture(t, now)
	var b vm.Board
	b.Now = now
	w.collectV2Marshal(&b)
	c := v2Worker(t, b, "codex")
	if c.LeftPct != 11 || c.Detail != "wk 11%" || c.Tag != "ready" || c.TagTone != vm.ToneWarn || !c.SignedIn {
		t.Fatalf("codex row %+v", c)
	}
	if chk, ok := v2Check(b.Ready, "Workers", "codex signed in · weekly 11% · resets"); !ok || chk.Tag != "low" || chk.Mark != "warn" {
		t.Fatalf("codex check %+v in %+v", chk, b.Ready.Checks)
	}
	o := v2Worker(t, b, "opencode")
	if o.Tag != "not installed" || o.Installed || !strings.Contains(o.Reason, "not installed") {
		t.Fatalf("opencode row %+v", o)
	}
	if _, ok := v2Check(b.Ready, "Workers", "opencode"); ok {
		t.Fatal("a provider that is not installed has a Workers check")
	}
	if chk, ok := v2Check(b.Ready, "Browser", "no display: links are shown on the Board"); !ok || chk.Mark != "ok" {
		t.Fatalf("browser check %+v", chk)
	}
	if chk, ok := v2Check(b.Ready, "Repo", "clean · main @ abb617e · 2 earlier Codex sessions"); !ok || chk.Mark != "ok" {
		t.Fatalf("repo check %+v in %+v", chk, b.Ready.Checks)
	}
	if _, ok := v2Check(b.Ready, "Platform", "Linux amd64 · workers can run here"); !ok {
		t.Fatalf("platform check missing: %+v", b.Ready.Checks)
	}
	if b.Ready.Total != len(b.Ready.Checks) || b.Ready.Ready >= b.Ready.Total {
		t.Fatalf("counts %d of %d", b.Ready.Ready, b.Ready.Total)
	}
	if !b.Ready.TelemetryOn || !b.Ready.TelemetryAsk {
		t.Fatalf("telemetry on %v ask %v", b.Ready.TelemetryOn, b.Ready.TelemetryAsk)
	}
	keys := map[string]string{}
	for _, d := range b.Ready.Defaults {
		keys[d[0]] = d[1]
	}
	if keys["review"] != "other provider" || !strings.HasPrefix(keys["approval"], "you: architecture") || keys["fallback"] == "" || strings.Contains(keys["fallback"], "opencode") {
		t.Fatalf("defaults %+v", b.Ready.Defaults)
	}
}

func TestV2WorkersLanesAndSpareDuringARun(t *testing.T) {
	now := time.Now()
	in := v2MarshalIn{Now: now, Probed: true, Run: true, RunState: marshal.Dispatching,
		Chat:      v2Chat{Live: true, Provider: "claude"},
		Probe:     v2Probe{Lead: "claude", Installed: map[string]bool{"claude": true, "agy": true, "codex": true}},
		Lanes:     map[string]int{"claude": 4, "agy": 4},
		SignedIn:  map[string]bool{"codex": true, "agy": true},
		Providers: map[string]store.ProviderStatus{"agy": {Provider: "agy", Model: "gemini-3.8", VerifiedAt: now}},
	}
	rows := buildV2Workers(in)
	if rows[0].Tag != "4 lanes" || rows[1].Tag != "4 lanes" || rows[1].Detail != "gemini-3.8" || rows[2].Tag != "spare" {
		t.Fatalf("rows %+v", rows)
	}
	if d := buildV2Marshal(in).Doing; d != "watching the workers" {
		t.Fatalf("doing %q", d)
	}
}

func TestV2MarshalNothingKnownYet(t *testing.T) {
	m := buildV2Marshal(v2MarshalIn{Now: time.Now()})
	if m.Provider != "" || m.ContextPct != -1 || m.Limit5hPct != -1 || m.WeeklyPct != -1 {
		t.Fatalf("Marshal %+v", m)
	}
	rows := buildV2Workers(v2MarshalIn{Now: time.Now()})
	for _, r := range rows {
		if r.Tag != "checking" || r.LeftPct != -1 {
			t.Fatalf("row before any lookup %+v", r)
		}
	}
	if r := buildV2Ready(v2MarshalIn{Now: time.Now()}); len(r.Checks) != 0 || r.Defaults != nil {
		t.Fatalf("checklist before the first probe %+v", r)
	}
}

// The probe reads the transcript of the live chat and every part through its
// seams; the collector then paints from it without probing again.
func TestV2MarshalProbeReadsTheChatTranscript(t *testing.T) {
	w := emptyBoardWorkspace(t)
	history := filepath.Join(t.TempDir(), "chat.jsonl")
	line := `{"type":"assistant","isSidechain":false,"effort":"high","message":{"model":"claude-opus-5-5","usage":{"input_tokens":2,"cache_creation_input_tokens":800,"cache_read_input_tokens":40000,"output_tokens":200}}}` + "\n"
	if err := os.WriteFile(history, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := v2ProbeParts
	t.Cleanup(func() { v2ProbeParts = saved })
	asked := false
	v2ProbeParts.lead = func(string) string { return "claude" }
	v2ProbeParts.lookPath = func(name string) (string, error) {
		if name == "opencode" {
			return "", errors.New("not found")
		}
		return "/bin/" + name, nil
	}
	v2ProbeParts.repo = func(context.Context, string) v2Repo {
		return v2Repo{OK: true, Branch: "main", Head: "abc1234", Changed: 2}
	}
	v2ProbeParts.platform = func() (bool, string, string) { return false, "macOS arm64", "socket confinement requires Linux amd64" }
	v2ProbeParts.browser = func() (bool, string) { return true, "" }
	v2ProbeParts.telemetry = func(string) (bool, bool) { return true, true }
	v2ProbeParts.current = func(context.Context, string) (string, bool) { asked = true; return "", false }
	chat := v2Chat{Live: true, Provider: "claude", History: history}
	w.refreshV2Marshal(context.Background(), v2ProbeInput{Chat: chat, Probing: true})
	if asked {
		t.Fatal("the provider CLI was asked for its model while the chat runs")
	}
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{role: "marshal-chat", provider: "claude", state: "working", historyPath: history}
	b := vm.Board{Now: time.Now()}
	w.collectV2Marshal(&b)
	if b.Marshal.Model != "Opus 5.5" || b.Marshal.Effort != "high" || b.Marshal.ContextUsed != 41002 {
		t.Fatalf("Marshal %+v", b.Marshal)
	}
	if chk, ok := v2Check(b.Ready, "Platform", "macOS arm64 · workers cannot run here"); !ok || chk.Mark != "bad" || !strings.Contains(chk.Hint, "Linux amd64") {
		t.Fatalf("platform %+v", b.Ready.Checks)
	}
	if chk, ok := v2Check(b.Ready, "Repo", "2 changed · main @ abc1234"); !ok || chk.Mark != "warn" {
		t.Fatalf("repo %+v", b.Ready.Checks)
	}
	if b.Ready.TelemetryAsk {
		t.Fatal("telemetry asked again after the owner was told")
	}
	w.v2m.mu.Lock()
	running := w.v2m.running
	w.v2m.mu.Unlock()
	if running {
		t.Fatal("a fresh probe for the same chat was started again")
	}
}

// U4: a verify of agy that ends at once is a missing sign-in, said plainly.
func TestVerifyNoteSaysAgyIsNotSignedIn(t *testing.T) {
	for _, tc := range []struct {
		provider string
		err      string
		signed   bool
	}{
		{"agy", "harness probe did not produce the exact reply: exit code 1", true},
		{"antigravity", `harness probe did not produce the exact reply: reply was ""`, true},
		{"agy", "native agy probe did not produce the exact successful reply", true},
		{"agy", "native agy probe did not produce the exact successful reply: stderr: stub verify error", false},
		{"agy", "harness probe did not produce the exact reply: exit code 1: stderr: segmentation fault", false},
		{"agy", "harness probe did not produce the exact reply: exit code 1: provider limit: quota", false},
		{"agy", "harness probe did not produce the exact reply: run timed out", false},
		{"agy", "harness probe did not produce the exact reply: exit code 1: stderr: Please sign in to continue", true},
		{"codex", "harness probe did not produce the exact reply: exit code 1", false},
		{"claude", "harness probe did not produce the exact reply: exit code 1: stderr: Not logged in", true},
	} {
		if got := verifySignedOut(tc.provider, errors.New(tc.err)); got != tc.signed {
			t.Errorf("%s %q: signed out %v, want %v", tc.provider, tc.err, got, tc.signed)
		}
	}
	msg, isErr := verifyActivityNote("agy", "", errors.New("harness probe did not produce the exact reply: exit code 1"))
	if !isErr || msg != "agy is not signed in: run agy once in a terminal and sign in" {
		t.Fatalf("note %q", msg)
	}
	msg, _ = verifyActivityNote("codex", "", errors.New("harness probe did not produce the exact reply: exit code 1"))
	if !strings.HasPrefix(msg, "Verify codex failed: ") {
		t.Fatalf("an unrelated failure was reworded: %q", msg)
	}
}

// U5: the first-use card shows the provider the Marshal runs on as in use,
// and v does not pick it to verify.
func TestFirstUseChipMarshalProviderInUse(t *testing.T) {
	w := emptyBoardWorkspace(t)
	w.firstUse.installed = map[string]bool{"codex": true, "claude": true, "agy": true, "opencode": true}
	w.prov.snap.Loaded = true
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{role: "marshal-chat", provider: "claude", state: "working"}
	chips := w.firstUseChips(time.Now())
	for _, c := range chips {
		if c.name == "claude" && c.state != "in use" {
			t.Fatalf("claude chip %+v", c)
		}
	}
	if next := nextUnverified(chips, "claude"); next == "claude" {
		t.Fatal("v picks the live Marshal provider to verify")
	}
}
