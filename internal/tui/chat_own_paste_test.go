package tui

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
)

// fakeClaudePane is a Claude Code input box behind a chatRunner. While busy
// it keeps showing its dim suggestion and does not draw a paste; once the
// turn ends it draws the pasted text (the captured mac frame) and Enter
// submits it, leaving the empty suggestion frame.
type fakeClaudePane struct {
	mu           sync.Mutex
	suggestion   string
	typed        string
	busy         bool
	pasted       string
	pastes       int
	enters       int
	submitted    []string
	cmds         []string
	drawnFrame   string
	pasteOption  string
	emptyOnEnter string
}

func (f *fakeClaudePane) frame() string {
	if f.pasted != "" && !f.busy {
		return f.drawnFrame
	}
	return f.suggestion
}

func (f *fakeClaudePane) run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, strings.Join(args, " "))
	switch args[0] {
	case "display-message":
		return []byte("%7 0 0\n"), nil
	case "capture-pane":
		fr := f.frame()
		for _, a := range args {
			if a == "-e" {
				return []byte(fr), nil
			}
		}
		return []byte(StripANSI(fr)), nil
	case "set-buffer":
		f.typed = args[len(args)-1]
	case "paste-buffer":
		f.pastes++
		f.pasted = f.typed
	case "send-keys":
		f.enters++
		if f.pasted != "" && !f.busy {
			f.submitted = append(f.submitted, f.pasted)
			f.pasted = ""
		}
	case "set-option":
		if len(args) > 2 && args[2] != "-u" {
			f.pasteOption = args[len(args)-1]
		}
	case "show-options":
		return []byte(f.pasteOption), nil
	}
	return nil, nil
}

func readFrame(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The Marshal's answer is pasted while the Claude turn is still running, so
// the box does not draw it and the delivery gives up. The owner is told it
// waits, and the card stays. When the turn ends, the card's Enter submits
// the leftover paste once, without pasting a second time.
func TestAskAnswerPastedDuringRunningTurnIsSubmittedByCardEnter(t *testing.T) {
	const label = "Start from main, land on main"
	ownPastes.mu.Lock()
	ownPastes.panes = map[string]ownPaste{}
	ownPastes.mu.Unlock()
	w := emptyBoardWorkspace(t)
	defer w.Close()
	f := &fakeClaudePane{
		suggestion: readFrame(t, "mac_frame_suggestion.txt"),
		drawnFrame: readFrame(t, "mac_frame.txt"),
		busy:       true,
	}
	w.chatRun = f.run
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", paneID: "%7", window: "marshal-chat-fake", provider: "claude"}
	w.tmuxMu.Unlock()
	choices := `[{"key":"enter","label":"` + label + `"},{"key":"f","label":"Land on a new feature branch"}]`
	p, err := parseMarshalProposal(ownPasteAskLine("Which branch should the work start from and land on?", choices))
	if err != nil {
		t.Fatal(err)
	}
	w.openAsk(p)
	if w.currentAsk() == nil {
		t.Fatal("the card did not open")
	}

	w.answerAsk(0)
	if w.currentAsk() == nil {
		w.mu.Lock()
		t.Logf("output %q cmds %v", w.state.LastOutput, f.cmds)
		w.mu.Unlock()
		t.Fatal("the card closed although nothing was submitted")
	}
	f.mu.Lock()
	if f.pastes != 1 || f.enters != 0 {
		t.Fatalf("pastes %d enters %d after the first press", f.pastes, f.enters)
	}
	f.mu.Unlock()
	w.mu.Lock()
	out := w.state.LastOutput
	w.mu.Unlock()
	if !strings.Contains(out, "not submitted") && !strings.Contains(out, "not sent") {
		t.Fatalf("the owner was told nothing: %q", out)
	}

	f.mu.Lock()
	f.busy = false
	f.mu.Unlock()
	w.answerAsk(0)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.submitted) != 1 || f.submitted[0] != label {
		t.Fatalf("submitted %q, want exactly %q", f.submitted, label)
	}
	if f.pastes != 1 {
		t.Fatalf("pasted %d times, want once", f.pastes)
	}
	if w.currentAsk() != nil {
		t.Fatal("the card stayed after the answer was submitted")
	}
}

// A dim suggestion in an empty Claude input is not the owner's text: the
// delivery sees an idle prompt, and awaiting a paste keeps waiting over it.
func TestDimSuggestionIsNotOwnerText(t *testing.T) {
	sugg := readFrame(t, "mac_frame_suggestion.txt")
	if !composerClear(composerDialectFor("claude"), sugg) || !chatPromptIdle(sugg) {
		t.Fatal("a dim suggestion was read as owner text")
	}
	text := "Start from main, land on main"
	drawn := readFrame(t, "mac_frame.txt")
	if composerClear(composerDialectFor("claude"), drawn) || !ownChatComposer(drawn, text) {
		t.Fatal("the pasted answer was not read as ours")
	}
	f := &fakeClaudePane{suggestion: sugg, drawnFrame: drawn, busy: true, pasted: text}
	d := composerDialectFor("claude")
	seen, _, err := awaitOwnPaste(context.Background(), f.run, d, "%7", text, 1, 20_000_000)
	if err != nil || seen {
		t.Fatalf("over a suggestion: seen %v err %v, want still waiting", seen, err)
	}
	f.busy = false
	seen, _, err = awaitOwnPaste(context.Background(), f.run, d, "%7", text, 1, 20_000_000)
	if err != nil || !seen {
		t.Fatalf("drawn: seen %v err %v", seen, err)
	}
}

func ownPasteAskLine(question, choices string) string {
	data, err := json.Marshal(map[string]string{"action": "ask", "question": question, "choices": choices})
	if err != nil {
		panic(err)
	}
	return marshalProposalPrefix + string(data)
}

// "confirm all" is said to the chat while Claude's redraw lags behind the
// paste: the delivery gives up waiting for it to show, tells the owner it
// waits, and the unsent paste is submitted at the next look once drawn.
func TestSaidMessagePastedUnderLagIsSubmittedLater(t *testing.T) {
	const text = "confirm all"
	ownPastes.mu.Lock()
	ownPastes.panes = map[string]ownPaste{}
	ownPastes.mu.Unlock()
	w := emptyBoardWorkspace(t)
	defer w.Close()
	f := &fakeClaudePane{
		suggestion: readFrame(t, "mac_frame_suggestion.txt"),
		drawnFrame: readFrame(t, "mac_frame_confirm_unsent.txt"),
		busy:       true,
	}
	w.chatRun = f.run
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", paneID: "%7", window: "marshal-chat-fake", provider: "claude"}
	w.tmuxMu.Unlock()

	if _, err := w.typeIntoChat(context.Background(), "%7", text); err != nil {
		t.Fatalf("a lagging redraw failed the delivery: %v", err)
	}
	w.mu.Lock()
	out := w.state.LastOutput
	w.mu.Unlock()
	if strings.Contains(out, "did not reach") {
		t.Fatalf("reported as lost: %q", out)
	}
	if !strings.Contains(out, "has not taken it yet") {
		t.Fatalf("the owner was not told it waits: %q", out)
	}
	f.mu.Lock()
	if len(f.submitted) != 0 || f.pastes != 1 {
		t.Fatalf("submitted %q pastes %d too early", f.submitted, f.pastes)
	}
	f.busy = false
	f.mu.Unlock()

	w.submitOwnLeftover(context.Background(), "%7", false)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.submitted) != 1 || f.submitted[0] != text || f.pastes != 1 {
		t.Fatalf("submitted %q pastes %d, want %q once", f.submitted, f.pastes, text)
	}
}
