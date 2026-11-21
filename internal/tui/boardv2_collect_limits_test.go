package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
	"github.com/Zen1th53/marshal/internal/usage"
)

var cet = time.FixedZone("CET", 3600)

// limitsLiveNow is 19:37 on the live workstation run.
var limitsLiveNow = time.Date(2026, 11, 21, 19, 37, 0, 0, cet)

// liveQuotas is the reading behind the live Activity line "Limits: codex ~8%
// weekly (resets 21:52), ~98% 5h · agy Claude and GPT 0% weekly (resets 26
// Nov), Gemini 4 Argon 99% weekly · claude limit unknown".
func liveQuotas() map[string]usage.Quota {
	asOf := limitsLiveNow.Add(-2 * time.Minute)
	return map[string]usage.Quota{
		"codex": {Provider: "codex", Known: true, AsOf: asOf, Windows: []usage.Window{
			{Label: "5h", LeftPercent: 98, ResetsAt: time.Date(2026, 11, 21, 23, 10, 0, 0, cet)},
			{Label: "wk", LeftPercent: 8, ResetsAt: time.Date(2026, 11, 21, 21, 52, 0, 0, cet)},
		}},
		"agy": {Provider: "agy", Known: true, AsOf: asOf, Windows: []usage.Window{
			{Label: "Gemini wk", LeftPercent: 100, ResetsAt: time.Date(2026, 11, 26, 4, 51, 0, 0, time.UTC)},
			{Label: "Gemini 5h", LeftPercent: 100, ResetsAt: time.Date(2026, 11, 21, 22, 51, 0, 0, time.UTC)},
			{Label: "Claude and GPT wk", LeftPercent: 0, ResetsAt: time.Date(2026, 11, 26, 9, 32, 0, 0, cet)},
			{Label: "Claude and GPT 5h", LeftPercent: 40, ResetsAt: time.Date(2026, 11, 21, 21, 0, 0, 0, time.UTC)},
			{Label: "Gemini 4 Argon wk", LeftPercent: 99, ResetsAt: time.Date(2026, 11, 24, 23, 48, 0, 0, time.UTC)},
		}},
		"claude":   {Provider: "claude", Reason: usage.ReasonNotReported},
		"opencode": usage.Unexposed("opencode"),
	}
}

func liveLimitsIn(agyModel string) v2MarshalIn {
	verified := limitsLiveNow.Add(-time.Hour)
	return v2MarshalIn{
		Now:       limitsLiveNow,
		Quotas:    liveQuotas(),
		Installed: map[string]bool{"claude": true, "agy": true, "codex": true, "opencode": true},
		Providers: map[string]store.ProviderStatus{
			"agy":    {Provider: "agy", Model: agyModel, VerifiedAt: verified},
			"codex":  {Provider: "codex", Model: "gpt-5.5-codex", VerifiedAt: verified},
			"claude": {Provider: "claude", Model: "claude-opus-5-5", VerifiedAt: verified},
		},
	}
}

func limitsFor(t *testing.T, ls []vm.ProviderLimits, provider string) vm.ProviderLimits {
	t.Helper()
	for _, l := range ls {
		if l.Provider == provider {
			return l
		}
	}
	t.Fatalf("no limits for %s in %+v", provider, ls)
	return vm.ProviderLimits{}
}

func TestResetClockSaysTheDateOffToday(t *testing.T) {
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 11, 21, 21, 52, 0, 0, cet), "21:52"},
		{time.Date(2026, 11, 21, 20, 52, 0, 0, time.UTC), "21:52"}, // read in the owner's zone
		{time.Date(2026, 11, 22, 9, 32, 0, 0, cet), "Nov 22 09:32"},
		{time.Date(2026, 11, 26, 9, 32, 0, 0, cet), "Nov 26 09:32"},
		{time.Date(2026, 11, 21, 23, 30, 0, 0, time.UTC), "Nov 22 00:30"}, // under a day away, but tomorrow
	} {
		if got := resetClock(c.at, limitsLiveNow); got != c.want {
			t.Errorf("resetClock(%v) = %q, want %q", c.at, got, c.want)
		}
	}
}

