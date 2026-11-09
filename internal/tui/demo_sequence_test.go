package tui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

var writeDemoSeq = flag.Bool("demo-seq", false, "write lifecycle demo ANSI frames to ../../out/demo")

// TestWriteDemoSequence renders the run lifecycle as an ordered set of Board
// frames for the project's demo GIF: plan with one model, build in parallel,
// review by a different model, verify, and merge only what passed. It writes
// nothing unless -demo-seq is set, so it is inert in normal runs.
func TestWriteDemoSequence(t *testing.T) {
	if !*writeDemoSeq {
		t.Skip("set -demo-seq to write demo frames")
	}
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("BOARD_TRUECOLOR", "")
	now := time.Date(2026, 11, 2, 14, 2, 31, 0, time.UTC)
	th := NewTheme(ThemeDefault, true, true)
	const w, h = 120, 40

	type stage struct {
		name  string
		apply func(b *boardVisual)
	}
	stages := []stage{
		{"1-plan", func(v *boardVisual) {
			v.board.Run.State = marshal.Drafting
			v.board.Run.Done, v.board.Run.Running, v.board.Run.ActiveLanes = 0, 0, 0
			v.board.Lanes, v.board.Activity = nil, nil
		}},
		{"2-build", func(v *boardVisual) {
			v.board.Run.State = marshal.Dispatching
		}},
		{"3-review", func(v *boardVisual) {
			v.board.Run.State = marshal.Reviewing
		}},
		{"4-verify", func(v *boardVisual) {
			v.board.Run.State = marshal.Verifying
		}},
		{"5-merge", func(v *boardVisual) {
			v.board.Run.State = marshal.Merging
			v.board.Run.Done, v.board.Run.Running = 15, 7
		}},
	}

	outDir := filepath.Join("..", "..", "out", "demo")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, s := range stages {
		b := demoBoard(now)
		// The public edition runs workers on the host: egress is recorded, not
		// enforced, and there is no runtime command hard-gate. Drop the inbox
		// gates and the governance activity (egress, lease) so the demo shows
		// only what this edition actually does — the orchestration lifecycle.
		b.Inbox = nil
		act := b.Activity[:0]
		for _, a := range b.Activity {
			if strings.Contains(a.Detail, "egress") || strings.Contains(a.Detail, "lease") {
				continue
			}
			act = append(act, a)
		}
		b.Activity = act
		v := boardVisual{board: b, now: now, theme: th, frame: anim.Frame{}, selectedTask: "T-017", focus: layout.Tasks, edition: "Marshal", health: map[string]string{"codex": "●", "claude": "●", "agy": "◐", "opencode": "●"}}
		s.apply(&v)
		l := layout.ComputeFor(w, h, layout.Home, v.layoutCounts())
		frame := paintBoard(l, th, v.render)
		got := strings.Join(frame, "\n") + "\n"
		path := filepath.Join(outDir, s.name+".ans")
		if err := os.WriteFile(path, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("wrote %s (%d lines)\n", path, len(frame))
	}
}
