package usage

import (
	"testing"
	"time"
)

// `agy -p /usage` as it read on the live workstation run: the Claude and GPT
// family spent for the week, Gemini 4 Argon nearly untouched.
const agyLiveUsage = "Gemini Models\tWeekly Limit Remaining\t100%\t2026-11-26T03:51:20Z\n" +
	"Gemini Models\tFive Hour Limit Remaining\t100%\t2026-11-21T23:51:20Z\n" +
	"Claude and GPT models\tWeekly Limit Remaining\t0%\t2026-11-26T08:32:00Z\n" +
	"Claude and GPT models\tFive Hour Limit Remaining\t40%\t2026-11-21T22:00:00Z\n" +
	"Gemini 4 Argon\tGemini 4 Argon Weekly Limit\t99%\t2026-11-24T23:48:16Z\n"

func liveAgy(t *testing.T) Quota {
	t.Helper()
	now, _ := time.Parse(time.RFC3339, "2026-11-21T18:37:00Z")
	return Quota{Provider: "agy", Known: true, AsOf: now, Windows: parseAgyUsage([]byte(agyLiveUsage), now)}
}

func TestFamilyCoversLiveLabels(t *testing.T) {
	for _, c := range []struct {
		family, model string
		want          bool
	}{
		{"Gemini", "gemini-4-argon-high", true},
		{"Gemini", "gemini-3.8-flash-high", true},
		{"Gemini 4 Argon", "gemini-4-argon-high", true},
		{"Gemini 4 Argon", "gemini-4-carbon-high", false},
		{"Gemini 4 Argon", "gemini-3.8-flash-high", false},
		{"Claude and GPT", "claude-opus-5-5-high", true},
		{"Claude and GPT", "gpt-oss-120b", true},
		{"Claude and GPT", "gemini-4-argon-high", false},
		{"Gemini", "claude-opus-5-5-high", false},
		{"Gemini", "", false},
		{"Gemini", "google/gemini-4-argon-high", true},
	} {
		if got := FamilyCovers(c.family, c.model); got != c.want {
			t.Errorf("FamilyCovers(%q, %q) = %v, want %v", c.family, c.model, got, c.want)
		}
	}
}

// The provider's worst window is Claude and GPT at 0%, but a Gemini model is
// limited only by the Gemini windows and its own Argon window.
func TestModelBindingPerFamily(t *testing.T) {
	q := liveAgy(t)
	if w, ok := q.Binding(); !ok || w.Label != "Claude and GPT wk" || w.UsedShown() != 100 {
		t.Fatalf("provider binding = %+v %v; want Claude and GPT wk at 0%%", w, ok)
	}
	w, ok := q.ModelBinding("gemini-4-argon-high")
	if !ok || w.Label != "Gemini 4 Argon wk" || w.UsedShown() != 1 {
		t.Fatalf("gemini binding = %+v %v; want Gemini 4 Argon wk at 99%%", w, ok)
	}
	if w, ok := q.ModelBinding("claude-opus-5-5-high"); !ok || w.Label != "Claude and GPT wk" || w.UsedShown() != 100 {
		t.Fatalf("claude binding = %+v %v; want Claude and GPT wk at 0%%", w, ok)
	}
	if _, ok := q.ModelBinding("mystery-model"); ok {
		t.Fatal("a model no family covers has no binding")
	}
	if _, ok := q.ModelBinding(""); ok {
		t.Fatal("an unknown model has no binding")
	}
}

// A provider with one limit for every model: every window applies to any
// model, as Binding.
func TestModelBindingUngrouped(t *testing.T) {
	reset := time.Now().Add(time.Hour)
	q := Quota{Provider: "codex", Known: true, Windows: []Window{{Label: "5h", LeftPercent: 98, ResetsAt: reset}, {Label: "wk", LeftPercent: 8, ResetsAt: reset}}}
	w, ok := q.ModelBinding("gpt-5.5-codex")
	if !ok || w.Label != "wk" {
		t.Fatalf("codex binding = %+v %v; want wk", w, ok)
	}
	if w2, _ := q.ModelBinding(""); w2 != w {
		t.Fatal("an unknown model reads the same windows")
	}
}

func TestSplitLabelAndKind(t *testing.T) {
	for label, want := range map[string][2]string{
		"5h": {"", "5h"}, "wk": {"", "wk"}, "Claude and GPT wk": {"Claude and GPT", "wk"}, "Gemini 4 Argon 5h": {"Gemini 4 Argon", "5h"},
	} {
		if f, k := SplitLabel(label); f != want[0] || k != want[1] {
			t.Errorf("SplitLabel(%q) = %q %q", label, f, k)
		}
	}
	if KindName("wk") != "weekly" || KindName("5h") != "5h" || KindName("90m") != "90m" {
		t.Fatal("KindName")
	}
}

func TestSource(t *testing.T) {
	for _, c := range []struct {
		q    Quota
		want string
	}{
		{Quota{Provider: "codex", Known: true}, "codex session log"},
		{Quota{Provider: "agy", Known: true}, "agy /usage"},
		{Quota{Provider: "claude", Reason: ReasonNotReported}, SourceNotReported},
		{Quota{Provider: "claude", Reason: ReasonNoFile}, SourceNotReported},
		{Unexposed("opencode"), SourceNotChecked},
		{Quota{Provider: "agy", Offer: []ModelGroup{{Name: "Gemini", Offered: true}}}, "agy models"},
		{Quota{Provider: "codex", Reason: ReasonStale}, "last report too old"},
		{Quota{Provider: "codex", Reason: ReasonMalformed}, "unreadable: malformed"},
	} {
		if got := c.q.Source(); got != c.want {
			t.Errorf("Source(%+v) = %q, want %q", c.q, got, c.want)
		}
	}
}
