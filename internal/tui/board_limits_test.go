package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/usage"
)

var limitsNow = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// limitsQuotas is the reading from the report: agy's Claude and GPT 5-hour
// group nearly spent, Codex low on its week, Claude healthy.
func limitsQuotas(agyLeft float64) []usage.Quota {
	return []usage.Quota{
		{Provider: "codex", Known: true, Windows: []usage.Window{
			{Label: "5h", LeftPercent: 45, ResetsAt: limitsNow.Add(2 * time.Hour)},
			{Label: "wk", LeftPercent: 19, ResetsAt: time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)},
		}},
		{Provider: "claude", Known: true, Windows: []usage.Window{{Label: "5h", LeftPercent: 61, ResetsAt: limitsNow.Add(3 * time.Hour)}}},
		{Provider: "opencode", Reason: usage.NotExposed},
		{Provider: "agy", Known: true, Windows: []usage.Window{
			{Label: "Gemini 5h", LeftPercent: 64, ResetsAt: limitsNow.Add(4 * time.Hour)},
			{Label: "Claude and GPT 5h", LeftPercent: agyLeft, ResetsAt: limitsNow.Add(2*time.Hour + 32*time.Minute)},
		}},
	}
}

func limitsBoard(worker string, queued int) *boardmodel.Board {
	b := &boardmodel.Board{Run: boardmodel.RunHead{ID: "R-1", State: marshal.Dispatching, Total: queued + 1}}
	b.Tasks = append(b.Tasks, boardmodel.TaskRow{ID: "T-0", Worker: worker, State: boardmodel.TaskWorking})
	for i := range queued {
		b.Tasks = append(b.Tasks, boardmodel.TaskRow{ID: "T-" + string(rune('1'+i)), Worker: worker, State: boardmodel.TaskQueued})
	}
	return b
}

// A window under 20% is one note per reset period: the same reading again,
// or a lower one before the reset, adds nothing; the next period warns again.
func TestLowLimitNoteOncePerPeriod(t *testing.T) {
	warned := map[string]limitWarned{}
	got := lowLimitNotes(limitsQuotas(12), nil, warned, limitsNow)
	want := []string{
		"codex weekly: 19% left · resets 21 Nov",
		"agy Claude and GPT 5h: 12% left · resets 14:32",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("notes = %q\nwant %q", got, want)
	}
	if again := lowLimitNotes(limitsQuotas(8), nil, warned, limitsNow.Add(10*time.Minute)); len(again) != 0 {
		t.Fatalf("same period noted again: %q", again)
	}
	// After the reset a new period that runs low is noted again.
	next := limitsQuotas(15)
	next[3].Windows[1].ResetsAt = limitsNow.Add(8 * time.Hour)
	// Codex's week has not reset, so only agy is noted.
	got = lowLimitNotes(next, nil, warned, limitsNow.Add(3*time.Hour))
	if len(got) != 1 || got[0] != "agy Claude and GPT 5h: 15% left · resets 20:00" {
		t.Fatalf("new period = %q", got)
	}
	// A healthy window is never noted.
	if n := lowLimitNotes(usageFresh(), nil, map[string]limitWarned{}, usageNow); len(n) != 0 {
		t.Fatalf("healthy reading noted: %q", n)
	}
}

// A group at 0% while tasks still wait for its provider says who can take
// them, once, even after its under-20% note was written.
func TestLowLimitSpentNamesOtherProvider(t *testing.T) {
	warned := map[string]limitWarned{}
	board := limitsBoard("agy", 3)
	if n := lowLimitNotes(limitsQuotas(12), board, warned, limitsNow); len(n) != 2 {
		t.Fatalf("first notes = %q", n)
	}
	got := lowLimitNotes(limitsQuotas(0), board, warned, limitsNow.Add(20*time.Minute))
	want := "agy Claude and GPT 5h: 0% left · resets 14:32. 3 tasks wait for agy; claude can take them (61% left)."
	if len(got) != 1 || got[0] != want {
		t.Fatalf("spent note = %q\nwant %q", got, want)
	}
	if again := lowLimitNotes(limitsQuotas(0), board, warned, limitsNow.Add(21*time.Minute)); len(again) != 0 {
		t.Fatalf("spent note repeated: %q", again)
	}
	// Nothing waiting: the 0% window is only its low note.
	idle := lowLimitNotes(limitsQuotas(0), limitsBoard("codex", 0), map[string]limitWarned{}, limitsNow)
	for _, n := range idle {
		if strings.Contains(n, "wait") {
			t.Fatalf("no queued work but %q", n)
		}
	}
	// One task, and no other provider with limit left.
	qs := []usage.Quota{{Provider: "agy", Known: true, Windows: []usage.Window{{Label: "Gemini 5h", LeftPercent: 0, ResetsAt: limitsNow.Add(time.Hour)}}}}
	got = lowLimitNotes(qs, limitsBoard("agy", 1), map[string]limitWarned{}, limitsNow)
	if len(got) != 1 || !strings.HasSuffix(got[0], "1 task waits for agy; no other provider has limit left.") {
		t.Fatalf("no other provider = %q", got)
	}
}

