//go:build !governed

package tui

import (
	"slices"
	"testing"
)

// shippedPaletteAllowList is the reviewed list of what the shipped palette
// (Space / Ctrl+K) offers, in order: the Board's keys, then the user
// commands no key runs. A new row is a product decision: add it here only
// with the review, so the registry's internal menus (claims, evidence,
// budget, goal, team, policy…) cannot creep back.
var shippedPaletteAllowList = []string{
	"view.board", "view.host", "view.memory", "view.providers", "view.verify", "view.security", "view.plan", "view.architecture",
	"board.move", "board.inspect", "board.focus", "board.pane", "board.close", "inspector.events", "inspector.walk", "inspector.pane", "task.diff", "task.retry", "task.model",
	"inbox.reply", "inbox.decide", "inbox.reason", "run.stopped", "inbox.cancel", "inbox.hardgate",
	"memory.decide", "memory.scroll", "providers.verify", "providers.route", "verify.return", "security.decide", "plan.inspect", "arch.open",
	"marshal.pick", "marshal.chat", "marshal.say", "command.line", "palette", "help", "workers.stop", "interrupt",
	"fkey.help", "fkey.review", "fkey.diff", "fkey.status", "fkey.models", "fkey.mcp", "fkey.codex", "fkey.claude", "fkey.opencode", "fkey.update", "fkey.agy",
	"app.doctor", "app.quit",
}

func TestShippedPaletteAllowList(t *testing.T) {
	check := func(name string, actions []PaletteAction) {
		var got []string
		for _, a := range actions {
			got = append(got, a.ID)
		}
		if !slices.Equal(got, shippedPaletteAllowList) {
			t.Errorf("%s palette lists\n%q\nwant the reviewed list\n%q", name, got, shippedPaletteAllowList)
		}
	}
	// The palette as the workspace builds it and as it rebuilds on open.
	check("static", append(boardPaletteActions(nil), paletteRegistryActions()...))
	w, _ := boardWorkspace(t)
	defer w.Close()
	check("live", w.palette.refresh())
}
