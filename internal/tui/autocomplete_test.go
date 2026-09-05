package tui

import (
	"testing"
)

func TestAutocompleteSlashCommands(t *testing.T) {
	ctx := CompletionContext{
		Commands: []string{"/status", "/rollback", "/route", "/claims", "/goal"},
		Agents:   []string{"codex", "claude", "opencode"},
		Claims:   []string{"C-10", "C-21"},
		Evidence: []string{"E-01", "E-32"},
		Tasks:    []string{"T-05", "T-18"},
		Subcommands: map[string][]string{
			"/goal": {"edit", "diff", "constraints", "pause"},
		},
	}
	comp := NewCompleter(ctx)

	// Test single match append space: /stat -> /status
	newText, newCursor, ok := comp.Complete("/stat", 5, false)
	if !ok || newText != "/status " || newCursor != 8 {
		t.Fatalf("expected '/status ', got ok=%v, text=%q, cursor=%d", ok, newText, newCursor)
	}

	// Test multiple matches: /ro -> matches /rollback, /route
	comp.Reset()
	newText, newCursor, ok = comp.Complete("/ro", 3, false)
	if !ok || newText != "/rollback" {
		t.Fatalf("expected first match '/rollback', got %q", newText)
	}

	// Cycle forward (Tab again)
	newText, newCursor, ok = comp.Complete(newText, newCursor, false)
	if !ok || newText != "/route" {
		t.Fatalf("expected second match '/route', got %q", newText)
	}

	// Cycle forward wraps to /rollback
	newText, newCursor, ok = comp.Complete(newText, newCursor, false)
	if !ok || newText != "/rollback" {
		t.Fatalf("expected wrapped match '/rollback', got %q", newText)
	}

	// Cycle backward (Shift+Tab)
	newText, newCursor, ok = comp.Complete(newText, newCursor, true)
	if !ok || newText != "/route" {
		t.Fatalf("expected reverse match '/route', got %q", newText)
	}

	// Fuzzy completion: /rb -> /rollback
	comp.Reset()
	newText, _, ok = comp.Complete("/rb", 3, false)
	if !ok || newText != "/rollback " {
		t.Fatalf("expected fuzzy '/rollback ', got ok=%v, text=%q", ok, newText)
	}
}

func TestAutocompleteAgentsAndObjects(t *testing.T) {
	ctx := CompletionContext{
		Commands: []string{"/status"},
		Agents:   []string{"codex", "claude", "opencode"},
		Claims:   []string{"C-10", "C-21"},
		Evidence: []string{"E-01", "E-32"},
		Tasks:    []string{"T-05", "T-18"},
	}
	comp := NewCompleter(ctx)

	// Agent mention @co -> @codex
	newText, _, ok := comp.Complete("@co", 3, false)
	if !ok || newText != "@codex " {
		t.Fatalf("expected '@codex ', got ok=%v, text=%q", ok, newText)
	}

	// Agent mention @te -> @team
	comp.Reset()
	newText, _, ok = comp.Complete("@te", 3, false)
	if !ok || newText != "@team " {
		t.Fatalf("expected '@team ', got ok=%v, text=%q", ok, newText)
	}

	// Claim ID completion: /inspect C-1 -> /inspect C-10
	comp.Reset()
	input := "/inspect C-1"
	newText, _, ok = comp.Complete(input, len(input), false)
	if !ok || newText != "/inspect C-10 " {
		t.Fatalf("expected '/inspect C-10 ', got ok=%v, text=%q", ok, newText)
	}

	// Object hash completion: #C-2 -> #C-21
	comp.Reset()
	input = "Check #C-2"
	newText, _, ok = comp.Complete(input, len(input), false)
	if !ok || newText != "Check #C-21 " {
		t.Fatalf("expected 'Check #C-21 ', got ok=%v, text=%q", ok, newText)
	}
}

func TestAutocompleteSubcommands(t *testing.T) {
	ctx := CompletionContext{
		Commands: []string{"/goal"},
		Subcommands: map[string][]string{
			"/goal": {"edit", "diff", "constraints", "pause"},
		},
	}
	comp := NewCompleter(ctx)

	input := "/goal ed"
	newText, _, ok := comp.Complete(input, len(input), false)
	if !ok || newText != "/goal edit " {
		t.Fatalf("expected '/goal edit ', got ok=%v, text=%q", ok, newText)
	}
}
