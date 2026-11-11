package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/inspector"
)

var writeBoardANS = flag.Bool("board-ans", false, "write Board ANSI frames to ../../out")

func demoBoard(now time.Time) model.Board {
	b := model.Board{Run: model.RunHead{ID: "R-7f3a", Title: "MCP memory PoC", State: marshal.Dispatching, Done: 12, Running: 9, ActiveLanes: 9, LaneCap: 12, Total: 53, TokenLimit: 100000, Tokens: marshal.Amount{Value: 61000, Known: true}}, PlanID: "v2", Budget: marshal.Budget{Tokens: marshal.Ceiling{Task: 40000}}}
	type row struct {
		id, worker, scope string
		state             model.TaskState
		seconds, rw       int
	}
	data := []row{
		{"T-009", "opencode-1", "internal/mcp/events_list.go", model.TaskMerged, 760, 0},
		{"T-011", "codex-2", "internal/mcp/run_digest*.go", "verifying", 543, 0},
		{"T-017", "codex-1", "internal/mcp/memory_recall.go", model.TaskWorking, 252, 0},
		{"T-018", "claude-1", "internal/app/memory_candidates*.go", model.TaskWorking, 238, 0},
		{"T-019", "agy-1", "internal/store/memory_restore.go", model.TaskWorking, 221, 0},
		{"T-020", "codex-1", "internal/app/memory_bridge.go", model.TaskReview, 380, 0},
		{"T-023", "codex-3", "internal/netpolicy/broker_npm.go", model.TaskParked, 127, 0},
		{"T-024", "claude-2", "internal/app/credential_lease.go", model.TaskRework, 475, 1},
		{"T-027", "opencode-2", "internal/tui/memory_review_extreme.go", model.TaskWorking, 72, 0},
		{"T-031", "agy-2", "scripts/release/push_tags.sh", model.TaskParked, 40, 0},
		{"T-033", "", "internal/store/migrations_canonical.go", model.TaskQueued, 0, 0},
		{"T-034", "", "docs/mcp-memory.md", model.TaskQueued, 0, 0},
	}
	for _, d := range data {
		t := model.TaskRow{ID: d.id, Worker: d.worker, Title: d.scope, Scope: []string{d.scope}, State: d.state, Returns: d.rw, ReworkLimit: 2}
		if d.id == "T-023" {
			t.Title = "npm broker"
			t.AuthorizedHosts = []string{"api.openai.com", "github.com"}
			t.AuthorizedTools = []string{"go", "git (read)", "rg"}
			t.EvidenceSummary = "1 claim · 0 SUPPORTED · 1 UNSUPPORTED"
		}
		if d.seconds > 0 {
			t.Since = now.Add(-time.Duration(d.seconds) * time.Second)
		}
		b.Tasks = append(b.Tasks, t)
	}
	type lane struct {
		worker, task, state, last string
		tokens                    int64
		sec                       int
	}
	ld := []lane{{"codex-1", "T-017", "working", "edit  mcp/memory_recall.go:212", 41000, 252}, {"claude-1", "T-018", "working", "test  go test ./internal/app/...", 28000, 238}, {"agy-1", "T-019", "working", "read  store/memory_restore.go", 19000, 221}, {"codex-2", "T-011", "review", "verify · fresh session · integration a91f2c", 63000, 543}, {"codex-1", "T-020", "review", "review · 4 claims · 3 SUPPORTED 1 UNSUPP", 52000, 380}, {"codex-3", "T-023", "parked", "parked · egress registry.npmjs.org", 12000, 127}, {"claude-2", "T-024", "rework", "rework 1/2 · lease must cover invocation", 52000, 475}, {"opencode-2", "T-027", "working", "edit  tui/memory_review_extreme.go:88", 11000, 72}, {"agy-2", "T-031", "parked", "probe · harness verify agy 1.4.10", 4000, 40}}
	for _, d := range ld {
		l := model.Lane{Worker: d.worker, Provider: strings.Split(d.worker, "-")[0], TaskID: d.task, State: model.LaneState(d.state), Started: now.Add(-time.Duration(d.sec) * time.Second), Tokens: d.tokens, Recent: [8]int64{1, 3, 2, 5, 4, 7, 6, 8}, Last: d.last}
		if d.task == "T-023" {
			// The parked lane's worker pane, for the inspector's [o].
			l.Pane = "%7"
		}
		b.Lanes = append(b.Lanes, l)
	}
	e := model.NewInboxItem(permission.Request{Kind: "network", TaskID: "T-023", Object: "registry.npmjs.org:443", Reason: "taint U · outside manifest · blast: 1 task, 0 files"})
	g := model.NewInboxItem(permission.Request{Kind: "marshal-command", TaskID: "T-031", Object: "git push --force origin main", Reason: "irreversible · type canonical id to confirm"})
	g.Canonical = "task.T-031.release-push"
	b.Inbox = []model.InboxItem{e, g}
	for _, a := range []struct{ at, id, state, detail string }{{"14:02:29", "T-009", "merged", "→ integration a91f2c · 7 evidence refs"}, {"14:02:14", "T-023", "parked", "egress registry.npmjs.org outside manifest (taint U)"}, {"14:01:50", "T-024", "returned", "review: lease ended mid-invocation → rework 1/2"}, {"14:01:02", "T-011", "verifying", "fresh codex session · pre_delivery"}} {
		clock, _ := time.Parse("15:04:05", a.at)
		// The demo's clock times on the demo's day, so the run reads as live.
		tm := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), clock.Second(), 0, now.Location())
		b.Activity = append(b.Activity, model.ActivityLine{At: tm, TaskID: a.id, State: model.TaskState(a.state), Detail: a.detail})
	}
	return b
}