// storeUsage writes the notes to Activity, once.
func TestStoreUsageWritesLowLimitNoteOnce(t *testing.T) {
	w := &Workspace{repaint: make(chan struct{}, 1)}
	now := time.Now()
	qs := []usage.Quota{{Provider: "agy", Known: true, Windows: []usage.Window{{Label: "Claude and GPT 5h", LeftPercent: 12, ResetsAt: now.Add(2 * time.Hour)}}}}
	w.storeUsage(qs)
	w.storeUsage(qs)
	notes := w.noteList()
	if len(notes) != 1 || !strings.HasPrefix(notes[0].Text, "agy Claude and GPT 5h: 12% left · resets ") || notes[0].Err {
		t.Fatalf("notes = %+v", notes)
	}
}

// The vitals row names a low window and when it resets; narrower, it keeps
// the percent left.
func TestVitalsLowWindowNamed(t *testing.T) {
	v := vitalsFixture(NewTheme(ThemeNoColor, true, false))
	v.vitals.quotas[2] = usage.Quota{Provider: "agy", Known: true, Windows: []usage.Window{
		{Label: "Gemini 5h", LeftPercent: 64, ResetsAt: vitalsNow.Add(4 * time.Hour)},
		{Label: "Claude and GPT 5h", LeftPercent: 12, ResetsAt: vitalsNow.Add(2*time.Hour + 32*time.Minute)},
	}}
	line, _ := v.vitalsLine(200)
	if got := StripANSI(line); !strings.Contains(got, "◆ agy ▓░░ Claude and GPT 5h: 12% left · resets 14:32") {
		t.Fatalf("wide row: %q", got)
	}
	line, _ = v.vitalsLine(100)
	if got := StripANSI(line); !strings.Contains(got, "12% left") || strings.Contains(got, "resets") {
		t.Fatalf("narrow row: %q", got)
	}
	// A healthy row is unchanged.
	if got := StripANSI(func() string { l, _ := vitalsFixture(v.theme).vitalsLine(160); return l }()); strings.Contains(got, "resets") {
		t.Fatalf("healthy row names a reset: %q", got)
	}
}

func TestPlanLimitText(t *testing.T) {
	got, low := planLimitText(limitsQuotas(5), []string{"codex"}, 18, limitsNow)
	want := "Limits: codex 19% weekly (resets 21 Nov), 45% 5h — this plan has 18 tasks. Below 20%: codex weekly."
	if got != want || !low {
		t.Fatalf("got %q low=%v\nwant %q", got, low, want)
	}
	got, low = planLimitText(limitsQuotas(5), []string{"agy", "claude", "opencode"}, 1, limitsNow)
	want = "Limits: agy Claude and GPT 5% 5h (resets 14:32), Gemini 64% 5h · claude 61% 5h · opencode limit unknown — this plan has 1 task. Below 20%: agy Claude and GPT 5h."
	if got != want || !low {
		t.Fatalf("got %q low=%v\nwant %q", got, low, want)
	}
	got, low = planLimitText(limitsQuotas(50), []string{"claude"}, 4, limitsNow)
	if got != "Limits: claude 61% 5h — this plan has 4 tasks" || low {
		t.Fatalf("healthy plan: %q low=%v", got, low)
	}
}

// The limit check reaches Activity once per plan, when the plan is about to
// run and the limits have been read.
func TestNotePlanLimitsOncePerPlan(t *testing.T) {
	w := &Workspace{repaint: make(chan struct{}, 1)}
	p := &MarshalPanel{RunID: "R-1", Board: *limitsBoard("codex", 17)}
	p.Board.PlanID = "P-1"
	w.notePlanLimits(p) // not read yet: nothing to say
	if n := w.noteList(); len(n) != 0 {
		t.Fatalf("noted before a reading: %+v", n)
	}
	w.usage.quotas, w.usage.read = limitsQuotas(50), true
	w.notePlanLimits(p)
	w.notePlanLimits(p)
	notes := w.noteList()
	if len(notes) != 1 || !strings.HasPrefix(notes[0].Text, "Limits: codex 19% weekly (resets ") || !strings.Contains(notes[0].Text, "this plan has 18 tasks. Below 20%: codex weekly.") {
		t.Fatalf("notes = %+v", notes)
	}
	// A run already under way is not a plan about to start.
	running := &MarshalPanel{RunID: "R-2", Board: *limitsBoard("codex", 2)}
	running.Board.Run.ID, running.Board.Run.Done = "R-2", 1
	w.notePlanLimits(running)
	if len(w.noteList()) != 1 {
		t.Fatalf("noted a run under way: %+v", w.noteList())
	}
}

// The plan gate shows the limit check under its buttons, amber when low.
func TestPlanGateShowsLimits(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, false), now: limitsNow}
	g := ownerGate{key: "plan:R-1:P-1", kind: "plan", title: "drafted plan waits for your approval", summary: "18 tasks", reply: gateReplyPlan, details: '7'}
	if lines := v.gateEntry(g, 0, 0, 120); len(lines) != 2 {
		t.Fatalf("no limits: %d rows", len(lines))
	}
	g.limits, g.limitsLow = planLimitText(limitsQuotas(50), []string{"codex"}, 18, limitsNow)
	lines := v.gateEntry(g, 0, 0, 120)
	if len(lines) != 3 || !strings.HasPrefix(StripANSI(lines[2]), "    Limits: codex 19% weekly (resets 21 Nov), 45% 5h — this plan has 18 tasks") {
		t.Fatalf("rows = %q", lines)
	}
	if n := VisibleLen(lines[2]); n != 120 {
		t.Fatalf("limit row width %d", n)
	}
}
