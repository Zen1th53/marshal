package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/board/vm"
)

// homeBusy is a run with lanes, tasks, Activity, the Marshal's state, an
// approval and readiness all filled in, and a decision for the owner.
func homeBusy() vm.Board {
	at := time.Date(2026, 11, 21, 14, 5, 0, 0, time.Local)
	active, capN := 2, 4
	b := homeRunning()
	b.Now = at
	b.NowLine.FromYou, b.NowLine.FromYouOK = "T05 allow network access to api.example.test", false
	b.Mode = vm.Mode{Active: &active, EffectiveCap: &capN}
	b.Plan = vm.Plan{
		Title: "12 tasks · 18 requirements", Accepted: "accepted rev 1 · 13:02",
		Stages: []vm.Stage{{Name: "plan", State: "done"}, {Name: "build", State: "current", Note: "5/12"}, {Name: "merge", State: "todo"}},
		Done:   4, Active: 2, Waiting: 1, Total: 12, ETA: 25 * time.Minute,
		Throughput: []int{0, 1, 3, 2}, MergedPerHr: 6, Reworked: 1, Tokens: 1_250_000,
	}
	b.Lanes = vm.Lanes{Limits: "limit claude 4 · agy 4 · total 12", Items: []vm.Lane{
		{Name: "claude-1", Provider: "claude", Model: "opus", Task: "T03 sub-view links", Step: "editing", Elapsed: 4 * time.Minute, LastSignal: 12 * time.Second, Signal: vm.Signal{State: vm.SignalLive, Label: "live"}},
		{Name: "agy-1", Provider: "agy", Model: "gemini", Task: "review T02", Step: "reviewing", Elapsed: 9 * time.Minute, LastSignal: 95 * time.Second, Stale: true, Signal: vm.Signal{State: vm.SignalStale}},
	}}
	b.Tasks = []vm.Task{
		{ID: "T02", Title: "parser", State: "review", Writer: "claude-1", Reviewer: "agy-1"},
		{ID: "T03", Title: "sub-view links", State: "building", Writer: "claude-1"},
		{ID: "T04", Title: "export", State: "waiting", Waiting: &vm.Waiting{Code: vm.WaitDependency, Label: "waits for T03", BlockerID: "T03"}},
		{ID: "T05", Title: "fetch", State: "waiting", Waiting: &vm.Waiting{Code: vm.WaitOwner, Label: "approval for the network", BlockerID: "ap-1"}},
		{ID: "T06", Title: "docs", State: "waiting", Waiting: &vm.Waiting{Code: vm.WaitPause, Label: "claude 5h limit", RetryAt: at.Add(40 * time.Minute)}},
		{ID: "T01", Title: "scaffold", State: "merged"},
	}
	b.Approvals = []vm.Approval{{ID: "ap-1", TaskID: "T05", Awaiting: "owner", CreatedAt: at.Add(-7 * time.Minute), ExpiresAt: at.Add(23 * time.Minute), Status: "pending", Reason: "network access", Actions: []vm.Key{{Key: "a", Label: "allow"}, {Key: "d", Label: "deny"}}}}
	b.Inbox = vm.Inbox{Count: 2, Lines: []string{"T05 allow network access to api.example.test", "plan change waits for your approval"}, Next: "the plan change"}
	b.Activity = []vm.Event{
		{At: at.Add(-3 * time.Minute), Kind: "merge", Text: "T01 merged"},
		{At: at.Add(-1 * time.Minute), Kind: "review", Text: "T02 sent for review", Count: 2},
	}
	b.Marshal = vm.Marshal{Provider: "claude", Model: "Opus 5.5", Effort: "high", ContextPct: 42, ContextUsed: 84_000, ContextWindow: 200_000, Limit5hPct: 61, WeeklyPct: 83}
	b.Steps = vm.Steps{Model: "Opus 5.5", Quiet: 40 * time.Second, Items: []vm.Step{{At: at.Add(-2 * time.Minute), Kind: "assign", Text: "T03 to claude-1"}}}
	b.Requests = []vm.Request{{Sent: at.Add(-4 * time.Minute), What: "start T03", Answer: "allow", Latency: 3 * time.Second}}
	b.Chat = vm.Chat{Bound: true, Restarts: 1, ReboundAt: at.Add(-30 * time.Minute), Resent: 2, Answered: 5, Stage: "building"}
	b.Workers = []vm.Worker{{Provider: "claude", Tag: "4 lanes", LeftPct: 61, SignedIn: true, Installed: true}}
	b.Limits = []vm.ProviderLimits{{Provider: "claude", Model: "opus", State: vm.LimitUsable, Rows: []vm.LimitRow{{Window: "5h", PercentLeft: 61, ResetsAt: at.Add(2 * time.Hour)}, {Window: "weekly", PercentLeft: 83}}}}
	b.Ready = vm.Readiness{SessionLabel: "manual", Ready: 3, Total: 4, Checked: at.Add(-time.Hour), Checks: []vm.Check{
		{Mark: "ok", Label: "git", Text: "clean"},
		{Mark: "warn", Label: "agy", Text: "not signed in", Hint: "run agy once"},
	}, Defaults: [][2]string{{"global-cap", "12"}}}
	b.System = vm.System{CPUPct: 37, MemPct: 58, DiskPct: 91, TempC: 71}
	return b
}

