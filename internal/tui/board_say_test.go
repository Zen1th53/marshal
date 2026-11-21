//go:build linux

package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
		"claude trust":        {"Do you trust the files in this folder?\n❯ 1. Yes, proceed\n  2. No, exit\n", false},
		"agy trust":           {"Do you trust the contents of this project?\n\n❯ Yes, I trust the contents of this project\n  No, exit\n", false},
		"agy trust bare":      {"Do you trust the contents of this project?\n> \n", false},
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

func TestChatWaitingFor(t *testing.T) {
	for name, tc := range map[string]struct {
		frame string
		want  chatWait
	}{
		"agy trust prompt": {
			"Do you trust the contents of this project?\n\n/Users/user/project\n\n❯ Yes, I trust the contents of this project\n  No, exit\n",
			chatWaitTrust,
		},
		"claude trust prompt with enter to confirm": {
			"Do you trust the files in this folder?\n❯ 1. Yes, proceed\n  2. No, exit\nEnter to confirm · Esc to exit\n",
			chatWaitTrust,
		},
		"codex trust prompt": {
			"Do you trust the contents of this directory?\n› 1. Yes, continue\nPress enter to continue\n",
			chatWaitTrust,
		},
		"boxed wrapped trust prompt": {
			"╭──────────────────────────╮\n│ Do you trust the         │\n│ contents of this project?│\n│ ❯ Yes                    │\n╰──────────────────────────╯\n",
			chatWaitTrust,
		},
		"claude permission prompt": {
			"Bash command\n  go test ./...\nDo you want to proceed?\n❯ 1. Yes\n  2. No\n",
			chatWaitPermission,
		},
		"codex mcp permission prompt": {
			"Allow the marshal MCP server to run tool marshal_plan_submit?\n› 1. Allow once\n  2. Allow for this session\nenter to submit | esc to cancel\n",
			chatWaitPermission,
		},
		"agy permission prompt": {
			"Allow agy to run shell command?\n❯ Allow once\n  Allow always\n  Deny\n",
			chatWaitPermission,
		},
		"conversational question is not a permission dialog": {
			"compiling...\nHow do you want to proceed?\n",
			chatWaitNone,
		},
		"actively working turn is not a prompt": {
			"Do you trust the contents of this project?\n• Working (12s • esc to interrupt)\n",
			chatWaitNone,
		},
		"draft in prompt": {
			"fake provider\n> my draft\n",
			chatWaitNone,
		},
	} {
		if got := chatWaitingFor(tc.frame); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestChatWaitWords(t *testing.T) {
	got := chatWaitWords("agy", chatWaitTrust, false, false, chatReviewAsk{})
	if !strings.Contains(got, "Waiting for you: agy asks to trust this folder") || !strings.Contains(got, "F11") || !strings.Contains(got, "Yes") {
		t.Fatalf("unexpected trust message: %q", got)
	}
	gotQueued := chatWaitWords("agy", chatWaitTrust, true, false, chatReviewAsk{})
	if !strings.Contains(gotQueued, "Your message stays queued") {
		t.Fatalf("queued trust message missing queue notice: %q", gotQueued)
	}
	gotPerm := chatWaitWords("claude", chatWaitPermission, true, false, chatReviewAsk{})
	if !strings.Contains(gotPerm, "Waiting for you: claude asks permission to continue") || !strings.Contains(gotPerm, "F11") {
		t.Fatalf("unexpected permission message: %q", gotPerm)
	}
}

func TestChatNetworkError(t *testing.T) {
	const agyErr = "There was a network issue connecting to the server, please try again"
	for name, tc := range map[string]struct {
		frame  string
		before string
		sent   string
		want   string
	}{
		"agy network error after sent": {
			frame:  "agy\n> build a todo app\n" + agyErr + "\n> \n",
			before: "agy\n> \n",
			sent:   "build a todo app",
			want:   agyErr,
		},
		"wrapped agy network error in narrow pane": {
			frame:  "agy\n> build a todo app\nThere was a network issue connecting to the\nserver, please try again\n> \n",
			before: "agy\n> \n",
			sent:   "build a todo app",
			want:   agyErr,
		},
		"old error above clean retry is ignored": {
			frame:  "agy\n> build a todo app\n" + agyErr + "\n> build a todo app\nDone! I built it.\n> \n",
			before: "agy\n> build a todo app\n" + agyErr + "\n> \n",
			sent:   "build a todo app",
			want:   "",
		},
		"old error above running retry is ignored": {
			frame:  "agy\n> build a todo app\n" + agyErr + "\n> build a todo app\nThinking (esc to interrupt)\n",
			before: "agy\n> build a todo app\n" + agyErr + "\n> \n",
			sent:   "build a todo app",
			want:   "",
		},
		"second error after retry is detected": {
			frame:  "agy\n> build a todo app\n" + agyErr + "\n> build a todo app\n" + agyErr + "\n> \n",
			before: "agy\n> build a todo app\n" + agyErr + "\n> \n",
			sent:   "build a todo app",
			want:   agyErr,
		},
		"unchanged frame is ignored": {
			frame:  "agy\n> build a todo app\n" + agyErr + "\n> \n",
			before: "agy\n> build a todo app\n" + agyErr + "\n> \n",
			sent:   "build a todo app",
			want:   "",
		},
		"short message does not match substring inside error": {
			frame:  "agy\n> in\n" + agyErr + "\n> \n",
			before: "agy\n> \n",
			sent:   "in",
			want:   agyErr,
		},
		"collapsed paste compares error counts": {
			frame:  "agy\n> [Pasted text #1 +5 lines]\n" + agyErr + "\n> [Pasted text #2 +5 lines]\n" + agyErr + "\n> \n",
			before: "agy\n> [Pasted text #1 +5 lines]\n" + agyErr + "\n> \n",
			sent:   "a very long pasted prompt that was collapsed",
			want:   agyErr,
		},
		"collapsed paste ignores stale error": {
			frame:  "agy\n> [Pasted text #1 +5 lines]\n" + agyErr + "\n> [Pasted text #2 +5 lines]\nDone.\n> \n",
			before: "agy\n> [Pasted text #1 +5 lines]\n" + agyErr + "\n> \n",
			sent:   "a very long pasted prompt that was collapsed",
			want:   "",
		},
		"briefing turn error with no sent text": {
			frame:  "agy\n" + agyErr + "\n> \n",
			before: "",
			sent:   "",
			want:   agyErr,
		},
		"old error above recovered turn is ignored without sent text": {
			frame:  "agy\n> netfail2\n" + agyErr + "\n> recover\ngot:recover\nok:recover\n> \n",
			before: "",
			sent:   "",
			want:   "",
		},
	} {
		if got := chatNetworkError(tc.frame, tc.before, tc.sent); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
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

func lastBoardOutput(w *Workspace) string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state.LastOutput
}

// When the provider CLI sits on a trust or permission prompt waiting for the
// owner, the Board says what is waiting for whom (instead of "Marshal chat
// busy") and keeps the queued message until the owner answers in the chat.
func TestBoardSayShowsProviderTrustAndPermissionPrompts(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	w := realTmuxWorkspace(t)
	t.Cleanup(w.Close)
	w.screen = NewScreen(NewTerminal(strings.NewReader(""), &strings.Builder{}))
	w.homeBoard = true
	oldPoll, oldSettle, oldTurn := sayPoll, saySettle, sayTurnSettle
	sayPoll, saySettle, sayTurnSettle = 50*time.Millisecond, 80*time.Millisecond, 200*time.Millisecond
	defer func() { sayPoll, saySettle, sayTurnSettle = oldPoll, oldSettle, oldTurn }()

	script := `printf 'Do you trust the contents of this project?\n\n❯ Yes, I trust the contents of this project\n  No, exit\n'
IFS= read -r _
printf '\033[2J\033[Hready\n> '
while IFS= read -r l; do
  printf 'got:%s\n' "$l"
  if [ "$l" = "need-perm" ]; then
    printf 'Do you want to proceed?\n❯ 1. Yes\n  2. No\n'
    IFS= read -r _
    printf 'allowed\n'
  fi
  printf '> '
done`
	var pane string
	if err := tmux.NewWindow(context.Background(), w.tmuxSession, "marshal-chat-agy", w.workDir, nil, []string{"sh", "-c", script}, &pane); err != nil {
		t.Fatal(err)
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", paneID: pane, window: "marshal-chat-agy", provider: "antigravity"}
	w.tmuxMu.Unlock()
	waitForTmuxOutput(t, pane, "Do you trust the contents of this project?")

	ctx := context.Background()
	w.sayToMarshal(ctx, "need-perm")
	waitFor(t, func() bool {
		return strings.Contains(lastBoardOutput(w), "Waiting for you: agy asks to trust this folder")
	}, func() string { return fmt.Sprintf("output %q echo %+v", lastBoardOutput(w), w.sayEchoList()) })
	if strings.Contains(lastBoardOutput(w), "Marshal chat busy") {
		t.Fatalf("Board said busy instead of waiting on trust: %q", lastBoardOutput(w))
	}
	if !strings.Contains(lastBoardOutput(w), "F11") {
		t.Fatalf("Board did not tell owner how to open chat: %q", lastBoardOutput(w))
	}
	if out, _ := tmux.CapturePane(ctx, pane); strings.Contains(out, "need-perm") {
		t.Fatalf("typed into trust prompt:\n%s", out)
	}

	// Owner accepts the trust prompt in the chat pane.
	if _, err := tmux.RunCommand(ctx, "send-keys", "-t", pane, "Enter"); err != nil {
		t.Fatal(err)
	}
	waitForTmuxOutput(t, pane, "got:need-perm")
	waitFor(t, func() bool { return echoState(w, "need-perm") == saySent }, func() string { return fmt.Sprintf("%+v", w.sayEchoList()) })

	// Now the provider asks for permission mid-turn; even before another
	// message is queued, the Board tells the owner it is waiting on them.
	waitForTmuxOutput(t, pane, "Do you want to proceed?")
	waitFor(t, func() bool {
		return strings.Contains(lastBoardOutput(w), "Waiting for you: agy asks permission to continue")
	}, func() string { return fmt.Sprintf("output %q", lastBoardOutput(w)) })
	// Queue a follow-up while the permission prompt is already shown; it must
	// not overwrite the prompt notice with "Marshal chat busy".
	w.sayToMarshal(ctx, "after-perm")
	waitFor(t, func() bool { return echoState(w, "after-perm") == sayQueued }, func() string { return echoState(w, "after-perm") })
	if strings.Contains(lastBoardOutput(w), "Marshal chat busy") {
		t.Fatalf("queued message overwrote permission notice with busy: %q", lastBoardOutput(w))
	}
	if _, err := tmux.RunCommand(ctx, "send-keys", "-t", pane, "Enter"); err != nil {
		t.Fatal(err)
	}
	waitForTmuxOutput(t, pane, "got:after-perm")
	waitFor(t, func() bool { return echoState(w, "after-perm") == saySent }, func() string { return fmt.Sprintf("%+v", w.sayEchoList()) })
}

// When a turn ends on a provider network error, the Board shows it and
// Marshal re-sends the last message once automatically while keeping queued
// messages queued instead of dropping them. A second network error stops
// retrying and keeps queued messages queued until the chat moves on.
func TestBoardSayRetriesNetworkErrorOnceAndKeepsQueued(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	w := realTmuxWorkspace(t)
	t.Cleanup(w.Close)
	w.screen = NewScreen(NewTerminal(strings.NewReader(""), &strings.Builder{}))
	w.homeBoard = true
	oldPoll, oldSettle, oldTurn := sayPoll, saySettle, sayTurnSettle
	sayPoll, saySettle, sayTurnSettle = 50*time.Millisecond, 80*time.Millisecond, 200*time.Millisecond
	defer func() { sayPoll, saySettle, sayTurnSettle = oldPoll, oldSettle, oldTurn }()

	logFile := filepath.Join(t.TempDir(), "received.log")
	script := fmt.Sprintf(`n1=0
printf 'agy stub\n> '
while IFS= read -r l; do
  printf '%%s\n' "$l" >> %q
  printf 'got:%%s\n' "$l"
  case "$l" in
    netfail1)
      n1=$((n1 + 1))
      printf 'Working (esc to interrupt)'
      sleep 0.4
      printf '\r\033[K'
      if [ "$n1" -eq 1 ]; then
        printf 'There was a network issue connecting to the server, please try again\n'
      else
        printf 'ok:netfail1\n'
      fi
      ;;
    netfail2)
      printf 'Working (esc to interrupt)'
      sleep 0.3
      printf '\r\033[KThere was a network issue connecting to the server, please try again\n'
      ;;
    *)
      printf 'ok:%%s\n' "$l"
      ;;
  esac
  printf '> '
done`, logFile)
	var pane string
	if err := tmux.NewWindow(context.Background(), w.tmuxSession, "marshal-chat-net", w.workDir, nil, []string{"sh", "-c", script}, &pane); err != nil {
		t.Fatal(err)
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", paneID: pane, window: "marshal-chat-net", provider: "antigravity"}
	w.tmuxMu.Unlock()
	waitForTmuxOutput(t, pane, "agy stub")

	ctx := context.Background()
	readLog := func() []string {
		data, err := os.ReadFile(logFile)
		if err != nil {
			return nil
		}
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" {
			return nil
		}
		return strings.Split(trimmed, "\n")
	}

	// Part 1: first turn hits a network error while another message is queued;
	// Marshal retries netfail1 once, then sends queued-after-netfail.
	w.sayToMarshal(ctx, "netfail1")
	waitForTmuxOutput(t, pane, "esc to interrupt")
	w.sayToMarshal(ctx, "queued-after-netfail")
	waitFor(t, func() bool { return echoState(w, "queued-after-netfail") == sayQueued }, func() string { return echoState(w, "queued-after-netfail") })

	waitFor(t, func() bool {
		out := lastBoardOutput(w)
		return strings.Contains(out, "There was a network issue connecting to the server, please try again") &&
			strings.Contains(out, "sent the last message again")
	}, func() string {
		p, _ := tmux.CapturePane(ctx, pane)
		return fmt.Sprintf("output %q log %v pane:\n%s", lastBoardOutput(w), readLog(), p)
	})
	waitForTmuxOutput(t, pane, "ok:netfail1")
	waitForTmuxOutput(t, pane, "ok:queued-after-netfail")
	waitFor(t, func() bool { return echoState(w, "queued-after-netfail") == saySent }, func() string { return echoState(w, "queued-after-netfail") })
	if got := strings.Join(readLog(), ","); got != "netfail1,netfail1,queued-after-netfail" {
		t.Fatalf("unexpected delivery order: %q", got)
	}

	// Part 2: retry also fails -> Marshal stops after 1 retry, tells the owner,
	// and keeps still-queued queued until the owner recovers in the chat.
	w.sayToMarshal(ctx, "netfail2")
	waitForTmuxOutput(t, pane, "got:netfail2")
	w.sayToMarshal(ctx, "still-queued")
	waitFor(t, func() bool {
		out := lastBoardOutput(w)
		return strings.Contains(out, "Network error again") && strings.Contains(out, "F11")
	}, func() string {
		p, _ := tmux.CapturePane(ctx, pane)
		return fmt.Sprintf("output %q log %v pane:\n%s", lastBoardOutput(w), readLog(), p)
	})
	time.Sleep(400 * time.Millisecond)
	if got := strings.Join(readLog(), ","); got != "netfail1,netfail1,queued-after-netfail,netfail2,netfail2" {
		t.Fatalf("retried more than once or sent queued message into broken chat: %q", got)
	}
	if state := echoState(w, "still-queued"); state != sayQueued {
		t.Fatalf("still-queued state = %q, want %q", state, sayQueued)
	}

	// Owner opens the chat and runs a clean turn; still-queued then delivers.
	if _, err := tmux.RunCommand(ctx, "send-keys", "-t", pane, "recover", "Enter"); err != nil {
		t.Fatal(err)
	}
	waitForTmuxOutput(t, pane, "ok:recover")
	waitForTmuxOutput(t, pane, "ok:still-queued")
	waitFor(t, func() bool { return echoState(w, "still-queued") == saySent }, func() string { return echoState(w, "still-queued") })
}
