package lanes

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

var updateGolden = flag.Bool("update-lanes-golden", false, "rewrite testdata/*.golden")

func fixedTime() time.Time {
	return time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)
}

func sample9Lanes(now time.Time) []model.Lane {
	return []model.Lane{
		{
			Worker:   "codex",
			Provider: "codex",
			TaskID:   "T-01",
			Pane:     "%1",
			State:    model.LaneWorking,
			Started:  now.Add(-18 * time.Second),
			Tokens:   41200,
			Recent:   [8]int64{100, 500, 1200, 3000, 8000, 15000, 28000, 41200},
			Last:     "edit schema.proto",
		},
		{
			Worker:   "claude",
			Provider: "claude",
			TaskID:   "T-02",
			Pane:     "%2",
			State:    model.LaneWorking,
			Started:  now.Add(-45 * time.Second),
			Tokens:   20500,
			Recent:   [8]int64{500, 2000, 5000, 10000, 12000, 16000, 19000, 20500},
			Last:     "run tests ./internal/tui/...",
		},
		{
			Worker:   "omp",
			Provider: "omp",
			TaskID:   "T-03",
			Pane:     "%3",
			State:    model.LaneWorking,
			Started:  now.Add(-2 * time.Minute),
			Tokens:   18000,
			Recent:   [8]int64{2000, 4000, 6000, 8000, 10000, 12000, 15000, 18000},
			Last:     "review PR test coverage",
		},
		{
			Worker:   "agy",
			Provider: "antigravity",
			TaskID:   "T-04",
			Pane:     "%4",
			State:    model.LaneWorking,
			Started:  now.Add(-12 * time.Second),
			Tokens:   6400,
			Recent:   [8]int64{200, 400, 800, 1600, 3200, 4800, 5600, 6400},
			Last:     "git diff inspection",
		},
		{
			Worker:   "codex-2",
			Provider: "codex",
			TaskID:   "T-05",
			Pane:     "%5",
			State:    model.LaneWorking,
			Started:  now.Add(-5 * time.Minute),
			Tokens:   85000,
			Recent:   [8]int64{10000, 20000, 35000, 50000, 62000, 71000, 79000, 85000},
			Last:     "implement OSC 7501 ingestion",
		},
		{
			Worker:   "claude-2",
			Provider: "claude",
			TaskID:   "T-06",
			Pane:     "%6",
			State:    model.LaneParked,
			Started:  now.Add(-15 * time.Minute),
			Tokens:   12000,
			Recent:   [8]int64{12000, 12000, 12000, 12000, 12000, 12000, 12000, 12000},
			Last:     "parked waiting for gate",
		},
		{
			Worker:   "tern",
			Provider: "tern",
			TaskID:   "T-07",
			Pane:     "%7",
			State:    model.LaneRework,
			Started:  now.Add(-3 * time.Minute),
			Tokens:   34000,
			Recent:   [8]int64{5000, 10000, 15000, 20000, 25000, 28000, 31000, 34000},
			Last:     "rework: fix race condition",
		},
		{
			Worker:   "orca",
			Provider: "orca",
			TaskID:   "T-08",
			Pane:     "%8",
			State:    model.LaneReview,
			Started:  now.Add(-4 * time.Minute),
			Tokens:   27000,
			Recent:   [8]int64{1000, 3000, 7000, 12000, 18000, 21000, 24000, 27000},
			Last:     "verifying acceptance gates",
		},
		{
			Worker:   "devin",
			Provider: "devin",
			TaskID:   "T-09",
			Pane:     "%9",
			State:    model.LaneIdle,
			Started:  now.Add(-25 * time.Minute),
			Tokens:   0,
			Recent:   [8]int64{},
			Last:     "idle awaiting dispatch",
		},
	}
}

// Golden frame with 9 lanes
func TestGoldenFrame9Lanes(t *testing.T) {
	now := fixedTime()
	lanes := sample9Lanes(now)
	b := model.Board{Lanes: lanes}

	p := layout.Pane{
		ID:      layout.Lanes,
		Rect:    layout.Rect{X: 102, Y: 1, W: 68, H: 12},
		Density: layout.Full,
	}

	opt := Options{
		Now:       now,
		FrameTick: 0,
		UTF8:      true,
	}

	lines := Render(p, b, opt)

	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l + "|\n")
	}

	goldenPath := filepath.Join("testdata", "lanes-9lanes-full.golden")
	if *updateGolden {
		_ = os.MkdirAll(filepath.Dir(goldenPath), 0755)
		if err := os.WriteFile(goldenPath, []byte(sb.String()), 0644); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("%v (run with -update-lanes-golden to create it)", err)
	}

	if sb.String() != string(want) {
		t.Errorf("9 lanes golden mismatch:\nGOT:\n%s\nWANT:\n%s", sb.String(), string(want))
	}
}