func TestLimitRowsCodexFiveHourAndWeekly(t *testing.T) {
	l := limitsFor(t, buildV2Limits(liveLimitsIn("gemini-4-argon-high")), "codex")
	if l.State != vm.LimitUsable || len(l.Rows) != 2 {
		t.Fatalf("codex = %+v", l)
	}
	five, week := l.Rows[0], l.Rows[1]
	if five.Family != "" || five.Window != "5h" || five.PercentLeft != 98 || five.Source != "codex session log" || five.Drives {
		t.Fatalf("5h row = %+v", five)
	}
	if week.Window != "weekly" || week.PercentLeft != 8 || !week.ResetsAt.Equal(time.Date(2026, 11, 21, 21, 52, 0, 0, cet)) || !week.Drives {
		t.Fatalf("weekly row = %+v; the tighter window drives", week)
	}
	if !week.ObservedAt.Equal(limitsLiveNow.Add(-2 * time.Minute)) {
		t.Fatalf("observed at %v", week.ObservedAt)
	}
}

// agy's Claude and GPT family is spent; its workers run Gemini 4 Argon,
// which has 99% of its week left. agy is usable, and the Workers row no
// longer says "at limit · 0% left".
func TestLimitRowsAgyPerFamily(t *testing.T) {
	in := liveLimitsIn("gemini-4-argon-high")
	l := limitsFor(t, buildV2Limits(in), "agy")
	if l.State != vm.LimitUsable || l.Model != "gemini-4-argon-high" || len(l.Rows) != 5 {
		t.Fatalf("agy = %+v", l)
	}
	byKey := map[string]vm.LimitRow{}
	for _, r := range l.Rows {
		byKey[r.Family+"|"+r.Window] = r
		if r.Source != "agy /usage" {
			t.Errorf("row source %q", r.Source)
		}
	}
	spent := byKey["Claude and GPT|weekly"]
	if spent.PercentLeft != 0 || spent.Drives || !spent.ResetsAt.Equal(time.Date(2026, 11, 26, 9, 32, 0, 0, cet)) {
		t.Fatalf("spent family row = %+v", spent)
	}
	argon := byKey["Gemini 4 Argon|weekly"]
	if argon.PercentLeft != 99 || !argon.Drives {
		t.Fatalf("argon row = %+v; it is the Gemini model's tightest window", argon)
	}
	for _, w := range buildV2Workers(in) {
		if w.Provider == "agy" && (w.Tag == "at limit" || w.LeftPct != 99 || strings.Contains(w.Detail, "wk") || w.TagTone == vm.ToneWarn) {
			t.Fatalf("agy worker row = %+v; the spent family is not its workers' weekly figure", w)
		}
	}
	in.Probed = true
	in.Probe.Installed = in.Installed
	found := false
	for _, c := range buildV2Ready(in).Checks {
		if c.Label == "Workers" && strings.HasPrefix(c.Text, "agy") {
			found = true
			if c.Mark != "ok" || strings.Contains(c.Text, "weekly 0%") {
				t.Fatalf("agy readiness check = %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("no agy readiness check")
	}
}

// The same reading with agy workers on a Claude model: agy is out, the
// spent family drives it, and the reset carries its date.
func TestLimitRowsAgyOutForSpentFamily(t *testing.T) {
	in := liveLimitsIn("claude-opus-5-5-high")
	l := limitsFor(t, buildV2Limits(in), "agy")
	if l.State != vm.LimitOut || !strings.Contains(l.Reason, "for Claude and GPT until Nov 26 09:32") {
		t.Fatalf("agy = %+v", l)
	}
	for _, r := range l.Rows {
		if want := r.Family == "Claude and GPT" && r.Window == "weekly"; r.Drives != want {
			t.Errorf("row %+v drives=%v, want %v", r, r.Drives, want)
		}
	}
	for _, w := range buildV2Workers(in) {
		if w.Provider == "agy" && (w.Tag != "at limit" || w.Detail != "until Nov 26 09:32") {
			t.Fatalf("agy worker row = %+v; the reset days away carries its date", w)
		}
	}
}

func TestLimitRowsClaudeNotReported(t *testing.T) {
	l := limitsFor(t, buildV2Limits(liveLimitsIn("gemini-4-argon-high")), "claude")
	if len(l.Rows) != 1 {
		t.Fatalf("claude = %+v", l)
	}
	r := l.Rows[0]
	if r.PercentLeft != -1 || r.Source != "not reported by the CLI" || !r.ResetsAt.IsZero() || r.Drives || r.Window != "" {
		t.Fatalf("claude row = %+v; a limit not reported is -1, never a number", r)
	}
	if l.State != vm.LimitUsable {
		t.Fatalf("claude state %q: a limit not reported does not stop dispatch", l.State)
	}
}

func TestLimitRowsOpencodeNotChecked(t *testing.T) {
	in := liveLimitsIn("gemini-4-argon-high")
	l := limitsFor(t, buildV2Limits(in), "opencode")
	if l.State != vm.LimitNotChecked || len(l.Rows) != 1 || l.Rows[0].PercentLeft != -1 || l.Rows[0].Source != "not checked" {
		t.Fatalf("opencode = %+v", l)
	}
	// Before the first reading every provider's limits are not checked.
	in.Quotas = nil
	for _, l := range buildV2Limits(in) {
		if len(l.Rows) != 1 || l.Rows[0].Source != "not checked" || l.Rows[0].PercentLeft != -1 {
			t.Fatalf("%s before a reading = %+v", l.Provider, l)
		}
	}
}

func TestLimitStatesForSignInAndInstall(t *testing.T) {
	in := liveLimitsIn("gemini-4-argon-high")
	in.Installed["opencode"] = false
	in.SignedOut = map[string]string{"codex": "codex is not signed in"}
	ls := buildV2Limits(in)
	if s := limitsFor(t, ls, "opencode").State; s != vm.LimitNotInstalled {
		t.Fatalf("opencode %q", s)
	}
	if c := limitsFor(t, ls, "codex"); c.State != vm.LimitNotSignedIn || c.Reason != "codex is not signed in" {
		t.Fatalf("codex %+v", c)
	}
}

// The dispatcher's hook reads the same per-family rule.
func TestModelLeftInPerFamily(t *testing.T) {
	var qs []usage.Quota
	for _, q := range liveQuotas() {
		qs = append(qs, q)
	}
	if left, ok := modelLeftIn(qs, "antigravity", "gemini-4-argon-high"); !ok || left != 99 {
		t.Fatalf("gemini = %d %v", left, ok)
	}
	if left, ok := modelLeftIn(qs, "agy", "claude-opus-5-5-high"); !ok || left != 0 {
		t.Fatalf("claude family = %d %v", left, ok)
	}
	if _, ok := modelLeftIn(qs, "agy", "mystery-model"); ok {
		t.Fatal("a model no family covers is unknown")
	}
	if left, ok := modelLeftIn(qs, "codex", "gpt-5.5-codex"); !ok || left != 8 {
		t.Fatalf("codex = %d %v", left, ok)
	}
	if _, ok := modelLeftIn(qs, "claude", "claude-opus-5-5"); ok {
		t.Fatal("claude's limit is not reported")
	}
}

// agy's limits report asked for authentication: agy is not signed in, not
// at a limit, and no sign-in URL reaches the Board.
func TestLimitsAgyNotSignedIn(t *testing.T) {
	in := liveLimitsIn("gemini-4-argon-high")
	in.Quotas["agy"] = usage.Quota{Provider: "agy", Reason: usage.ReasonNotSignedIn, Offer: []usage.ModelGroup{{Name: "Gemini 4", Offered: true}}}
	l := limitsFor(t, buildV2Limits(in), "agy")
	if l.State != vm.LimitNotSignedIn || !strings.Contains(l.Reason, "not signed in") || len(l.Rows) != 1 || l.Rows[0].Source != "not signed in" || l.Rows[0].PercentLeft != -1 {
		t.Fatalf("agy = %+v", l)
	}
	for _, w := range buildV2Workers(in) {
		if w.Provider == "agy" && w.Tag != "not signed in" {
			t.Fatalf("agy worker row = %+v", w)
		}
	}
}
