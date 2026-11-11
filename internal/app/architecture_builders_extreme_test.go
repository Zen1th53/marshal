//go:build !governed

package app

import (
	"context"
	"runtime"
	"testing"
)

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
