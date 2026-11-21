package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// homeRunning is a run with work going on and nothing needed from the owner.
func homeRunning() vm.Board {
	return vm.Board{
		Top:     vm.TopBar{Project: "demo", Run: "R-7", State: "RUNNING", StateTone: vm.ToneOK, Workers: 3, Clock: time.Date(2026, 11, 21, 14, 5, 0, 0, time.Local)},
		NowLine: vm.NowLine{Marshal: "claude Opus 5.5 · watching the work", Work: "3 running · 9 queued", FromYou: "nothing", FromYouOK: true},
		Plan:    vm.Plan{Done: 4, Total: 12, ETA: 12 * time.Minute},
		Web:     vm.Web{Running: true, Opened: true},
	}
}

// homePausedBoard is a run paused on a stuck task whose card's Enter moves
// it to codex and continues.
func homePausedBoard() vm.Board {
	b := homeRunning()
	b.Top.State, b.Top.StateTone = "PAUSED", vm.ToneBad
	b.NowLine.FromYou, b.NowLine.FromYouOK = "run paused: rv06 stuck", false
	b.Paused = &vm.Paused{Kind: "stalled", Title: "Paused · rv06 stuck", Actions: []vm.Key{{Key: "⏎", Label: "move rv06 to codex and continue"}, {Key: "x", Label: "stop the run"}}}
	return b
}

// The home screen at the sizes the owner named, in Unicode and ASCII: one
// header, Now, From you, Web, the key bar, and nothing else.
func TestHomeScreenGoldens(t *testing.T) {
	for _, tc := range []struct {
		name       string
		b          vm.Board
		cols, rows int
	}{
		{"home-running-080x24", homeRunning(), 80, 24},
		{"home-running-132x40", homeRunning(), 132, 40},
		{"home-running-060x20", homeRunning(), 60, 20},
		{"home-paused-080x24", homePausedBoard(), 80, 24},
		{"home-paused-060x20", homePausedBoard(), 60, 20},
		{"home-paused-132x40", homePausedBoard(), 132, 40},
	} {
		for _, unicode := range []bool{true, false} {
			name := tc.name
			if !unicode {
				name += "-ascii"
			}
			t.Run(name, func(t *testing.T) {
				v := boardVisual{theme: NewTheme(ThemeNoColor, unicode, false), now: tc.b.Top.Clock}
				frame := renderHome(v, tc.b, homePrimaryOf(tc.b, homeIn{}), tc.cols, tc.rows)
				if len(frame) != tc.rows {
					t.Fatalf("%d rows", len(frame))
				}
				for i, l := range frame {
					if n := VisibleLen(l); n != tc.cols {
						t.Fatalf("row %d is %d cells wide", i, n)
					}
				}
				text := strings.Join(frame, "\n")
				for _, bad := range []string{"eta", "ETA", "?t=", "http"} {
					if strings.Contains(text, bad) {
						t.Fatalf("the home screen shows %q:\n%s", bad, text)
					}
				}
				if !unicode && strings.ContainsFunc(text, func(r rune) bool { return r > 127 }) {
					t.Fatalf("ASCII frame has non-ASCII cells:\n%s", text)
				}
				checkGolden(t, name, frame)
			})
		}
	}
}

// Down to 40x10 every line is whole; smaller still never panics.
func TestHomeScreenSmallSizes(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, false)}
	b := homePausedBoard()
	frame := renderHome(v, b, homePrimaryOf(b, homeIn{}), 40, 10)
	text := strings.Join(frame, "\n")
	for _, want := range []string{"MARSHAL", "Now:", "From you:", "Web:", "⏎ chat"} {
		if !strings.Contains(text, want) {
			t.Fatalf("40x10 lacks %q:\n%s", want, text)
		}
	}
	for cols := 1; cols <= 45; cols++ {
		for rows := 1; rows <= 8; rows++ {
			if got := renderHome(v, b, homePrimaryOf(b, homeIn{}), cols, rows); len(got) != rows {
				t.Fatalf("%dx%d: %d rows", cols, rows, len(got))
			}
		}
	}
}

