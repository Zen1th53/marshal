package webboard

import (
	"time"

	"github.com/Zen1th53/marshal/internal/board/vm"
)

// The fixtures are the Board v2 golden fixtures (internal/tui/board/v2),
// so the page can be compared with the terminal screens.

func at(h, m int) time.Time { return time.Date(2026, 11, 20, h, m, 0, 0, time.UTC) }

func kv(pairs ...string) []vm.Key {
	var out []vm.Key
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, vm.Key{Key: pairs[i], Label: pairs[i+1]})
	}
	return out
}

func marshalClaude(effort string) vm.Marshal {
	return vm.Marshal{Provider: "claude", Model: "opus-5.5", Effort: effort, InUse: true, Doing: "watching reviews · next: final",
		ContextPct: 41, ContextUsed: 82000, ContextWindow: 200000, Limit5hPct: 58, WeeklyPct: 71, ChatBound: true, InputClear: true, InputKnown: true}
}

func fixtureRunning() vm.Board {
	const sec, mn = time.Second, time.Minute
	lane := func(name, model, task, step string, el, sig time.Duration, rev, stale, sel bool) vm.Lane {
		return vm.Lane{Name: name, Model: model, Task: task, Step: step, Elapsed: el, LastSignal: sig, Reviewing: rev, Stale: stale, Selected: sel}
	}
	return vm.Board{
		Screen:  vm.ScreenRunning,
		Now:     at(19, 31),
		Top:     vm.TopBar{Version: "v0.3.5", Project: "marshal-work", Run: "review-fixes", State: "RUNNING", StateTone: vm.ToneInfo, Workers: 8, Clock: at(19, 31)},
		NowLine: vm.NowLine{Marshal: "claude Opus · watching reviews", Work: "8 running · 4 queued", FromYou: vm.ChatAskedLine, ChatAsked: true},
		Marshal: marshalClaude("high"),
		Plan: vm.Plan{Title: "12 tasks · 18 requirements", Accepted: "accepted rev 1 · 18:52",
			Stages: []vm.Stage{{Name: "goal", State: "done"}, {Name: "reqs", State: "done"}, {Name: "design", State: "done"}, {Name: "arch", State: "done"},
				{Name: "tasks", State: "done"}, {Name: "build", State: "current", Note: "5/12"}, {Name: "review", State: "todo"}, {Name: "merge", State: "todo"}},
			Done: 5, Active: 6, Waiting: 5, Total: 12, ETA: 45 * mn, Throughput: []int{0, 1, 2, 4, 5, 6, 6, 7, 6, 5, 6, 7}, MergedPerHr: 5, Tokens: 1_900_000},
		Lanes: vm.Lanes{Limits: "limit claude 4 · agy 4 · total 12", Items: []vm.Lane{
			lane("claude-1", "sonnet-5.5", "T03 sub-view external links", "tests", 14*mn, 8*sec, false, false, true),
			lane("claude-2", "sonnet-5.5", "T05 request panel overlap", "editing", 11*mn, 4*sec, false, false, false),
			lane("claude-3", "sonnet-5.5", "T07 legend keyboard focus", "editing", 6*mn, 12*sec, false, false, false),
			lane("claude-4", "sonnet-5.5", "review T02 (by agy-1)", "reviewing", 2*mn, 3*sec, true, false, false),
			lane("agy-1", "gemini-3.8", "T04 codec round-trip test", "tests", 9*mn, 6*sec, false, false, false),
			lane("agy-2", "gemini-3.8", "T06 history paging", "editing", 8*mn, 9*sec, false, false, false),
			lane("agy-3", "gemini-3.8", "review T01 (by claude-2)", "reviewing", 3*mn, 2*sec, true, false, false),
			lane("agy-4", "gemini-3.8", "T09 dashed-change styles", "editing", 7*mn, 100*sec, false, true, false),
		}},
		Tasks: []vm.Task{
			{ID: "T01", Title: "stale target refusal message", Files: "review.go", Writer: "claude", Reviewer: "agy", State: "merged"},
			{ID: "T02", Title: "digest-bound review requests", Files: "codec.go", Writer: "agy", Reviewer: "claude", State: "review"},
			{ID: "T03", Title: "sub-view external links", Files: "review.html", Writer: "claude", Reviewer: "agy", State: "building"},
			{ID: "T05", Title: "request panel overlap", Files: "review.html", Writer: "claude", Reviewer: "agy", State: "building"},
			{ID: "T12", Title: "regression matrix sign-off", Files: "matrix.md", Writer: "claude", Reviewer: "agy", State: "waiting", Note: "waits T03"},
			{ID: "T04", Title: "codec round-trip test", Files: "codec_test.go", Writer: "agy", Reviewer: "claude", State: "building", Note: "rework 1"},
			{ID: "T06", Title: "history paging", Files: "history.go", Writer: "agy", Reviewer: "claude", State: "building"},
			{ID: "T07", Title: "legend keyboard focus", Files: "legend.js", Writer: "claude", Reviewer: "agy", State: "building"},
			{ID: "T08", Title: "review page empty state", Files: "review.html", Writer: "claude", Reviewer: "agy", State: "merged"},
			{ID: "T09", Title: "dashed-change styles", Files: "review.css", Writer: "agy", Reviewer: "claude", State: "building"},
			{ID: "T10", Title: "request panel copy", Files: "review.html", Writer: "", State: "waiting", Note: "waits T05"},
			{ID: "T11", Title: "codec error messages", Files: "codec.go", Writer: "", State: "waiting", Note: "queued"},
		},
		Workers: []vm.Worker{
			{Provider: "claude", Glyph: "✳", LeftPct: 58, Tag: "4 lanes", TagTone: vm.ToneOK, Detail: "sonnet", SignedIn: true, Installed: true},
			{Provider: "agy", Glyph: "◆", LeftPct: 71, Tag: "4 lanes", TagTone: vm.ToneOK, Detail: "gemini-3.8", SignedIn: true, Installed: true},
			{Provider: "codex", Glyph: "⬡", LeftPct: 11, Tag: "spare", TagTone: vm.ToneWarn, Detail: "wk 11%", SignedIn: true, Installed: true,
				Reason: "weekly limit low: kept as the fallback only"},
		},
		Limits: []vm.ProviderLimits{
			{Provider: "claude", Model: "sonnet", State: vm.LimitUsable, Rows: []vm.LimitRow{
				{Family: "", Window: "5h", PercentLeft: 58, ResetsAt: at(21, 0), ObservedAt: at(19, 29), Source: "claude /usage", Drives: true},
				{Family: "", Window: "weekly", PercentLeft: 71, ResetsAt: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC), ObservedAt: at(19, 29), Source: "claude /usage"},
				{Family: "opus", Window: "weekly", PercentLeft: 83, ResetsAt: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC), ObservedAt: at(19, 29), Source: "claude /usage"},
			}},
			{Provider: "agy", Model: "gemini-3.8", State: vm.LimitUsable, Rows: []vm.LimitRow{
				{Family: "Gemini 3.8", Window: "5h", PercentLeft: 71, ResetsAt: at(22, 10), ObservedAt: at(19, 24), Source: "agy /usage", Drives: true},
				{Family: "Gemini 3.8", Window: "weekly", PercentLeft: -1, ObservedAt: at(19, 24), Source: "not reported by the CLI"},
			}},
			{Provider: "codex", Model: "gpt-5.5", State: vm.LimitUsable, Reason: "weekly limit 11%: used only when claude and agy are out", Rows: []vm.LimitRow{
				{Family: "", Window: "5h", PercentLeft: 90, ResetsAt: at(20, 40), ObservedAt: at(19, 10), Source: "codex session log"},
				{Family: "", Window: "weekly", PercentLeft: 11, ResetsAt: time.Date(2026, 11, 21, 6, 0, 0, 0, time.UTC), ObservedAt: at(19, 10), Source: "codex session log", Drives: true},
			}},
		},
		Inbox: vm.Inbox{Next: "final review before merge.", Refs: []string{"secret-ref"}},
		Activity: []vm.Event{
			{At: at(19, 30), Kind: "merge", Text: "T08 merged after agy review", Tone: vm.ToneOK},
			{At: at(19, 28), Kind: "review", Text: "T02 review on claude-4", Tone: vm.ToneVio},
			{At: at(19, 22), Kind: "signal", Text: "agy-4 slow heartbeat", Tone: vm.ToneWarn, Count: 3, Last: at(19, 29)},
			{At: at(18, 52), Kind: "plan", Text: "12 tasks · 8 workers", Tone: vm.ToneInfo},
			{At: at(18, 44), Kind: "arch", Text: "rev 1 accepted in browser", Tone: vm.ToneOK},
			{At: at(18, 41), Kind: "memory", Text: "old chat memory skipped", Tone: vm.ToneMuted, Count: 6},
			{At: at(18, 40), Kind: "note", Text: "Marshal decision requested: decision-8a8d3f0c91b2e4d7a6c5 project-contract hello-uz", Tone: vm.ToneInfo},
			{At: at(18, 39), Kind: "note", Text: "Marshal decision requested: decision-1f2e3d4c5b6a79880011 memory MEM-IMPORT-3111ee9093dfae48", Tone: vm.ToneInfo, Count: 2},
		},
		Steps: vm.Steps{Items: []vm.Step{
			{At: at(19, 29), Kind: "check", Text: "T02 review verdict: changes requested"},
			{At: at(19, 25), Kind: "assign", Text: "T07 to claude-3"},
		}, Quiet: 42 * sec, Model: "claude Opus 5.5"},
		Requests: []vm.Request{
			{Sent: at(18, 44), What: "apply architecture", Answer: "allow", Latency: 3 * sec, Tone: vm.ToneOK},
			{Sent: at(18, 42), What: "parallel limit 8", Answer: "allow", Latency: 5 * sec, Tone: vm.ToneOK},
		},
		Chat: vm.Chat{Bound: true, InputClear: true, InputKnown: true},
		// The probe's readiness stays on the Board during a run.
		Ready: vm.Readiness{Ready: 1, Total: 1, TelemetryOn: true, Checked: at(18, 30),
			Checks:   []vm.Check{{Mark: "ok", Label: "Marshal", Text: "claude · Opus 5.5 · signed in", Tag: "in use"}},
			Defaults: [][2]string{{"workers", "claude 4 · agy 4"}, {"fallback", "codex"}, {"review", "other provider"}, {"approval", "you: architecture · tasks: Marshal, then you"}}},
		System: vm.System{CPUPct: 64, MemPct: 71, DiskPct: 58, TempC: 83, ThermalGuard: true, ThermalCap: 6,
			Notice: "host: thermal guard · asked owner: lane cap 12 → 6 (inbox)", UpdateAvailable: "v0.3.9"},
		MarshalSays: vm.MarshalSays{At: at(19, 27), Text: "T02 came back from review with two changes requested: the digest check " +
			"runs before the stale-target check, and one test name is wrong. I sent it back to agy-1 with both points.\n\n" +
			"T12 is stuck on a flaky matrix run. Should I retry it from scratch, or wait for T03 to merge first?"},
	}
}