// Redaction test: a secret in last action never reaches the frame
func TestRedactionInLastAction(t *testing.T) {
	now := fixedTime()
	secret := "ghp_VerySecretGitHubToken1234567890abcdef"
	b := model.Board{
		Lanes: []model.Lane{
			{
				Worker:   "codex",
				Provider: "codex",
				TaskID:   "T-01",
				Pane:     "%1",
				State:    model.LaneWorking,
				Started:  now.Add(-10 * time.Second),
				Tokens:   1500,
				Last:     "curl -H 'Authorization: Bearer " + secret + "' https://api.github.com",
			},
		},
	}

	p := layout.Pane{
		ID:      layout.Lanes,
		Rect:    layout.Rect{X: 0, Y: 0, W: 80, H: 5},
		Density: layout.Full,
	}

	lines := Render(p, b, Options{Now: now, UTF8: true})
	rendered := strings.Join(lines, "\n")

	if strings.Contains(rendered, secret) {
		t.Fatalf("CRITICAL: Secret reached rendered frame! Output:\n%s", rendered)
	}
	if !strings.Contains(rendered, "[REDACTED]") {
		t.Errorf("Expected [REDACTED] in output, got:\n%s", rendered)
	}
}

// Invariant: A lane row never exceeds one line
func TestLaneRowNeverExceedsOneLine(t *testing.T) {
	now := fixedTime()
	longAction := strings.Repeat("very long action message that should be truncated cleanly ", 10)
	b := model.Board{
		Lanes: []model.Lane{
			{
				Worker:   "codex-worker-long",
				Provider: "codex",
				TaskID:   "TASK-LONG-01",
				Pane:     "%1",
				State:    model.LaneWorking,
				Started:  now.Add(-3600 * time.Second),
				Tokens:   12500000,
				Last:     longAction,
			},
		},
	}

	for _, width := range []int{30, 44, 60, 68, 80, 100, 120} {
		p := layout.Pane{
			ID:      layout.Lanes,
			Rect:    layout.Rect{X: 0, Y: 0, W: width, H: 3},
			Density: layout.Full,
		}
		lines := Render(p, b, Options{Now: now, UTF8: true})
		for idx, line := range lines {
			if strings.Contains(line, "\n") {
				t.Fatalf("width %d line %d contains newline", width, idx)
			}
			if w := visibleLen(line); w != width {
				t.Fatalf("width %d line %d has visible width %d", width, idx, w)
			}
		}
	}
}

// Three densities test: full, compact, strip
func TestThreeDensities(t *testing.T) {
	now := fixedTime()
	b := model.Board{Lanes: sample9Lanes(now)}

	// Strip density (80x1)
	stripLines := Render(layout.Pane{ID: layout.Lanes, Rect: layout.Rect{W: 80, H: 1}, Density: layout.Strip}, b, Options{Now: now, UTF8: true})
	if len(stripLines) != 1 {
		t.Fatalf("strip density produced %d lines, want 1", len(stripLines))
	}
	if !strings.HasPrefix(stripLines[0], "LANES") {
		t.Errorf("strip density line missing LANES prefix: %q", stripLines[0])
	}
	if visibleLen(stripLines[0]) != 80 {
		t.Errorf("strip density width %d, want 80", visibleLen(stripLines[0]))
	}

	// Compact density (60x8)
	compactLines := Render(layout.Pane{ID: layout.Lanes, Rect: layout.Rect{W: 60, H: 8}, Density: layout.Compact}, b, Options{Now: now, UTF8: true})
	if len(compactLines) != 8 {
		t.Fatalf("compact density produced %d lines, want 8", len(compactLines))
	}
	// 8 rows of 9 lanes -> 7 rows + "… 2 more"
	if !strings.Contains(compactLines[7], "… 2 more") {
		t.Errorf("compact overflow missing '… 2 more', got: %q", compactLines[7])
	}

	// Full density (68x12)
	fullLines := Render(layout.Pane{ID: layout.Lanes, Rect: layout.Rect{W: 68, H: 12}, Density: layout.Full}, b, Options{Now: now, UTF8: true})
	if len(fullLines) != 9 {
		t.Fatalf("full density produced %d lines, want 9", len(fullLines))
	}
}
