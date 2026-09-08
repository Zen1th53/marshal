package tui

import (
	"strings"
	"testing"
)

func TestComposerBasicTypingAndCursor(t *testing.T) {
	c := NewComposer(NewTheme(ThemeDefault, true, true))

	// Type "hello world"
	for _, r := range "hello world" {
		c.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}
	if c.Text() != "hello world" {
		t.Fatalf("expected 'hello world', got %q", c.Text())
	}
	if c.CursorPos() != 11 {
		t.Fatalf("expected cursor 11, got %d", c.CursorPos())
	}

	// Move cursor left 5 times
	for i := 0; i < 5; i++ {
		c.HandleKey(KeyEvent{Type: KeyLeft})
	}
	if c.CursorPos() != 6 {
		t.Fatalf("expected cursor 6, got %d", c.CursorPos())
	}

	// Insert "brave " in the middle
	for _, r := range "brave " {
		c.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}
	if c.Text() != "hello brave world" {
		t.Fatalf("expected 'hello brave world', got %q", c.Text())
	}

	// Test Home / Ctrl+A
	c.HandleKey(KeyEvent{Type: KeyHome})
	if c.CursorPos() != 0 {
		t.Fatalf("expected cursor 0 at Home, got %d", c.CursorPos())
	}

	// Test End / Ctrl+E
	c.HandleKey(KeyEvent{Type: KeyEnd})
	if c.CursorPos() != len(c.Text()) {
		t.Fatalf("expected cursor at end, got %d", c.CursorPos())
	}

	// Test Ctrl+W (delete previous word)
	c.HandleKey(KeyEvent{Type: KeyCtrlW})
	if c.Text() != "hello brave " {
		t.Fatalf("expected 'hello brave ', got %q", c.Text())
	}
	c.HandleKey(KeyEvent{Type: KeyCtrlW})
	if c.Text() != "hello " {
		t.Fatalf("expected 'hello ', got %q", c.Text())
	}

	// Test Backspace
	c.HandleKey(KeyEvent{Type: KeyBackspace})
	if c.Text() != "hello" {
		t.Fatalf("expected 'hello', got %q", c.Text())
	}

	// Test Enter submission
	submitted, ok := c.HandleKey(KeyEvent{Type: KeyEnter})
	if !ok || submitted != "hello" {
		t.Fatalf("expected submitted 'hello', got ok=%v, text=%q", ok, submitted)
	}
	if c.Text() != "" {
		t.Fatalf("expected cleared buffer after Enter, got %q", c.Text())
	}
}

func TestComposerHistory(t *testing.T) {
	c := NewComposer(NewTheme(ThemeDefault, true, true))
	c.AddHistory("/status")
	c.AddHistory("/claims")
	c.AddHistory("/route")

	// Up arrow cycles backwards
	c.HandleKey(KeyEvent{Type: KeyUp})
	if c.Text() != "/route" {
		t.Fatalf("expected '/route', got %q", c.Text())
	}

	c.HandleKey(KeyEvent{Type: KeyUp})
	if c.Text() != "/claims" {
		t.Fatalf("expected '/claims', got %q", c.Text())
	}

	// Down arrow cycles forwards
	c.HandleKey(KeyEvent{Type: KeyDown})
	if c.Text() != "/route" {
		t.Fatalf("expected '/route', got %q", c.Text())
	}

	// Down arrow past end returns to empty
	c.HandleKey(KeyEvent{Type: KeyDown})
	if c.Text() != "" {
		t.Fatalf("expected empty buffer at bottom of history, got %q", c.Text())
	}
}

func TestSubmittedSecretsAreRedactedBeforeHistory(t *testing.T) {
	c := NewComposer(NewTheme(ThemeDefault, true, false))
	secret := "tiny7"
	input := "/provider config codex token=" + secret
	for _, r := range input {
		c.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}
	submitted, ok := c.HandleKey(KeyEvent{Type: KeyEnter})
	if !ok || !strings.Contains(submitted, secret) {
		t.Fatalf("handler must receive original command for safe rejection: %q", submitted)
	}
	if len(c.history) != 1 || strings.Contains(c.history[0], secret) || !strings.Contains(c.history[0], "[REDACTED]") {
		t.Fatalf("secret entered composer history: %#v", c.history)
	}
}

func TestComposerPaste(t *testing.T) {
	c := NewComposer(NewTheme(ThemeDefault, true, true))
	c.HandleKey(KeyEvent{Type: KeyPaste, Paste: "@codex fix auth bug"})
	if c.Text() != "@codex fix auth bug" {
		t.Fatalf("expected paste result, got %q", c.Text())
	}
}

func TestComposerSearchCtrlR(t *testing.T) {
	c := NewComposer(NewTheme(ThemeDefault, true, true))
	c.AddHistory("/checkpoint create before-refactor")
	c.AddHistory("/rollback CP-01")
	c.AddHistory("/status")

	// Enter search mode
	c.HandleKey(KeyEvent{Type: KeyCtrlR})
	if !c.IsSearchMode() {
		t.Fatalf("expected search mode active")
	}

	// Type query "roll"
	for _, r := range "roll" {
		c.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}

	// Match should be /rollback CP-01
	if c.Text() != "/rollback CP-01" {
		t.Fatalf("expected matched '/rollback CP-01', got %q", c.Text())
	}

	// Press Enter to accept search result
	c.HandleKey(KeyEvent{Type: KeyEnter})
	if c.IsSearchMode() {
		t.Fatalf("expected search mode to exit on Enter")
	}
	if c.Text() != "/rollback CP-01" {
		t.Fatalf("expected buffer to hold '/rollback CP-01', got %q", c.Text())
	}
}

// TestComposerDynamicPrompt pins the composer's prompt contract.
//
// Project, mode and runtime state deliberately do NOT appear here: they belong
// to the persistent statusline. Carrying them in the prompt duplicated context
// and, because the prompt then spanned two lines while the redraw cleared only
// one, appended a fresh banner to scrollback on every keystroke.
func TestComposerDynamicPrompt(t *testing.T) {
	c := NewComposer(NewTheme(ThemeDefault, true, true))
	c.SetPrompt(ComposerPromptInfo{
		Project: "codex-core",
		Mode:    "ULTRA",
		State:   "VERIFYING",
	})

	prompt := StripANSI(c.PromptString())
	if !strings.Contains(prompt, PromptMarker) {
		t.Fatalf("prompt missing its input marker: %q", prompt)
	}
	if strings.Contains(prompt, "\n") {
		t.Fatalf("prompt must occupy a single line: %q", prompt)
	}
	for _, statusField := range []string{"codex-core", "ULTRA", "VERIFYING"} {
		if strings.Contains(prompt, statusField) {
			t.Errorf("prompt repeats statusline field %q: %q", statusField, prompt)
		}
	}

	// Addressing a participant is composer state the statusline does not carry,
	// so that one target does belong in the prompt.
	c.SetPrompt(ComposerPromptInfo{Agent: "codex"})
	prompt2 := StripANSI(c.PromptString())
	if !strings.Contains(prompt2, "@codex") {
		t.Fatalf("prompt missing the addressed agent: %q", prompt2)
	}
	if strings.Contains(prompt2, "\n") {
		t.Fatalf("agent prompt must occupy a single line: %q", prompt2)
	}
}