// Only a safe continue-type action gets y; a decision with authority says
// to open the web, and the Marshal's question in chat says Enter.
func TestHomeFromYou(t *testing.T) {
	paused := homePausedBoard()
	if got, _ := homeFromYou(paused, homePrimaryOf(paused, homeIn{})); got != "run paused: rv06 stuck — y move rv06 to codex and continue" {
		t.Fatalf("paused: %q", got)
	}
	inbox := homeRunning()
	inbox.NowLine.FromYou, inbox.NowLine.FromYouOK = "allow network access to example.com", false
	inbox.Decision = &vm.Decision{Kind: decisionInbox, Actions: []vm.Key{{Key: "⏎", Label: "allow once"}}}
	if p := homePrimaryOf(inbox, homeIn{}); p.kind != "" {
		t.Fatalf("an inbox decision got y: %+v", p)
	}
	if got, _ := homeFromYou(inbox, homePrimaryOf(inbox, homeIn{})); !strings.HasSuffix(got, "— open the web to decide (w)") {
		t.Fatalf("inbox: %q", got)
	}
	for _, kind := range []string{decisionArchitecture, decisionPlan, decisionGuard, decisionAsk, decisionChange, decisionNext} {
		b := homeRunning()
		b.Decision = &vm.Decision{Kind: kind, Actions: []vm.Key{{Key: "⏎", Label: "do it"}}}
		if p := homePrimaryOf(b, homeIn{}); p.kind != "" {
			t.Fatalf("%s got y: %+v", kind, p)
		}
	}
	req := homeRunning()
	req.Decision = &vm.Decision{Kind: decisionRequirements, Actions: []vm.Key{{Key: "⏎", Label: "confirm all"}}}
	if p := homePrimaryOf(req, homeIn{}); p.kind != decisionRequirements || p.label != "confirm all" {
		t.Fatalf("requirements: %+v", p)
	}
	if p := homePrimaryOf(homeRunning(), homeIn{retry: "T03"}); p.kind != "retry" || p.task != "T03" {
		t.Fatalf("retry: %+v", p)
	}
	asked := homeRunning()
	asked.NowLine = vm.NowLine{FromYou: vm.ChatAskedLine, ChatAsked: true}
	if got, _ := homeFromYou(asked, homePrimaryOf(asked, homeIn{})); got != vm.ChatAskedLine+" — ⏎ open chat" {
		t.Fatalf("chat asked: %q", got)
	}
	if got, needs := homeFromYou(homeRunning(), homePrimary{}); got != "nothing" || needs {
		t.Fatalf("nothing: %q %v", got, needs)
	}
}

// With nothing on a card, the From-you line says what y is for: a stuck
// task, a provider not checked yet; never "nothing — y …".
func TestHomeFromYouWithoutACard(t *testing.T) {
	b := homeRunning()
	if got, _ := homeFromYou(b, homePrimaryOf(b, homeIn{retry: "T03"})); got != "T03 is stuck — y retry T03" {
		t.Fatalf("retry: %q", got)
	}
	ready := vm.Board{NowLine: vm.NowLine{FromYou: "nothing", FromYouOK: true}}
	if got, _ := homeFromYou(ready, homePrimaryOf(ready, homeIn{verify: "claude"})); got != "claude is not checked yet — y check claude (one model call)" {
		t.Fatalf("verify: %q", got)
	}
	// A run on the Board is no time for a readiness check.
	if p := homePrimaryOf(b, homeIn{verify: "claude"}); p.kind != "" {
		t.Fatalf("verify during a run: %+v", p)
	}
	ready.Ready.TelemetryAsk = true
	if got, _ := homeFromYou(ready, homePrimaryOf(ready, homeIn{})); !strings.HasPrefix(got, "anonymous usage counts are on") {
		t.Fatalf("telemetry: %q", got)
	}
}

// t, g and e act only while their prompt is on the From-you line: with a
// paused card shown, the usage counts and agy's sign-in take no keys.
func TestHomeUndrawnPromptsTakeNoKeys(t *testing.T) {
	b := homePausedBoard()
	b.Ready.TelemetryAsk = true
	b.Ready.Actions = []vm.Key{{Key: "g", Label: "sign in"}}
	p := homePrimaryOf(b, homeIn{})
	for _, r := range "tge" {
		if homeKeyShown(b, p, r) {
			t.Errorf("%c acts while the paused card holds From you", r)
		}
	}
	ready := vm.Board{NowLine: vm.NowLine{FromYou: "nothing", FromYouOK: true}, Ready: vm.Readiness{TelemetryAsk: true}}
	if !homeKeyShown(ready, homePrimaryOf(ready, homeIn{}), 't') {
		t.Error("t does not act while the usage counts are on the line")
	}
	signIn := vm.Board{NowLine: vm.NowLine{FromYou: "nothing", FromYouOK: true}, Ready: vm.Readiness{Actions: []vm.Key{{Key: "g"}}}}
	if !homeKeyShown(signIn, homePrimaryOf(signIn, homeIn{}), 'g') || homeKeyShown(signIn, homePrimaryOf(signIn, homeIn{}), 't') {
		t.Error("g and t do not follow agy's sign-in line")
	}
}

