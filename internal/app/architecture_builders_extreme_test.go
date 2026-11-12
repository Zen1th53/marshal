//go:build !governed

package app

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/usage"
)

// TestBuilderLimitTextMatchesPicker: the design-review builder list says a
// limit the way the launch picker and vitals row do: the percent left,
// rounded down, and "limit unknown" when none can be read.
func TestBuilderLimitTextMatchesPicker(t *testing.T) {
	reset := time.Date(2026, 11, 12, 15, 4, 0, 0, time.Local)
	qs := []usage.Quota{
		{Provider: "codex", Known: true, Windows: []usage.Window{{Label: "wk", LeftPercent: 35.6, ResetsAt: reset}}},
		{Provider: "claude", Reason: usage.ReasonNoFile},
	}
	if got := builderLimitText(qs, "codex"); got != "wk 35% left · resets 15:04" {
		t.Fatalf("codex = %q", got)
	}
	for _, p := range []string{"claude", "opencode"} {
		if got := builderLimitText(qs, p); got != "limit unknown" {
			t.Fatalf("%s = %q, want limit unknown", p, got)
		}
	}
}

func TestArchitectureBuildersOrderAndPlainReasons(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	r := &Runtime{}
	options := r.architectureBuilders()(context.Background())
	wantID := []string{"claude", "codex", "agy", "opencode"}
	wantGlyph := []string{"✳", "⬡", "◆", "●"}
	if len(options) != len(wantID) {
		t.Fatalf("want %d options, got %d", len(wantID), len(options))
	}
	for i := range options {
		if options[i].ID != wantID[i] {
			t.Fatalf("option %d id: want %q got %q", i, wantID[i], options[i].ID)
		}
		if options[i].Glyph != wantGlyph[i] {
			t.Fatalf("option %d glyph: want %q got %q", i, wantGlyph[i], options[i].Glyph)
		}
		if options[i].Available {
			t.Fatalf("provider %q should be unavailable with an empty PATH", options[i].ID)
		}
		want := "not installed"
		if options[i].ID == "agy" && runtime.GOOS != "linux" {
			want = "not available in this build"
		}
		if options[i].Reason != want {
			t.Fatalf("provider %q reason: want %q got %q", options[i].ID, want, options[i].Reason)
		}
		if options[i].Limit != "" {
			t.Fatalf("unavailable provider %q must not carry a limit: %q", options[i].ID, options[i].Limit)
		}
	}
}