// hostUnknown is a System whose host figures are not known yet, as the
// collector leaves them before the host watcher's first sample.
var hostUnknown = vm.System{CPUPct: -1, MemPct: -1, DiskPct: -1, TempC: -1}

func fixtureReady() vm.Board {
	return vm.Board{
		Screen:  vm.ScreenReady,
		System:  hostUnknown,
		Top:     vm.TopBar{Version: "v0.3.5", Project: "marshal-work", State: "READY", StateTone: vm.ToneOK, Clock: at(18, 22)},
		NowLine: vm.NowLine{Marshal: "claude Opus · ready", Work: "no run yet", FromYou: "sign in to agy, then ⏎ say the goal"},
		Marshal: marshalClaude("high"),
		Ready: vm.Readiness{Ready: 5, Total: 7, Checked: at(18, 22), TelemetryAsk: true, TelemetryOn: true,
			Checks: []vm.Check{
				{Mark: "ok", Label: "Marshal", Text: "claude · Opus 5.5 · signed in", Tag: "in use"},
				{Mark: "ok", Label: "Workers", Text: "claude Sonnet 5.5 · up to 4 at once", Tag: "ready"},
				{Mark: "warn", Label: "Workers", Text: "agy is installed but not signed in", Tag: "action", Hint: "run  agy  once in any terminal and sign in · g opens it here"},
				{Mark: "warn", Label: "Fallback", Text: "codex signed in · weekly 11% · resets 21 Nov", Tag: "low"},
				{Mark: "ok", Label: "Platform", Text: "Linux amd64 · workers can run here", Tag: "ready"},
				{Mark: "ok", Label: "Repo", Text: "clean · main @ abb617e · 2 earlier Codex sessions", Tag: "ready"},
				{Mark: "ok", Label: "Browser", Text: "no display: links are shown on the Board", Tag: "ready"},
			},
			Defaults: [][2]string{{"workers", "claude 4 · agy 4"}, {"fallback", "codex"}, {"review", "other provider"}, {"approval", "you: architecture, merge"}},
			Actions:  kv("g", "sign in to agy", "⏎", "say what to build", "v", "check again"),
		},
		Workers: []vm.Worker{
			{Provider: "claude", Glyph: "✳", LeftPct: 58, Tag: "in use", TagTone: vm.ToneOK, Detail: "sonnet", SignedIn: true, Installed: true},
			{Provider: "agy", Glyph: "◆", LeftPct: -1, Tag: "not signed in", TagTone: vm.ToneWarn, Installed: true, Reason: "not signed in: run agy once"},
			{Provider: "codex", Glyph: "⬡", LeftPct: 11, Tag: "spare", TagTone: vm.ToneWarn, Detail: "wk 11%", SignedIn: true, Installed: true},
		},
	}
}