// The first-run Marshal picker owns the keys even over a draft left in the
// composer, a plain one or a command with its completion menu open.
func TestPickerOwnsTheKeysOverADraft(t *testing.T) {
	for _, draft := range []string{"hello", "/he"} {
		t.Run(draft, func(t *testing.T) {
			w := emptyBoardWorkspace(t)
			defer w.Close()
			w.endOpening()
			w.composer.SetText(draft)
			if draft[0] == '/' {
				w.completionOpen = true
			}
			w.openPicker([]pickerCand{{provider: "codex", known: true, left: 40}, {provider: "claude"}}, 0, "")
			ctx := context.Background()
			w.handleTerminalKey(ctx, KeyEvent{Type: KeyDown})
			w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: 'x'})
			w.handleTerminalKey(ctx, KeyEvent{Type: KeyBackspace})
			if got := w.composer.Text(); got != draft {
				t.Fatalf("the draft took the picker's keys: %q", got)
			}
			w.picker.mu.Lock()
			sel, open := w.picker.sel, w.picker.open
			w.picker.mu.Unlock()
			if !open || sel != 1 {
				t.Fatalf("the picker did not take Down: open %v sel %d", open, sel)
			}
		})
	}
}

// The Web line never holds the private link; with no display it says how
// to get it.
func TestHomeWebLine(t *testing.T) {
	for _, tc := range []struct {
		web  vm.Web
		want string
	}{
		{vm.Web{}, "open with w"},
		{vm.Web{Running: true}, "open with w"},
		{vm.Web{Running: true, Opened: true}, "opened in your browser (w opens it again)"},
		{vm.Web{Running: true, NoDisplay: true}, "no display here: /marshal web shows the link to open"},
	} {
		if got := homeWeb(tc.web); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.web, got, tc.want)
		}
	}
}

// The chat's last turn asks the owner something only when it ends with a
// question (or asks for a reply) and nothing runs near the prompt.
func TestChatAskedIn(t *testing.T) {
	frame := func(last string) string {
		return "⏺ I looked at the folder.\n" + last + "\n\n╭────────────────────╮\n│ >                  │\n╰────────────────────╯\n  ? for shortcuts\n"
	}
	for _, tc := range []struct {
		last string
		want bool
	}{
		{"⏺ Should I read the files in docs/?", true},
		{"  Reply 'allow the folder' or 'paste them'.", true},
		{"⏺ Done: the plan has 12 tasks.", false},
		{"✻ Thinking… (esc to interrupt)", false},
	} {
		if got := chatAskedIn(frame(tc.last)); got != tc.want {
			t.Errorf("%q: %v, want %v", tc.last, got, tc.want)
		}
	}
	if chatAskedIn("no prompt here?") {
		t.Error("a frame with no prompt asked")
	}
}

// The collector puts the chat's question in From you, for the terminal and
// the Web Board alike, only when nothing else waits.
func TestFromYouChatAsked(t *testing.T) {
	in := flowIn{chatAsked: true}
	n := flowNowLine(in)
	if n.FromYou != vm.ChatAskedLine || n.FromYouOK || !n.ChatAsked {
		t.Fatalf("now line %+v", n)
	}
	if f := webboard.FromBoard(vm.Board{NowLine: n}); f.NowLine.FromYou != vm.ChatAskedLine {
		t.Fatalf("web From you %q", f.NowLine.FromYou)
	}
	in.chatBlocked = 1
	if n := flowNowLine(in); n.ChatAsked || n.FromYou == vm.ChatAskedLine {
		t.Fatalf("a card's ask lost to the chat's: %+v", n)
	}
}

