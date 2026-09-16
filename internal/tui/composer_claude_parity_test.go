package tui

import (
	"strings"
	"testing"
)

// The live menu must be able to ask what would match without the buffer moving
// underneath the operator while they are still typing.
func TestSuggestDoesNotMutateBufferOrCycle(t *testing.T) {
	c := NewCompleter(CompletionContext{Commands: []string{"/memory", "/models", "/model"}})
	word, first := c.Suggest("/mo", 3)
	if word != "/mo" {
		t.Fatalf("word = %q, want /mo", word)
	}
	if len(first) == 0 {
		t.Fatal("no candidates for /mo")
	}
	// Repeating the call must answer identically: Suggest advances nothing.
	_, second := c.Suggest("/mo", 3)
	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Errorf("Suggest advanced a cycle: %v then %v", first, second)
	}
	if _, none := c.Suggest("hello", 5); len(none) != 0 {
		t.Errorf("a bare word produced candidates: %v", none)
	}
}

// Typing a trigger opens the menu on its own. That is what makes the surface
// discoverable without knowing a command name already.
func TestMenuOpensWhileTypingAndNarrows(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	for _, r := range "/mem" {
		ws.composer.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
		ws.refreshCompletion()
	}
	if !ws.completionOpen {
		t.Fatal("typing a slash command did not open the menu")
	}
	if ws.composer.Text() != "/mem" {
		t.Fatalf("the menu rewrote the buffer while typing: %q", ws.composer.Text())
	}
	for _, m := range ws.completions {
		if !strings.HasPrefix(m, "/mem") {
			t.Errorf("candidate %q does not match the typed prefix", m)
		}
	}

	// Typing past every candidate closes it rather than leaving a stale list.
	for _, r := range "zzz" {
		ws.composer.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
		ws.refreshCompletion()
	}
	if ws.completionOpen {
		t.Errorf("the menu survived a prefix nothing matches: %v", ws.completions)
	}
}

// Moving the highlight must not touch the draft; only accepting writes to it.
func TestArrowsMoveHighlightAndAcceptWritesOnce(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	ws.composer.SetText("/mo")
	ws.composer.cursor = 3
	ws.refreshCompletion()
	if !ws.completionOpen {
		t.Fatal("typing /mo did not open the menu")
	}
	if len(ws.completions) < 2 {
		t.Fatalf("expected several /mo candidates, got %v", ws.completions)
	}

	before := ws.composer.Text()
	ws.moveCompletion(1)
	if ws.composer.Text() != before {
		t.Fatalf("moving the highlight rewrote the buffer: %q -> %q", before, ws.composer.Text())
	}
	wanted := ws.completions[ws.completionIndex]

	ws.acceptCompletion()
	if ws.completionOpen {
		t.Error("accepting left the menu open")
	}
	if got := ws.composer.Text(); got != wanted+" " {
		t.Errorf("accepted text = %q, want %q", got, wanted+" ")
	}
	if ws.composer.CursorPos() != len([]rune(wanted))+1 {
		t.Errorf("cursor = %d, want end of the accepted word", ws.composer.CursorPos())
	}
}

// The highlight survives narrowing, so typing one more letter does not silently
// select a different command than the one under the cursor.
func TestHighlightSurvivesNarrowing(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	ws.composer.SetText("/m")
	ws.composer.cursor = 2
	ws.refreshCompletion()
	if len(ws.completions) < 2 {
		t.Skipf("need at least two candidates for /m, got %v", ws.completions)
	}
	// Highlight a candidate that will still match after one more character.
	target := ""
	for i, m := range ws.completions {
		if strings.HasPrefix(m, "/me") {
			ws.completionIndex, target = i, m
			break
		}
	}
	if target == "" {
		t.Skip("no /me candidate to track")
	}
	ws.composer.HandleKey(KeyEvent{Type: KeyRune, Rune: 'e'})
	ws.refreshCompletion()
	if !ws.completionOpen {
		t.Fatal("menu closed while candidates remained")
	}
	if got := ws.completions[ws.completionIndex]; got != target {
		t.Errorf("highlight moved from %q to %q across a narrowing keystroke", target, got)
	}
}

// A long paste is shown as one short token and restored verbatim on submit, so
// the composer stays readable without the operator losing a character.
func TestLargePasteCollapsesAndRestores(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	pasted := "line one\nline two\nline three\nline four\nline five"

	ws.composer.HandleKey(KeyEvent{Type: KeyRune, Rune: '/'})
	for _, r := range "say " {
		ws.composer.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}
	ws.composer.HandleKey(KeyEvent{Type: KeyPaste, Paste: pasted})

	shown := ws.composer.Text()
	if strings.Contains(shown, "line three") {
		t.Errorf("a five-line paste was inlined:\n%s", shown)
	}
	if !strings.Contains(shown, "[Pasted text #1 +5 lines]") {
		t.Errorf("placeholder missing from the draft: %q", shown)
	}
	if ws.composer.PendingPastes() != 1 {
		t.Errorf("pending pastes = %d, want 1", ws.composer.PendingPastes())
	}

	submitted, ok := ws.composer.HandleKey(KeyEvent{Type: KeyEnter})
	if !ok {
		t.Fatal("Enter did not submit")
	}
	if !strings.Contains(submitted, pasted) {
		t.Errorf("the paste was not restored on submit:\n%s", submitted)
	}
	if ws.composer.PendingPastes() != 0 {
		t.Error("submitting left pastes behind for the next draft")
	}
}

// A short paste is ordinary text and must not be hidden behind a placeholder.
func TestShortPasteStaysInline(t *testing.T) {
	c := NewComposer(nil)
	c.HandleKey(KeyEvent{Type: KeyPaste, Paste: "one\ntwo"})
	if got := c.Text(); got != "one\ntwo" {
		t.Errorf("short paste = %q, want it inline", got)
	}
	if c.PendingPastes() != 0 {
		t.Error("a short paste was collapsed")
	}
}

// Bracketed paste is data, never keystrokes: a pasted newline must not submit.
func TestPastedNewlineDoesNotSubmit(t *testing.T) {
	c := NewComposer(nil)
	if _, submitted := c.HandleKey(KeyEvent{Type: KeyPaste, Paste: "rm -rf /\nyes\n"}); submitted {
		t.Fatal("a pasted newline submitted the buffer")
	}
}

// A token the operator typed themselves has no paste behind it and must survive
// submission exactly as written.
func TestUnbackedPlaceholderIsLeftAlone(t *testing.T) {
	c := NewComposer(nil)
	for _, r := range "[Pasted text #7 +3 lines]" {
		c.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}
	submitted, ok := c.HandleKey(KeyEvent{Type: KeyEnter})
	if !ok {
		t.Fatal("Enter did not submit")
	}
	if submitted != "[Pasted text #7 +3 lines]" {
		t.Errorf("an unbacked token was rewritten: %q", submitted)
	}
}

// A finished command must stay submittable. A menu still offering the exact
// word already typed would take Enter away from running it.
func TestMenuClosesOnAnExactCompleteCommand(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	for _, r := range "/status" {
		ws.composer.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
		ws.refreshCompletion()
	}
	if ws.completionOpen {
		t.Fatalf("the menu stayed open on a complete command: %v", ws.completions)
	}
	submitted, ok := ws.composer.HandleKey(KeyEvent{Type: KeyEnter})
	if !ok || submitted != "/status" {
		t.Fatalf("Enter did not submit the finished command: %q, ok=%v", submitted, ok)
	}
}