func fixturePlanning() vm.Board {
	const sec = time.Second
	return vm.Board{
		Screen:  vm.ScreenPlanning,
		System:  hostUnknown,
		Top:     vm.TopBar{Version: "v0.3.5", Project: "marshal-work", State: "PLANNING", StateTone: vm.ToneVio, Clock: at(18, 47)},
		NowLine: vm.NowLine{Marshal: "claude Opus · tasks · 6m", Work: "none yet", FromYou: "nothing · plan in ≈ 5m", FromYouOK: true},
		Marshal: marshalClaude("high"),
		Plan: vm.Plan{Stages: []vm.Stage{{Name: "goal", State: "done"}, {Name: "reqs", State: "done"}, {Name: "design", State: "done"},
			{Name: "arch", State: "done", Note: "rev 1"}, {Name: "tasks", State: "current", Note: "6m"}, {Name: "build"}, {Name: "review"}, {Name: "merge"}}},
		Steps: vm.Steps{Items: []vm.Step{
			{At: at(18, 47), Kind: "draft", Text: "12 tasks · review.html edits ordered in 3 waves"},
			{At: at(18, 46), Kind: "assign", Text: "4 claude sonnet · 4 agy gemini-3.8 · review on other"},
			{At: at(18, 45), Kind: "check", Text: "old branch dogfood/review-page-fixes: none · use main"},
			{At: at(18, 43), Kind: "read", Text: "requirements.md · design.md · 18-row regression matrix"},
			{At: at(18, 41), Kind: "read", Text: "architecture rev 1 · 14 parts · 37 links · 2 pages"},
			{At: at(18, 41), Kind: "start", Text: "planning on claude Opus 5.5 (plan model)"},
		}},
		Requests: []vm.Request{
			{Sent: at(18, 44), What: "apply architecture", Answer: "allow", Latency: 3 * sec, Tone: vm.ToneOK},
			{Sent: at(18, 42), What: "parallel limit 8", Answer: "allow", Latency: 5 * sec, Tone: vm.ToneOK},
			{Sent: at(18, 41), What: "memory import", Answer: "decline", Latency: 4 * sec},
		},
		Chat: vm.Chat{Bound: true, Restarts: 1, ReboundAt: at(18, 41), Resent: 2, InputClear: true, InputKnown: true},
	}
}