// The Web Board starts with the Board and opens in the browser where there
// is a display; the home's Web line then says so, without the link.
func TestWebBoardAutostarts(t *testing.T) {
	opened := webBoardSeams(t)
	origGOOS := v2GOOS
	t.Cleanup(func() { v2GOOS = origGOOS })
	v2GOOS = "darwin"
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.endOpening()
	w.autoStartWebBoard(context.Background())
	if got := opened(); len(got) != 1 || webLink.FindString(got[0]) == "" {
		t.Fatalf("opened %v", got)
	}
	if s := w.webState(); !s.Running || !s.Opened || s.NoDisplay {
		t.Fatalf("web state %+v", s)
	}
	lines, _, _ := w.frameLinesFull(80, 24)
	text := StripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(text, "opened in your browser") || strings.Contains(text, "?t=") {
		t.Fatalf("home:\n%s", text)
	}
}

// With no display the Web Board still starts, no browser opens, and the Web
// line says how to get the link.
func TestWebBoardAutostartWithoutDisplay(t *testing.T) {
	opened := webBoardSeams(t)
	origGOOS, origEnv := v2GOOS, v2Getenv
	t.Cleanup(func() { v2GOOS, v2Getenv = origGOOS, origEnv })
	v2GOOS, v2Getenv = "linux", func(string) string { return "" }
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.endOpening()
	w.autoStartWebBoard(context.Background())
	if len(opened()) != 0 {
		t.Fatal("a browser opened with no display")
	}
	if s := w.webState(); !s.Running || s.Opened || !s.NoDisplay {
		t.Fatalf("web state %+v", s)
	}
	lines, _, _ := w.frameLinesFull(80, 24)
	text := StripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(text, "no display here: /marshal web shows the link") || strings.Contains(text, "?t=") {
		t.Fatalf("home:\n%s", text)
	}
}

// MARSHAL_WEB_OPEN=0 starts the Web Board without opening the browser.
func TestWebBoardAutostartNoOpen(t *testing.T) {
	opened := webBoardSeams(t)
	origGOOS, origEnv := v2GOOS, v2Getenv
	t.Cleanup(func() { v2GOOS, v2Getenv = origGOOS, origEnv })
	v2GOOS = "darwin"
	v2Getenv = func(k string) string {
		if k == "MARSHAL_WEB_OPEN" {
			return "0"
		}
		return ""
	}
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.autoStartWebBoard(context.Background())
	if len(opened()) != 0 || !w.webState().Running {
		t.Fatalf("opened %v, state %+v", opened(), w.webState())
	}
}

// Enter on the home screen opens the chat and never acts on a card; y with
// nothing safe to do says where to decide; the keys type nothing.
func TestHomeKeys(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.endOpening()
	w.frameLines(80, 24)
	if !w.handleHomeKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter was not taken")
	}
	if !w.handleHomeKey(KeyEvent{Type: KeyRune, Rune: 'y'}) {
		t.Fatal("y was not taken")
	}
	w.mu.RLock()
	out := w.state.LastOutput
	w.mu.RUnlock()
	if !strings.Contains(out, "Nothing waits for you.") && !strings.Contains(out, "Web Board") {
		t.Fatalf("y said %q", out)
	}
	if w.composer.Text() != "" {
		t.Fatalf("typed %q", w.composer.Text())
	}
	if w.handleHomeKey(KeyEvent{Type: KeyRune, Rune: 'z'}) {
		t.Fatal("z was taken: it is the composer's")
	}
}

// y runs the safe action once: a second y within homeYesQuiet does nothing.
func TestHomeYesOncePerPress(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.boardUI.homeYesAt = time.Now()
	before := w.outputSeq.Load()
	w.homeYes(context.Background())
	if w.outputSeq.Load() != before {
		t.Fatal("a second y within the quiet time did something")
	}
}

// A Marshal state that is already a sentence is not prefixed again ("the
// Marshal is the architecture is ready…" was drawn live).
func TestHomeNowDoesNotDoubleASentence(t *testing.T) {
	for doing, want := range map[string]string{
		"claude · reading the code": "the Marshal is reading the code",
		"claude · the architecture is ready for you to accept in the browser": "the architecture is ready for you to accept in the browser",
	} {
		if got := homeNow(vm.Board{NowLine: vm.NowLine{Marshal: doing}}); got != want {
			t.Errorf("homeNow(%q) = %q, want %q", doing, got, want)
		}
	}
}
