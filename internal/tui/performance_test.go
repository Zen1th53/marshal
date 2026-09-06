package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Performance characteristics of frame composition.
//
// These measure the work a single repaint does, which is what bounds redraw cost
// under event load. They assert generous ceilings rather than exact timings, so
// they document the shape of the cost without becoming flaky on a loaded host.

func benchState(claims, messages int) UIState {
	s := UIState{
		ProjectID:          "PROJECT-perf",
		SessionID:          "sess-perf",
		SessionMode:        "ULTRA",
		UnderstandingState: model.GoalReady,
		Goal:               model.GoalContract{ID: "goal-perf", Revision: 3, DesiredOutcome: "measure frame cost"},
		GitStatus:          GitStatusResult{Branch: "main", Clean: true, Commit: "abc1234"},
		Participants: []model.Participant{
			{AgentID: "claude", Role: model.RoleArchitect, Harness: "claude", Model: UnknownModel, IsActive: true},
			{AgentID: "codex", Role: model.RoleDeveloper, Harness: "codex", Model: UnknownModel, IsActive: true},
		},
	}
	for i := 0; i < claims; i++ {
		s.Claims = append(s.Claims, model.Claim{
			ID:             fmt.Sprintf("C-%04d", i),
			State:          model.ClaimStateSupported,
			NormalizedText: "a claim about the system under test",
		})
	}
	for i := 0; i < messages; i++ {
		s.RecentMessages = append(s.RecentMessages, model.AgentMessage{
			ID:      fmt.Sprintf("m-%04d", i),
			From:    model.AuthorProvenance{AgentID: "codex", Harness: "codex"},
			Kind:    model.MessageFinding,
			Content: "a finding recorded during the run",
		})
	}
	return s
}

// TestFrameCostWithLargeClaimSet proves claim volume does not make a repaint
// scale with total claims: the section caps what it renders.
func TestFrameCostWithLargeClaimSet(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)
	c := NewComposer(th)

	for _, n := range []int{10, 1000} {
		state := benchState(n, 0)
		start := time.Now()
		for i := 0; i < 50; i++ {
			BuildFrame(state, th, "/tmp/perf", c, nil, 120, 40)
		}
		per := time.Since(start) / 50
		fmt.Printf("PERF claims=%-5d frame=%v\n", n, per)

		if per > 25*time.Millisecond {
			t.Errorf("claims=%d: frame composition took %v, expected well under 25ms", n, per)
		}
	}
}

// TestFrameCostWithLargeTranscript measures repaint cost against transcript size.
func TestFrameCostWithLargeTranscript(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)
	c := NewComposer(th)

	for _, n := range []int{10, 1000, 10000} {
		state := benchState(0, n)
		start := time.Now()
		for i := 0; i < 20; i++ {
			BuildFrame(state, th, "/tmp/perf", c, nil, 120, 40)
		}
		per := time.Since(start) / 20
		fmt.Printf("PERF transcript=%-6d frame=%v\n", n, per)

		if per > 250*time.Millisecond {
			t.Errorf("transcript=%d: frame composition took %v", n, per)
		}
	}
}

// TestFrameClipsToViewport proves a large transcript does not produce a
// proportionally large frame: the viewport bounds what is laid out.
func TestFrameClipsToViewport(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)
	c := NewComposer(th)

	const rows = 40
	frame := BuildFrame(benchState(0, 10000), th, "/tmp/perf", c, nil, 120, rows)
	lines, _ := frame.Lines(120, rows)

	if len(lines) != rows {
		t.Errorf("frame produced %d lines for a %d-row terminal", len(lines), rows)
	}
}

// TestLargeToolOutputDoesNotUnboundFrame proves a single huge command result is
// still clipped to the viewport.
func TestLargeToolOutputDoesNotUnboundFrame(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)
	c := NewComposer(th)

	state := benchState(0, 0)
	state.LastCommand = "/doctor"
	state.LastOutput = strings.Repeat("a line of tool output\n", 5000)

	start := time.Now()
	frame := BuildFrame(state, th, "/tmp/perf", c, nil, 120, 40)
	lines, _ := frame.Lines(120, 40)
	elapsed := time.Since(start)

	fmt.Printf("PERF large-tool-output frame=%v lines=%d\n", elapsed, len(lines))
	if len(lines) != 40 {
		t.Errorf("large output produced %d lines for a 40-row terminal", len(lines))
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("large tool output took %v to compose", elapsed)
	}
}

// TestStatuslineCostIsBounded measures the per-repaint cost of the statusline,
// which is rebuilt on every frame.
func TestStatuslineCostIsBounded(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)
	state := benchState(100, 100)

	start := time.Now()
	for i := 0; i < 1000; i++ {
		RenderStatusline(state, th, "/home/u/projects/marshal", 120)
	}
	per := time.Since(start) / 1000
	fmt.Printf("PERF statusline=%v\n", per)

	if per > 2*time.Millisecond {
		t.Errorf("statusline render took %v per frame", per)
	}
}

// TestGraphemeSegmentationCostIsBounded guards the editing path added for
// grapheme correctness: it runs on every cursor move and must stay cheap.
func TestGraphemeSegmentationCostIsBounded(t *testing.T) {
	line := []rune(strings.Repeat("a👨‍👩‍👧‍👦中é ", 100))

	start := time.Now()
	for i := 0; i < 1000; i++ {
		PrevGraphemeStart(line, len(line))
	}
	per := time.Since(start) / 1000
	fmt.Printf("PERF grapheme-prev len=%d %v\n", len(line), per)

	if per > 5*time.Millisecond {
		t.Errorf("grapheme traversal took %v on a %d-rune line", per, len(line))
	}
}