func TestBoardDemoGoldens(t *testing.T) {
	// The goldens hold the 24-bit palette. The Board picks its palette from
	// COLORTERM, which a terminal sets and a CI runner does not, so pin it.
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("BOARD_TRUECOLOR", "")
	now := time.Date(2026, 11, 2, 14, 2, 31, 0, time.UTC)
	b := demoBoard(now)
	th := NewTheme(ThemeDefault, true, true)
	v := boardVisual{board: b, now: now, theme: th, frame: anim.Frame{}, selectedTask: "T-023", selectedInbox: b.Inbox[0].Request.Key(), focus: layout.Tasks, edition: "Marshal", marshalLeads: true, health: map[string]string{"codex": "●", "claude": "●", "agy": "◐", "opencode": "●"}}
	cases := []struct {
		name    string
		w, h    int
		mode    layout.Mode
		inspect bool
		chips   bool
	}{{"chips-split-120x40", 120, 40, layout.Split, false, true}, {"chips-split-80x24", 80, 24, layout.Split, false, true}, {"split-56x24", 56, 24, layout.Split, false, false}, {"split-56x29", 56, 29, layout.Split, false, false}, {"split-56x29-empty", 56, 29, layout.Split, false, false}, {"split-80x24", 80, 24, layout.Split, false, false}, {"split-100x30", 100, 30, layout.Split, false, false}, {"split-120x40", 120, 40, layout.Split, false, false}, {"split-170x45", 170, 45, layout.Split, false, false}, {"split-200x50", 200, 50, layout.Split, false, false}, {"home-80x24", 80, 24, layout.Home, false, false}, {"home-100x30", 100, 30, layout.Home, false, false}, {"home-120x40", 120, 40, layout.Home, false, false}, {"home-170x45", 170, 45, layout.Home, false, false}, {"home-200x50", 200, 50, layout.Home, false, false}, {"inspector-120x40", 120, 40, layout.Split, true, false}, {"inspector-80x24", 80, 24, layout.Split, true, false},
		// TUI3-0012: the ultrawide terminal, three columns with the
		// inspector docked, alone and beside the Marshal chat.
		{"home-200x60", 200, 60, layout.Home, false, false}, {"split-200x60", 200, 60, layout.Split, false, false}, {"inspector-200x60", 200, 60, layout.Home, true, false}, {"chips-home-200x60", 200, 60, layout.Home, false, true},
		// The run waits for the operator: the header says so, the footer
		// gives the reason and Enter action, the inbox leads with it.
		{"stop-home-170x30", 170, 30, layout.Home, false, false}, {"stop-home-80x24", 80, 24, layout.Home, false, false}, {"stop-split-100x30", 100, 30, layout.Split, false, false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := v
			if strings.HasSuffix(tc.name, "-empty") {
				// A run that has planned its tasks but has no lanes,
				// activity or decisions yet.
				v.board.Lanes, v.board.Inbox, v.board.Activity = nil, nil, nil
				v.board.Run.ActiveLanes = 0
				v.selectedInbox = ""
			}
			if strings.HasPrefix(tc.name, "stop-") {
				v.board.Run.State = marshal.AwaitingUser
				v.board.Run.Paused = "verification stopped: go test ./internal/netpolicy/... failed (exit 1)"
				v.board.Run.Next = []string{"Press Enter to continue."}
			}
			l := layout.ComputeFor(tc.w, tc.h, tc.mode, v.layoutCounts())
			if tc.chips {
				// The header with the host chip.
				v.arch = true
				host := demoHost(t)
				v.host = &host
			}
			v.hints = goldenHints(v)
			var frame []string
			if tc.inspect {
				frame = paintBoardInspector(l, th, inspector.New(b, "T-023"), v.render, nil)
			} else {
				frame = paintBoard(l, th, v.render)
			}
			got := strings.Join(frame, "\n") + "\n"
			path := filepath.Join("testdata", "board", "demo-"+tc.name+".golden")
			if *updateBoardGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("%s differs; run with -update-board-golden", path)
			}
			if *writeBoardANS {
				out := filepath.Join("..", "..", "out", tc.name+".ans")
				if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(out, []byte(got), 0644); err != nil {
					t.Fatal(err)
				}
			}
			for i, line := range frame {
				if n := VisibleLen(line); n != tc.w {
					t.Errorf("row %d width %d, want %d", i, n, tc.w)
				}
			}
		})
	}
}