func fixtureDecision() vm.Board {
	b := fixturePlanning()
	b.Screen = vm.ScreenDecision
	b.Top.State, b.Top.StateTone, b.Top.Inbox, b.Top.Clock = "YOUR TURN", vm.ToneWarn, 1, at(18, 42)
	b.NowLine = vm.NowLine{Marshal: "waiting on you", Work: "none yet", FromYou: "accept architecture rev 1 · a"}
	b.Decision = &vm.Decision{
		Kind:  "architecture",
		Title: "Accept architecture · rev 1", Summary: "14 parts · 37 links · covers R1–R18", ChangedNote: "5 parts changed by this goal (dashed)",
		Nodes: []vm.Node{{Name: "Board", Sub: "owner UI"}, {Name: "Runtime", Sub: "runs · leases"}, {Name: "Workers", Sub: "claude · agy"}, {Name: "architecture-review", Sub: "18 fixes", Changed: true},
			{Name: "Marshal chat", Sub: "MCP tools"}, {Name: "SQLite state", Sub: "revisions"}, {Name: "architecturecodec", Sub: "digests", Changed: true}, {Name: "review tests", Sub: "18 regressions", Changed: true}},
		Links: []vm.Link{{From: 0, To: 1}, {From: 1, To: 2}, {From: 2, To: 3, Changed: true}, {From: 4, To: 5, Both: true}},
		Lines: []vm.CardLine{{Key: "a", Label: "scope", Text: "the review page only", Note: "R1", Ref: "secret-ref"}},
		Link:  "http://127.0.0.1:37667/?token=7964deadbeef462b", NoDisplay: true,
		SelfApproval: "The Marshal tried to accept this for you at 18:40. Refused: only you can accept.",
		Actions:      kv("a", "open in browser", "c", "copy link", "k", "comment"),
		Refs:         []string{"secret-ref"},
	}
	return b
}

