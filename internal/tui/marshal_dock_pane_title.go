package tui

import (
	"context"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
)

// chatPaneTitle is what Marshal last made the Marshal chat pane's tmux title,
// and whether that pane is guarded against the provider setting its own.
//
// A provider CLI sets the pane title with OSC 0/2, and tmux also takes an APC
// string (ESC _ ... ESC \) as one. Claude Code sends a kitty graphics query
// as APC ("Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA") to learn whether the terminal
// draws images, and tmux made that the chat pane's title. Marshal owns this
// pane's title: it turns allow-set-title off for the pane, so nothing the CLI
// prints renames it, and sets the title itself (the v2 MARSHAL CHAT strip,
// or the classic title under MARSHAL_BOARD=classic).
type chatPaneTitle struct {
	pane    string
	title   string
	guarded bool // allow-set-title is off for pane (tmux 3.4+)
}

// holdChatPaneTitle makes pane's tmux title title and keeps it there. The
// guard is set once per pane; the title is set when it changes and whenever
// the pane shows another one (no allow-set-title before tmux 3.4, and APC
// titles that get past it in 3.5).
func holdChatPaneTitle(ctx context.Context, run chatRunner, h *chatPaneTitle, pane, title string) error {
	title = strings.TrimSpace(title)
	if pane == "" || title == "" {
		return nil
	}
	if h.pane != pane {
		*h = chatPaneTitle{pane: pane}
		_, err := run(ctx, "set-option", "-p", "-t", pane, "allow-set-title", "off")
		h.guarded = err == nil
		// The window name is Marshal's too; the provider's ESC k does not
		// rename it.
		_, _ = run(ctx, "set-option", "-w", "-t", pane, "allow-rename", "off")
	}
	if h.title == title {
		// Looked at even when guarded: tmux 3.5 still takes an APC string
		// as the title with allow-set-title off (3.8 does not), so the
		// guard alone does not keep Claude Code's graphics query out.
		out, err := run(ctx, "display-message", "-p", "-t", pane, "#{pane_title}")
		if err == nil && strings.TrimSpace(string(out)) == title {
			return nil
		}
	}
	if _, err := run(ctx, "select-pane", "-t", pane, "-T", title); err != nil {
		h.title = ""
		return err
	}
	h.title = title
	return nil
}

// holdMarshalChatTitle holds the Marshal chat pane's title for chat, with
// the v2 strip as it is now. It is called by reconcile (docked or not) and
// by the chat's monitor every few seconds, so a chat that stays in its own
// window, and is never docked, still gets the guard and the strip. A nil
// chat or one without a pane is left alone.
func (w *Workspace) holdMarshalChatTitle(ctx context.Context, chat *activeTmuxAgent, strip dockStrip) error {
	if chat == nil || !isPaneID(chat.paneID) {
		return nil
	}
	title, _ := w.dockTitleWith(chat, strip, 0, false)
	ctx, cancel := context.WithTimeout(ctx, dockWait)
	defer cancel()
	w.dock.titleMu.Lock()
	defer w.dock.titleMu.Unlock()
	run := func(ctx context.Context, args ...string) ([]byte, error) { return tmux.RunCommand(ctx, args...) }
	return holdChatPaneTitle(ctx, run, &w.dock.paneTitle, chat.paneID, title)
}

// chatTitleEvery is how often the chat's monitor holds its title.
const chatTitleEvery = 2 * time.Second