// boardCounts is what the live Board passes the layout for b.
func boardCounts(b model.Board) layout.Counts {
	return layout.Counts{Lanes: len(b.Lanes), Tasks: len(b.Tasks), Inbox: len(b.Inbox), Activity: len(b.Activity)}
}

func TestBoardHeaderMockupWidths(t *testing.T) {
	now := time.Date(2026, 11, 2, 14, 2, 31, 0, time.UTC)
	v := boardVisual{board: demoBoard(now), now: now, theme: NewTheme(ThemeNoColor, true, true), edition: "Marshal", marshalLeads: true, health: map[string]string{"codex": "●", "claude": "●", "agy": "◐", "opencode": "●"}}
	for _, tc := range []struct {
		width int
		want  []string
		omit  []string
	}{
		{80, []string{"Marshal", "MCP memory PoC", "running", "12/53", "▰▰▰▰▰▱▱▱", "⚑2"}, []string{"codex", "14:02:31"}},
		{100, []string{"Marshal", "MCP memory PoC", "running", "12/53", "▰▰▰▰▰▱▱▱", "inbox 2"}, []string{"codex", "14:02:31"}},
		{170, []string{"12/53 done", "budget", "61%", "14:02:31"}, []string{"codex ●", "lanes"}},
		{200, []string{"12/53 done", "budget", "61%", "14:02:31"}, []string{"codex ●", "lanes"}},
	} {
		header := StripANSI(v.header(tc.width))
		if got := VisibleLen(header); got != tc.width {
			t.Errorf("width %d: got %d cells", tc.width, got)
		}
		for _, want := range tc.want {
			if !strings.Contains(header, want) {
				t.Errorf("width %d: missing %q in %q", tc.width, want, header)
			}
		}
		for _, omit := range tc.omit {
			if strings.Contains(header, omit) {
				t.Errorf("width %d: unexpected %q in %q", tc.width, omit, header)
			}
		}
	}
}

func TestBoardDemoASCIIAndNoColor(t *testing.T) {
	now := time.Date(2026, 11, 2, 14, 2, 31, 0, time.UTC)
	b := demoBoard(now)
	v := boardVisual{board: b, now: now, theme: NewTheme(ThemeNoColor, false, false), selectedTask: "T-023", selectedInbox: b.Inbox[0].Request.Key(), focus: layout.Tasks, edition: "Marshal", marshalLeads: true}
	l := layout.Compute(80, 24, layout.Split)
	for y, line := range paintBoard(l, v.theme, v.render) {
		if strings.Contains(line, "\x1b") {
			t.Fatalf("row %d has ANSI in NO_COLOR", y)
		}
		if VisibleLen(line) != 80 {
			t.Fatalf("row %d width %d", y, VisibleLen(line))
		}
		for _, r := range line {
			if r > 127 {
				t.Fatalf("row %d has non-ASCII glyph %q", y, r)
			}
		}
	}
}
