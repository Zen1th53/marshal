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
	"run.pause", "run.resume", "app.doctor", "app.quit",
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

// Pause and resume are the user's run controls in the palette, and Ctrl+X's
// stop-all is a row too. Each row is on only when its command can act: pause
// while exactly one run of the session is running, resume while exactly one
// is paused; otherwise the row says why.
func TestShippedPaletteRunControls(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	row := func(id string) PaletteAction {
		t.Helper()
		for _, a := range w.palette.refresh() {
			if a.ID == id {
				return a
			}
		}
		t.Fatalf("the palette has no %s row", id)
		return PaletteAction{}
	}
	if stop := row("workers.stop"); stop.Command != "/stop all" || stop.Shortcut != "Ctrl+X" || stop.Disabled {
		t.Fatalf("stop-all row: %+v", stop)
	}
	for _, c := range []struct {
		pause, resume int32
		pauseOn       bool
		pauseWhy      string
		resumeOn      bool
		resumeWhy     string
	}{
		{0, 0, false, "No run is running to pause.", false, "No run is paused."},
		{1, 0, true, "", false, "No run is paused."},
		{0, 1, false, "No run is running to pause.", true, ""},
		{2, 2, false, "More than one run is running; type /pause run:<id>.", false, "More than one run is paused; type /resume run:<id>."},
	} {
		w.runPausable.Store(c.pause)
		w.runResumable.Store(c.resume)
		p, r := row("run.pause"), row("run.resume")
		if p.Command != "/pause" || r.Command != "/resume" {
			t.Fatalf("run rows run %q and %q", p.Command, r.Command)
		}
		if p.Disabled == c.pauseOn || p.Reason != c.pauseWhy || r.Disabled == c.resumeOn || r.Reason != c.resumeWhy {
			t.Errorf("pausable %d resumable %d: pause %v %q, resume %v %q", c.pause, c.resume, !p.Disabled, p.Reason, !r.Disabled, r.Reason)
		}
	}
}