func fixturePaused() vm.Board {
	return vm.Board{
		Screen:  vm.ScreenPaused,
		System:  hostUnknown,
		Top:     vm.TopBar{Version: "v0.3.5", Project: "marshal-work", Run: "review-fixes", State: "2 PAUSED", StateTone: vm.ToneBad, Inbox: 1, Clock: at(20, 5)},
		NowLine: vm.NowLine{Marshal: "waiting on your choice", Work: "6 running · 2 paused", FromYou: "press ⏎ to move 2 tasks to claude"},
		Marshal: marshalClaude("high"),
		Plan:    vm.Plan{Done: 7, Active: 1, Total: 12},
		Workers: []vm.Worker{
			{Provider: "claude", Glyph: "✳", LeftPct: 52, Tag: "ready", TagTone: vm.ToneOK, Detail: "52%"},
			{Provider: "agy", Glyph: "◆", LeftPct: 0, Tag: "out", TagTone: vm.ToneBad, Detail: "21:10"},
			{Provider: "codex", Glyph: "⬡", LeftPct: 10, Tag: "low", TagTone: vm.ToneWarn, Detail: "10%"},
		},
		Limits: []vm.ProviderLimits{{Provider: "agy", Model: "gemini-3.8", State: vm.LimitOut, Reason: "Gemini 5-hour limit used up; no new work until 21:10",
			Rows: []vm.LimitRow{{Family: "Gemini 3.8", Window: "5h", PercentLeft: 0, ResetsAt: at(21, 10), ObservedAt: at(20, 4), Source: "agy /usage", Drives: true}}}},
		Paused: &vm.Paused{Kind: "quota", Title: "Paused · agy Gemini 5h limit", Resets: "resets 21:10",
			Cause: "agy-2 and agy-4 hit the agy Gemini 5-hour limit. Their work is kept.",
			Tasks: []vm.PausedTask{{Title: "T06 history paging", Progress: "edits done · tests not run", Kept: "branch + 3 commits"},
				{Title: "T09 dashed-change styles", Progress: "half edited", Kept: "branch + 1 commit"}},
			Will:    "move T06 and T09 to claude sonnet in fresh sessions, keep commits.\nreview for these two moves to codex.",
			Actions: kv("⏎", "move to claude", "w", "wait until 21:10", "d", "raw error", "x", "stop the run"),
			Room:    "claude has room for both (≈ 6% each).", Run: "6 lanes running · no other stops"},
	}
}

// fixtureFinished is the running Board once the run is done: eleven tasks
// merged, one cancelled, no lane left.
func fixtureFinished() vm.Board {
	b := fixtureRunning()
	b.Top.State, b.Top.StateTone, b.Top.Workers = "DONE", vm.ToneOK, 0
	b.NowLine = vm.NowLine{Marshal: "claude Opus · idle", Work: "11 merged · 1 cancelled", FromYou: "nothing", FromYouOK: true}
	b.Plan.Done, b.Plan.Active, b.Plan.Waiting, b.Plan.ETA = 11, 0, 0, 0
	for i := range b.Plan.Stages {
		b.Plan.Stages[i].State, b.Plan.Stages[i].Note = "done", ""
	}
	b.Lanes.Items = nil
	tasks := append([]vm.Task(nil), b.Tasks...)
	for i := range tasks {
		tasks[i].State, tasks[i].Note = "merged", ""
	}
	tasks[len(tasks)-1].State = "cancelled"
	b.Tasks = tasks
	b.MarshalSays = vm.MarshalSays{At: at(21, 2), Text: "All done: 11 tasks merged into the run branch; T11 was cancelled as no longer needed."}
	return b
}

// fixtureBareDecision is a decision card whose frame carries nothing it
// rests on (no summary, lines, parts or note): the page does not offer it.
func fixtureBareDecision() vm.Board {
	b := fixtureRunning()
	b.Screen = vm.ScreenDecision
	b.Top.State, b.Top.StateTone, b.Top.Inbox = "YOUR TURN", vm.ToneWarn, 1
	b.NowLine.FromYou, b.NowLine.FromYouOK = "approve the project contract · ⏎", false
	b.Decision = &vm.Decision{Kind: "project-contract", Title: "Approve the project contract", Actions: kv("⏎", "allow", "x", "deny")}
	return b
}

func fixture(name string) (vm.Board, bool) {
	switch name {
	case "finished":
		return fixtureFinished(), true
	case "bare":
		return fixtureBareDecision(), true
	case "", "running":
		return fixtureRunning(), true
	case "ready":
		return fixtureReady(), true
	case "planning":
		return fixturePlanning(), true
	case "decision":
		return fixtureDecision(), true
	case "paused":
		return fixturePaused(), true
	}
	return vm.Board{}, false
}