func pageText(t *testing.T, v boardVisual, b vm.Board, page string, cols, rows int) (string, *pageState) {
	t.Helper()
	st := &pageState{}
	frame := renderHomePage(v, b, page, st, cols, rows)
	checkFrame(t, frame, cols, rows)
	return strings.Join(frame, "\n"), st
}

func checkFrame(t *testing.T, frame []string, cols, rows int) {
	t.Helper()
	if len(frame) != rows {
		t.Fatalf("%d rows, want %d", len(frame), rows)
	}
	for i, l := range frame {
		if n := VisibleLen(l); n != cols {
			t.Fatalf("row %d is %d cells wide, want %d:\n%s", i, n, cols, strings.Join(frame, "\n"))
		}
	}
}

func pageMustShow(t *testing.T, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("lacks %q:\n%s", w, text)
		}
	}
}

// At 80x24 the home names the work, the lanes and tasks, the Marshal, the
// latest Activity and the owner's next action, and its key bar the pages.
func TestHomeShowsWorkAndOwnerActionAt80x24(t *testing.T) {
	for _, unicode := range []bool{true, false} {
		v := boardVisual{theme: NewTheme(ThemeNoColor, unicode, false)}
		b := homeBusy()
		frame := renderHome(v, b, homePrimaryOf(b, homeIn{}), 80, 24)
		checkFrame(t, frame, 80, 24)
		text := strings.Join(frame, "\n")
		if !unicode && strings.ContainsFunc(text, func(r rune) bool { return r > 127 }) {
			t.Fatalf("ASCII frame has non-ASCII cells:\n%s", text)
		}
		if !unicode {
			text = strings.NewReplacer(" . ", " · ", "x2", "×2").Replace(text)
		}
		pageMustShow(t, text, "From you:", "allow network access", "open the web to",
			"Work:", "2 active / cap 4", "1 waiting",
			"Lanes:", "claude-1 T03 4m · agy-1 review T02 9m STALE",
			"Tasks:", "T03 building", "T04 waiting (dependency)",
			"Marshal:", "claude Opus 5.5 high · context 42% · 5h 61% · weekly 83%",
			"Activity:", "T02 sent for review",
			"r run", "i inbox", "c checks", "? keys")
		if strings.Contains(text, "T01 merged ·") || strings.Contains(text, "T01 merged (") {
			t.Errorf("a merged task is on the Tasks line:\n%s", text)
		}
	}
}

