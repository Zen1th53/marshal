//go:build linux

package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
)

// claudeFixture is a script that draws Claude Code's input box the way
// v2.1 does ("❯" + NO-BREAK SPACE) holding its first argument, and on Enter
// redraws it empty under a SUBMITTED line.
const claudeFixture = `box() { printf '\342\235\257\302\240%s\n\342\224\200\342\224\200\342\224\200\n' "$1"; }
box "$1"; read x; clear; printf 'SUBMITTED\n\n'; box ""; exec sleep 600
`

// The live restart sequence for a chat pane that outlives its Board: the
// Board pasted into a resumed Claude Code chat, marked the pane, and was
// replaced before it saw the paste submitted. The new Board has no record
// of it, and no request open, yet the chat's monitor submits the
// placeholder its predecessor left, because the pane carries the mark. The
// owner's typing in a marked pane is never submitted.
func TestRealTmuxSubmitsPredecessorsMarkedPaste(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	w := realTmuxWorkspace(t)
	ctx := context.Background()
	open := func(content string) string {
		var pane string
		if err := tmux.NewWindow(ctx, w.tmuxSession, "chat-"+content[:3], w.workDir, nil, []string{"sh", "-c", claudeFixture, "sh", content}, &pane); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool {
			out, _ := tmux.RunCommand(ctx, "capture-pane", "-p", "-t", pane)
			got, ok := chatComposer(string(out))
			return ok && got == content
		})
		return pane
	}
	frame := func(pane string) string {
		out, _ := tmux.RunCommand(ctx, "capture-pane", "-p", "-t", pane)
		return string(out)
	}
	pane := open("[Pasted text #1]")
	chat := &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", provider: "claude", paneID: pane}
	w.tmuxMu.Lock()
	w.tmuxActiveWins[chat.id] = chat
	w.tmuxMu.Unlock()
	t.Cleanup(func() { forgetOwnPaste(pane) })

	// Unmarked: an orphan placeholder with nothing open is left alone.
	w.submitOwnLeftover(ctx, pane, false)
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(frame(pane), "SUBMITTED") {
		t.Fatal("unmarked placeholder submitted")
	}
	// The predecessor's mark makes it the Board's.
	markPanePaste(ctx, tmux.RunCommand, pane, "[Pasted text #1]")
	forgetOwnPaste(pane)
	w.submitOwnLeftover(ctx, pane, false)
	waitFor(t, func() bool { return strings.Contains(frame(pane), "SUBMITTED") }, func() string { return frame(pane) })
	if mark, ok := panePasteMark(ctx, tmux.RunCommand, pane); ok {
		t.Fatalf("mark left after submission: %q", mark)
	}

	// The owner's typing is never the Board's, marked pane or not.
	owner := open("fix the tests first")
	chat.paneID = owner
	t.Cleanup(func() { forgetOwnPaste(owner) })
	markPanePaste(ctx, tmux.RunCommand, owner, "")
	w.submitOwnLeftover(ctx, owner, false)
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(frame(owner), "SUBMITTED") {
		t.Fatal("owner's typing submitted")
	}
}

// The chat in its own window, never docked, holds Marshal's title against
// the kitty graphics APC Claude Code prints at start and after redraws: the
// guard is set on the chat pane itself, so tmux ignores the APC and OSC
// titles the CLI sends afterwards.
func TestRealTmuxUndockedChatTitleBeatsAPC(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	w := realTmuxWorkspace(t)
	ctx := context.Background()
	apc := `printf '\033_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\033\\'; printf '\033]2;✳ Claude Code\007'`
	script := apc + `; while [ ! -f "$1/again" ]; do sleep 0.05; done; ` + apc + `; touch "$1/done"; exec sleep 600`
	var pane string
	if err := tmux.NewWindow(ctx, w.tmuxSession, tmux.ChatWindowName(w.workDir), w.workDir, nil, []string{"sh", "-c", script, "sh", w.workDir}, &pane); err != nil {
		t.Fatal(err)
	}
	title := func() string {
		out, _ := tmux.RunCommand(ctx, "display-message", "-p", "-t", pane, "#{pane_title}")
		return strings.TrimSpace(string(out))
	}
	// As live: the CLI's APC became the title before Marshal looked.
	waitFor(t, func() bool { return title() == "✳ Claude Code" || title() == kittyQuery }, title)
	chat := &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", provider: "claude", paneID: pane}
	w.tmuxMu.Lock()
	w.tmuxActiveWins[chat.id] = chat
	w.tmuxMu.Unlock()
	if w.dockedChat() != "" {
		t.Fatal("chat docked; this models a chat in its own window")
	}
	want, _ := w.dockTitleWith(chat, w.dockStripNow(), 0, false)
	want = strings.TrimSpace(want)
	if err := w.holdMarshalChatTitle(ctx, chat, w.dockStripNow()); err != nil {
		t.Fatal(err)
	}
	if got := title(); got != want {
		t.Fatalf("title %q, want %q", got, want)
	}
	// Claude Code redraws and queries again.
	if err := os.WriteFile(filepath.Join(w.workDir, "again"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_, err := os.Stat(filepath.Join(w.workDir, "done"))
		return err == nil
	})
	// tmux 3.8 ignores the APC under the guard; 3.5 still takes it as the
	// title, and the next hold (the chat's monitor) puts the strip back.
	if err := w.holdMarshalChatTitle(ctx, chat, w.dockStripNow()); err != nil {
		t.Fatal(err)
	}
	if got := title(); got != want {
		t.Fatalf("provider retitled the chat: %q", got)
	}
	// tmux before 3.4 has no allow-set-title; there the hold above (checked
	// title, set back) is the whole guard.
	out, err := tmux.RunCommand(ctx, "show-options", "-p", "-v", "-t", pane, "allow-set-title")
	if err != nil {
		return // this tmux has no allow-set-title
	}
	if strings.TrimSpace(string(out)) != "off" {
		t.Fatalf("allow-set-title on the chat pane = %q (err %v)", out, err)
	}
}
