package v2

import (
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The fixtures hold the data docs/design/board-v2/tuipaint.py paints, so a
// render of each can be compared with the target screen*.ans.

func at(h, m int) time.Time { return time.Date(2026, 11, 20, h, m, 0, 0, time.UTC) }

const (
	sec = time.Second
	mn  = time.Minute
)

func kv(pairs ...string) []vm.Key { return keys(pairs...) }

func stagesBuild() []vm.Stage {
	return []vm.Stage{{Name: "goal", State: "done"}, {Name: "reqs", State: "done"}, {Name: "design", State: "done"}, {Name: "arch", State: "done"},
		{Name: "tasks", State: "done"}, {Name: "build", State: "current", Note: "5/12"}, {Name: "review", State: "todo"}, {Name: "merge", State: "todo"}}
}

func marshalClaude(effort string) vm.Marshal {
	return vm.Marshal{Provider: "claude", Model: "opus-5.5", Effort: effort, InUse: true, Doing: "watching reviews · next: final",
		ContextPct: 41, ContextUsed: 82000, ContextWindow: 200000, Limit5hPct: 58, WeeklyPct: 71, ChatBound: true, InputClear: true}
}

func fixtureRunning() vm.Board {
	lane := func(name, model, task, step string, el, sig time.Duration, rev, stale, sel bool) vm.Lane {
		return vm.Lane{Name: name, Model: model, Task: task, Step: step, Elapsed: el, LastSignal: sig, Reviewing: rev, Stale: stale, Selected: sel}
	}
	return vm.Board{
		Screen:  vm.ScreenRunning,
		Top:     vm.TopBar{Version: "v0.3.5", Project: "marshal-work", Run: "review-fixes", State: "RUNNING", StateTone: vm.ToneInfo, Workers: 8, Clock: at(19, 31)},
		NowLine: vm.NowLine{Marshal: "claude Opus · watching reviews", Work: "8 running · 4 queued", FromYou: "nothing · next ask: final review", FromYouOK: true},
		Marshal: marshalClaude("high"),
		Plan: vm.Plan{Title: "12 tasks · 18 requirements", Accepted: "accepted rev 1 · 18:52", Stages: stagesBuild(),
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
		},
		Workers: []vm.Worker{
			{Provider: "claude", Glyph: "✳", LeftPct: 58, Tag: "4 lanes", TagTone: vm.ToneOK, Detail: "sonnet"},
			{Provider: "agy", Glyph: "◆", LeftPct: 71, Tag: "4 lanes", TagTone: vm.ToneOK, Detail: "gemini-3.8"},
			{Provider: "codex", Glyph: "⬡", LeftPct: 11, Tag: "spare", TagTone: vm.ToneWarn, Detail: "wk 11%"},
		},
		Inbox: vm.Inbox{Next: "final review before merge."},
		Activity: []vm.Event{
			{At: at(19, 30), Kind: "merge", Text: "T08 merged after agy review", Tone: vm.ToneOK},
			{At: at(19, 28), Kind: "review", Text: "T02 review on claude-4", Tone: vm.ToneVio},
			{At: at(19, 22), Kind: "signal", Text: "agy-4 slow heartbeat", Tone: vm.ToneWarn, Count: 3},
			{At: at(18, 52), Kind: "plan", Text: "12 tasks · 8 workers", Tone: vm.ToneInfo},
			{At: at(18, 44), Kind: "arch", Text: "rev 1 accepted in browser", Tone: vm.ToneOK},
			{At: at(18, 41), Kind: "memory", Text: "old chat memory skipped", Tone: vm.ToneMuted, Count: 6},
		},
	}
}

func fixtureReady() vm.Board {
	return vm.Board{
		Screen:  vm.ScreenReady,
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
		Keys: kv("⏎", "say what to build", "g", "sign in to agy", "v", "check again", "m", "Marshal model", "?", "help"),
	}
}

func fixturePlanning() vm.Board {
	return vm.Board{
		Screen:  vm.ScreenPlanning,
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
		Chat: vm.Chat{Bound: true, Restarts: 1, ReboundAt: at(18, 41), Resent: 2, InputClear: true},
	}
}

func fixtureDecision() vm.Board {
	b := fixturePlanning()
	b.Screen = vm.ScreenDecision
	b.Top.State, b.Top.StateTone, b.Top.Inbox, b.Top.Clock = "YOUR TURN", vm.ToneWarn, 1, at(18, 42)
	b.NowLine = vm.NowLine{Marshal: "waiting on you", Work: "none yet", FromYou: "accept architecture rev 1 · a"}
	b.Decision = &vm.Decision{
		Title: "Accept architecture · rev 1", Summary: "14 parts · 37 links · covers R1–R18", ChangedNote: "5 parts changed by this goal (dashed)",
		Nodes: []vm.Node{{Name: "Board", Sub: "owner UI"}, {Name: "Runtime", Sub: "runs · leases"}, {Name: "Workers", Sub: "claude · agy"}, {Name: "architecture-review", Sub: "18 fixes", Changed: true},
			{Name: "Marshal chat", Sub: "MCP tools"}, {Name: "SQLite state", Sub: "revisions"}, {Name: "architecturecodec", Sub: "digests", Changed: true}, {Name: "review tests", Sub: "18 regressions", Changed: true}},
		Links: []vm.Link{{From: 0, To: 1}, {From: 1, To: 2}, {From: 2, To: 3, Changed: true}, {From: 4, To: 5, Both: true}, {From: 5, To: 6, Both: true},
			{From: 6, To: 7, Changed: true}, {From: 3, To: 7, Changed: true}},
		Link: "http://127.0.0.1:37667/?token=7964…462b", NoDisplay: true,
		SelfApproval: "The Marshal tried to accept this for you at 18:40. Refused: only you can accept.",
		Actions:      kv("a", "open in browser", "c", "copy link", "k", "comment"),
	}
	return b
}

func fixturePaused() vm.Board {
	return vm.Board{
		Screen:  vm.ScreenPaused,
		Top:     vm.TopBar{Version: "v0.3.5", Project: "marshal-work", State: "2 PAUSED", StateTone: vm.ToneBad, Inbox: 1, Clock: at(20, 5)},
		NowLine: vm.NowLine{Marshal: "waiting on your choice", Work: "6 running · 2 paused", FromYou: "press ⏎ to move 2 tasks to claude"},
		Marshal: marshalClaude("high"),
		Plan:    vm.Plan{Done: 7, Active: 1, Total: 12},
		Workers: []vm.Worker{
			{Provider: "claude", Glyph: "✳", LeftPct: 52, Tag: "ready", TagTone: vm.ToneOK, Detail: "52%"},
			{Provider: "agy", Glyph: "◆", LeftPct: 0, Tag: "out", TagTone: vm.ToneBad, Detail: "21:10"},
			{Provider: "codex", Glyph: "⬡", LeftPct: 10, Tag: "low", TagTone: vm.ToneWarn, Detail: "10%"},
		},
		Paused: &vm.Paused{Title: "Paused · agy Gemini 5h limit", Resets: "resets 21:10",
			Cause: "agy-2 and agy-4 hit the agy Gemini 5-hour limit. Their work is kept.",
			Tasks: []vm.PausedTask{{Title: "T06 history paging", Progress: "edits done · tests not run", Kept: "branch + 3 commits"},
				{Title: "T09 dashed-change styles", Progress: "half edited", Kept: "branch + 1 commit"}},
			Will:    "move T06 and T09 to claude sonnet in fresh sessions, keep commits.\nreview for these two moves to codex.",
			Actions: kv("⏎", "move to claude", "w", "wait until 21:10", "d", "raw error", "x", "stop the run"),
			Room:    "claude has room for both (≈ 6% each).", Run: "6 lanes running · no other stops"},
		Keys: kv("⏎", "move to claude", "w", "wait", "d", "details", "x", "stop run", "space", "actions"),
	}
}

func fixtureChat() vm.Board {
	return vm.Board{
		Screen:  vm.ScreenChat,
		Top:     vm.TopBar{Version: "v0.3.5", Project: "marshal-work", State: "PLANNING", StateTone: vm.ToneVio, Clock: at(18, 47)},
		Marshal: marshalClaude("medium"),
		Chat:    vm.Chat{Bound: true, InputClear: true, Answered: 3, Stage: "tasks · 6m"},
	}
}

func fixtureSplit() vm.Board {
	b := fixtureRunning()
	b.Screen = vm.ScreenSplit
	b.Top.Workers = 0
	b.Marshal = marshalClaude("medium")
	b.NowLine = vm.NowLine{Work: "8 running · 5/12 merged", FromYou: "nothing", FromYouOK: true}
	b.Chat = vm.Chat{Bound: true, InputClear: true}
	short := []string{"T03 sub-view links", "T05 request panel", "T07 legend focus", "review T02", "T04 codec test", "T06 history paging", "review T01", "T09 dashed styles"}
	for i := range b.Lanes.Items {
		b.Lanes.Items[i].Task = short[i]
	}
	for i, d := range []string{"sonnet", "gemini-3.8", "wk 11%"} {
		b.Workers[i].Detail = d
	}
	b.Activity = b.Activity[:3]
	return b
}

// claudeCode is the provider CLI's own area as tuipaint.py draws it (the
// Marshal does not restyle it), w x h cells.
func claudeCode(w, h int, compact bool) []string {
	g := make([][]rune, h)
	for i := range g {
		g[i] = []rune(strings.Repeat(" ", w))
	}
	put := func(x, y int, s string) {
		for _, r := range s {
			if y >= 0 && y < h && x >= 0 && x < w {
				g[y][x] = r
			}
			x++
		}
	}
	put(1, 1, "▐▛███▜▌")
	put(1, 2, "▝▜█████▛▘")
	put(1, 3, "  ▘▘ ▝▝")
	put(12, 1, "Claude Code v2.1.329")
	put(12, 2, "Opus 5.5 with medium effort · Claude Max")
	put(12, 3, "~/marshal-work")
	msgs := [][2]string{{"●", "Goal recorded (rev 3). Architecture rev 1 is accepted;"}, {" ", "reading it through the contract tool, page 1 of 2."},
		{"●", "marshal - marshal_project_contract_context (MCP)"}, {" ", "  ⎿ 14 parts · 37 links · 5 changed by this goal"},
		{"●", "Splitting 18 fixes into 12 tasks for 4 claude sonnet"}, {" ", "and 4 agy gemini-3.8 workers; review.html in 3 waves."}}
	if compact {
		msgs = msgs[:4]
	}
	for i, m := range msgs {
		put(1, 5+i, m[0])
		t := []rune(m[1])
		put(3, 5+i, string(t[:min(len(t), w-4)]))
	}
	put(1, 5+len(msgs)+1, "✻ Planning… (2m 10s · ↓ 4.1k tokens)")
	put(1, h-4, strings.Repeat("─", w-2))
	put(1, h-3, "❯ │")
	put(1, h-2, strings.Repeat("─", w-2))
	put(1, h-1, "⏵⏵ don't ask on (shift+tab to cycle)")
	out := make([]string, h)
	for i := range g {
		out[i] = string(g[i])
	}
	return out
}