// r opens the Run view from the home; the selection stays on the same lane
// or task while rows move, the terminal is resized or the list grows, and
// Esc goes back to the home.
func TestRunViewReachableAndSelectionStable(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.endOpening()
	if !w.handleHomeKey(KeyEvent{Type: KeyRune, Rune: 'r'}) || w.boardUI.homePage != homePageRun {
		t.Fatalf("r did not open the Run view: %q", w.boardUI.homePage)
	}
	lines, _, _ := w.frameLines(80, 24)
	text := strings.Join(lines, "\n")
	pageMustShow(t, text, "[r Run]", "STAGES", "LANES", "TASKS")

	v := boardVisual{theme: NewTheme(ThemeNoColor, true, false)}
	b := homeBusy()
	st := &w.boardUI.homePageState
	renderHomePage(v, b, homePageRun, st, 80, 24)
	for range 3 {
		if !w.handleHomeKey(KeyEvent{Type: KeyRune, Rune: 'j'}) {
			t.Fatal("j was not taken on the Run view")
		}
	}
	want := st.sel
	if want != "task:T03" {
		t.Fatalf("three downs from the first lane select %q", want)
	}
	selected := func(frame []string) string {
		for _, l := range frame {
			if strings.Contains(l, "▸ T03") {
				return l
			}
		}
		return ""
	}
	// The lanes and tasks are reordered and one task is added before it.
	b2 := homeBusy()
	b2.Lanes.Items[0], b2.Lanes.Items[1] = b2.Lanes.Items[1], b2.Lanes.Items[0]
	b2.Tasks = append([]vm.Task{{ID: "T00", State: "building"}}, b2.Tasks...)
	for _, size := range [][2]int{{80, 24}, {132, 40}, {60, 12}, {40, 8}} {
		frame := renderHomePage(v, b2, homePageRun, st, size[0], size[1])
		checkFrame(t, frame, size[0], size[1])
		if st.sel != want {
			t.Fatalf("%dx%d: selection moved to %q", size[0], size[1], st.sel)
		}
		if selected(frame) == "" {
			t.Fatalf("%dx%d: the selected task is not drawn:\n%s", size[0], size[1], strings.Join(frame, "\n"))
		}
	}
	// The selected task leaves: the selection falls on its neighbour.
	b3 := homeBusy()
	b3.Tasks = append(b3.Tasks[:1], b3.Tasks[2:]...)
	renderHomePage(v, b3, homePageRun, st, 80, 24)
	if st.sel == "" || st.sel == want {
		t.Fatalf("selection after the task left: %q", st.sel)
	}
	if !w.handleHomeKey(KeyEvent{Type: KeyEsc}) || w.boardUI.homePage != "" {
		t.Fatal("Esc did not go back to the home")
	}
	// The other pages open from the home and from each other.
	for _, k := range []struct {
		r    rune
		page string
	}{{'i', homePageInbox}, {'c', homePageReady}, {'r', homePageRun}} {
		if !w.handleHomeKey(KeyEvent{Type: KeyRune, Rune: k.r}) || w.boardUI.homePage != k.page {
			t.Fatalf("%c opened %q", k.r, w.boardUI.homePage)
		}
	}
	if !w.handleHomeKey(KeyEvent{Type: KeyRune, Rune: '1'}) || w.boardUI.homePage != "" {
		t.Fatal("1 did not go back to the home")
	}
}

// Without a browser the readiness checklist and Activity are on screen:
// the Readiness view lists the checks and their hints, the Run view and
// the home list Activity.
func TestReadinessAndActivityVisibleWithoutWeb(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, false)}
	b := homeBusy()
	b.Web = vm.Web{NoDisplay: true}
	ready, _ := pageText(t, v, b, homePageReady, 80, 40)
	pageMustShow(t, ready, "READINESS", "3/4 ready", "checked 13:05", "✓ git · clean", "▲ agy · not signed in", "run agy once", "global-cap: 12")
	run, _ := pageText(t, v, b, homePageRun, 80, 60)
	pageMustShow(t, run, "ACTIVITY", "14:02 · merge · T01 merged", "T02 sent for review ×2")
	home := strings.Join(renderHome(v, b, homePrimaryOf(b, homeIn{}), 80, 24), "\n")
	pageMustShow(t, home, "no display here", "Activity:", "T02 sent for review")
}

