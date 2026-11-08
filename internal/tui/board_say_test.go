package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestChatPromptIdle(t *testing.T) {
	const dim, reset = "\x1b[2m", "\x1b[0m"
	for name, tc := range map[string]struct {
		frame string
		idle  bool
	}{
		"bare prompt":         {"codex stub\n> ", true},
		"codex placeholder":   {"header\n\n\x1b[1m›\x1b[0m " + dim + "Ask Codex to do anything" + reset + "\n\n  GPT-5 · 100% left\n", true},
		"codex draft":         {"header\n\x1b[1m›\x1b[0m half typed idea\n\n  GPT-5 · 100% left\n", false},
		"codex working":       {"• Working (12s • esc to interrupt)\n\n› " + dim + "Ask Codex" + reset + "\n", false},
		"claude box idle":     {"╭──────╮\n│ > " + "\x1b[38;5;244m" + "Try \"fix lint\"" + reset + " │\n╰──────╯\n  ? for shortcuts\n", true},
		"claude grey rgb":     {"│ > \x1b[38;2;153;153;153mTry something\x1b[39m │\n", true},
		"claude thinking":     {"✻ Thinking… (esc to interrupt)\n╭──────╮\n│ >  │\n╰──────╯\n", false},
		"claude permission":   {"Do you want to proceed?\n❯ 1. Yes\n  2. No\n", false},
		"menu choice":         {"Pick one\n❯ 1. Yes\n", false},
		"no prompt":           {"compiling...\nstill going\n", false},
		"old prompt then out": {"> earlier message\nanswer text\n", false},
		"bright placeholder":  {"› \x1b[97mwhite text\x1b[0m\n", false},
	} {
		if got := chatPromptIdle(tc.frame); got != tc.idle {
			t.Errorf("%s: idle %v, want %v", name, got, tc.idle)
		}
	}
}

// fakeChatPane starts a provider-like CLI in its own tmux window and makes
// it the Marshal chat: it prints a "> " prompt, echoes each line it reads as
// "got:<line>", and for a line "slow" stays busy for a moment with an
// "esc to interrupt" status before its next prompt.
func fakeChatPane(t *testing.T, w *Workspace) string {
	t.Helper()
	script := `printf 'fake provider\n> '
while IFS= read -r l; do
  printf 'got:%s\n' "$l"
  case "$l" in slow*) printf 'Working (esc to interrupt)'; sleep 2; printf '\r\033[K';; esac
  printf '> '
done`
	var pane string
	if err := tmux.NewWindow(context.Background(), w.tmuxSession, "marshal-chat-fake", w.workDir, nil, []string{"sh", "-c", script}, &pane); err != nil {
		t.Fatal(err)
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", paneID: pane, window: "marshal-chat-fake", provider: "codex"}
	w.tmuxMu.Unlock()
	waitForTmuxOutput(t, pane, "fake provider")
	return pane
}

func echoState(w *Workspace, text string) string {
	for _, e := range w.sayEchoList() {
		if e.Text == text {
			return e.State
		}
	}
	return ""
}

// What the operator types on the Board reaches the docked chat as their
// message; a message typed while the chat is busy waits, says so in
// Activity, and goes in once the chat is back at its prompt.
func TestBoardComposerReachesTheChat(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	w := realTmuxWorkspace(t)
	// Close stops the deliverer before the tmux server goes.
	t.Cleanup(w.Close)
	w.screen = NewScreen(NewTerminal(strings.NewReader(""), &strings.Builder{}))
	w.homeBoard = true
	oldPoll, oldSettle := sayPoll, saySettle
	sayPoll, saySettle = 50*time.Millisecond, 80*time.Millisecond
	defer func() { sayPoll, saySettle = oldPoll, oldSettle }()
	pane := fakeChatPane(t, w)
	ctx := context.Background()

	// s opens the Board's composer for a message (b would otherwise open
	// the Architecture view); typed and sent with Enter through the same
	// key path the terminal takes.
	for _, r := range "sslow build a todo app" {
		w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: r})
	}
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyEnter})
	if w.composer.Text() != "" {
		t.Fatalf("composer kept %q", w.composer.Text())
	}
	waitFor(t, func() bool { return echoState(w, "slow build a todo app") == saySent }, func() string {
		out, _ := tmux.CapturePane(ctx, pane)
		return fmt.Sprintf("echo %+v output %q pane:\n%s", w.sayEchoList(), w.state.LastOutput, out)
	})
	waitForTmuxOutput(t, pane, "got:slow build a todo app")

	// The chat is busy now: the next message is queued, not typed.
	waitForTmuxOutput(t, pane, "esc to interrupt")
	w.sayToMarshal(ctx, "and add tests")
	waitFor(t, func() bool { return echoState(w, "and add tests") == sayQueued }, func() string { return echoState(w, "and add tests") })
	out, _ := tmux.CapturePane(ctx, pane)
	if strings.Contains(out, "and add tests") {
		t.Fatalf("a message was typed into a busy chat:\n%s", out)
	}
	lines, _, _ := w.frameLinesFull(120, 40)
	if text := StripANSI(strings.Join(lines, "\n")); !strings.Contains(text, "queued · chat is busy") || !strings.Contains(text, "and add tests") {
		t.Fatalf("Activity does not show the queued message:\n%s", text)
	}
	// Back at its prompt, the chat gets it.
	waitForTmuxOutput(t, pane, "got:and add tests")
	waitFor(t, func() bool { return echoState(w, "and add tests") == saySent }, func() string {
		out, _ := tmux.CapturePane(ctx, pane)
		return fmt.Sprintf("echo %+v output %q pane:\n%s", w.sayEchoList(), w.state.LastOutput, out)
	})

	// A draft the operator left in the chat is never typed over.
	if _, err := tmux.RunCommand(ctx, "send-keys", "-t", pane, "-l", "my draft"); err != nil {
		t.Fatal(err)
	}
	w.sayToMarshal(ctx, "third")
	waitFor(t, func() bool { return echoState(w, "third") == sayQueued })
	time.Sleep(300 * time.Millisecond)
	if out, _ := tmux.CapturePane(ctx, pane); strings.Contains(out, "third") {
		t.Fatalf("typed over a draft:\n%s", out)
	}
}

// With no chat open nothing is sent and the text stays in the composer.
func TestBoardComposerWithoutChatKeepsTheText(t *testing.T) {
	w := emptyBoardWorkspace(t)
	ctx := context.Background()
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: 's'})
	lines, _, _ := w.frameLinesFull(100, 30)
	if !strings.Contains(StripANSI(lines[len(lines)-1]), "to the Marshal ›") {
		t.Fatalf("s did not open the message line: %q", lines[len(lines)-1])
	}
	for _, r := range "build it" {
		w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: r})
	}
	if w.composer.Text() != "build it" {
		t.Fatalf("a Board key took a letter of the message: %q", w.composer.Text())
	}
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyEnter})
	if w.composer.Text() != "build it" {
		t.Fatalf("composer %q", w.composer.Text())
	}
	if echoState(w, "build it") != sayFailed {
		t.Fatalf("echo %+v", w.sayEchoList())
	}
	if !strings.Contains(w.state.LastOutput, "No Marshal chat is open") {
		t.Fatalf("answer %q", w.state.LastOutput)
	}
	// Esc closes the message line; a command still runs as a command.
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyEsc})
	if w.composing() {
		t.Fatal("Esc left the message line open")
	}
	for _, r := range "/help" {
		w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: r})
	}
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyEnter})
	if len(w.sayEchoList()) != 1 {
		t.Fatal("a command was said to the Marshal")
	}
}