// Every field the terminal projects is drawn on the home or one of its
// pages, and the keys the key bar leaves out are in the help it opens.
func TestHomeRunProjectionFieldsAndHiddenKeys(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, false)}
	b := homeBusy()
	run, _ := pageText(t, v, b, homePageRun, 100, 80)
	wide, _ := pageText(t, v, b, homePageRun, 160, 40)
	inbox, _ := pageText(t, v, b, homePageInbox, 140, 60)
	ready, _ := pageText(t, v, b, homePageReady, 100, 60)
	pageMustShow(t, wide, "STAGES", "ACTIVITY", "MARSHAL", "▸ claude-1", "STALE", "T02 sent for review ×2", "ETA 25m")
	for _, text := range []string{run} {
		// Lanes: provider, model, task, step, elapsed, signal, stale.
		pageMustShow(t, text, "claude-1 · claude/opus · T03 sub-view links · editing · 4m · signal live 12s ago",
			"agy-1 · agy/gemini", "STALE",
			// Stages, ETA, throughput sparkline and tokens.
			"✓ plan", "▸ build 5/12", "ETA 25m", "6 merged/h", "1 reworked", "1.2M tokens", "▁▃█▅",
			"limit claude 4 · agy 4 · total 12",
			// Marshal identity, context and quota; steps, requests, chat.
			"claude · Opus 5.5 · high · context 42% (84k/200k) · 5h 61% left · weekly 83% left",
			"planning on Opus 5.5", "quiet 40s", "assign · T03 to claude-1",
			"start T03 · → allow in 3s",
			"chat bound · building · 5 answered · 1 restarts · rebound 13:35 · 2 resent")
	}
	// Every queued owner item, the approval's age, expiry and actions,
	// and dependency, approval and pause waits with code and until.
	pageMustShow(t, inbox, "QUEUED (2)", "T05 allow network access", "plan change waits for your approval", "next: the plan change",
		"ap-1 · T05 · awaiting owner · pending · waiting 7m · expires 14:28 (in 23m) · network access · a allow, d deny",
		"T04 waits: waits for T03 (dependency, on T03)",
		"T05 waits: approval for the network (owner, on ap-1)",
		"T06 waits: claude 5h limit (pause, until 14:45 (in 40m))",
		"Web Board")
	// Caps and quota percent, window and reset.
	pageMustShow(t, ready, "caps: limit claude 4", "claude · 4 lanes · 61% left", "limits claude opus · usable · 5h 61% left resets", "weekly 83% left",
		"cpu 37% · memory 58% · disk 91% · 71°C (2)")

	home := strings.Join(renderHome(v, b, homePrimaryOf(b, homeIn{}), 80, 24), "\n")
	pageMustShow(t, home, "? keys")
	help, _, _ := v.helpOverlay(120, 1000, 0)
	plain := StripANSI(strings.Join(help, "\n"))
	for _, k := range []string{"?", "space", "/", "m ", "a ", "e ", "d ", "2 ", "3 ", "4 ", "5 ", "6 ", "7 ", "8 ", "Ctrl+X", "r ", "i ", "c "} {
		if !strings.Contains(plain, k) {
			t.Errorf("help lacks %q:\n%s", k, plain)
		}
	}
	for _, what := range []string{"run: stages, lanes, tasks, Activity", "inbox: everything waiting for you", "readiness: checks, providers, limits", "stop all workers"} {
		if !strings.Contains(plain, what) {
			t.Errorf("help lacks %q", what)
		}
	}
}

// The host notice has its own home row with the keys that reach more: an
// answer on the status row, new Activity or typing does not push it off.
func TestHostNoticeSurvivesOtherStatus(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, false), status: "Nothing waits for you."}
	b := homeBusy()
	b.System.Notice = "host: thermal guard · CPU 91°C"
	b.System.ThermalGuard, b.System.ThermalCap = true, 2
	b.System.UpdateAvailable, b.System.MCPFailed = "v1.4.0", true
	b.Activity = append(b.Activity, vm.Event{At: b.Now, Kind: "note", Text: "a newer line"})
	for _, size := range [][2]int{{80, 24}, {132, 40}, {60, 20}} {
		frame := renderHome(v, b, homePrimaryOf(b, homeIn{}), size[0], size[1])
		checkFrame(t, frame, size[0], size[1])
		text := strings.Join(frame, "\n")
		pageMustShow(t, text, "Host:", "▲ thermal guard", "Nothing waits for you.")
		if size[0] >= 132 {
			pageMustShow(t, text, "lanes capped at 2", "update v1.4.0 (F10)", "MCP failed (F6)", "2 host view")
		}
		if !strings.Contains(frame[len(frame)-1], "Nothing waits for you.") {
			t.Fatalf("the status row lost the answer:\n%s", text)
		}
	}
	if hostAbnormal(vm.System{CPUPct: 10}) != "" {
		t.Fatal("a quiet host has a home row")
	}
}
