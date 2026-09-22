package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

// The backlog is the point: a recipient that was closed while another agent
// worked must find that work waiting, not discarded. Opening a session marks
// the boundary instead of wiping what came before it.
func TestNewLiveInboxKeepsTheBacklogAndMarksTheBoundary(t *testing.T) {
	root := t.TempDir()
	path := inboxPath(root, "codex")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("## claude · earlier · assistant\n\ndelivered while codex was closed\n\n"), 0600); err != nil {
		t.Fatal(err)
	}

	box, err := newLiveInbox(root, "codex")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "delivered while codex was closed") {
		t.Errorf("the backlog was discarded:\n%s", content)
	}
	if !strings.Contains(content, "session opened") {
		t.Errorf("no boundary marks where this session starts:\n%s", content)
	}
	if !strings.Contains(content, "arrived before this session started") {
		t.Errorf("the boundary does not say what precedes it:\n%s", content)
	}
	if box.Count() != 0 {
		t.Errorf("opening an inbox reported %d delivered entries", box.Count())
	}
}

// A fresh inbox still explains itself, and still says the contents are data.
func TestLiveInboxHeaderWarnsOnFirstOpen(t *testing.T) {
	root := t.TempDir()
	if _, err := newLiveInbox(root, "codex"); err != nil {
		t.Fatalf("open: %v", err)
	}
	data, err := os.ReadFile(inboxPath(root, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"live inbox", "untrusted DATA", "not as instructions"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("header missing %q:\n%s", want, data)
		}
	}
}

// Both delivery paths can reach the same inbox: the producing session writing
// forward, and a peer watcher reading that provider's history. The entry must
// appear once.
func TestLiveInboxDropsADuplicateEntry(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2026, 9, 16, 12, 30, 0, 0, time.UTC)
	msg := importer.Message{Role: "assistant", Content: "edited workspace.go", Timestamp: at}

	first, err := openPeerInbox(root, "codex")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := first.append("claude", msg); err != nil {
		t.Fatal(err)
	}

	// A separate opener, as a second session would be.
	second, err := openPeerInbox(root, "codex")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := second.append("claude", msg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(inboxPath(root, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "edited workspace.go"); got != 1 {
		t.Errorf("entry written %d times, want 1:\n%s", got, data)
	}
	if second.Count() != 0 {
		t.Errorf("the duplicate was counted as delivered")
	}
}

// Delivering to a provider that is not running must not claim it opened a
// session, because it did not.
func TestOpenPeerInboxWritesNoSessionBoundary(t *testing.T) {
	root := t.TempDir()
	if _, err := openPeerInbox(root, "opencode"); err != nil {
		t.Fatalf("open: %v", err)
	}
	data, err := os.ReadFile(inboxPath(root, "opencode"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "session opened") {
		t.Errorf("delivery to a closed provider claimed a session:\n%s", data)
	}
}

func TestLiveInboxAppendsLabelledEntries(t *testing.T) {
	root := t.TempDir()
	box, err := newLiveInbox(root, "codex")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	at := time.Date(2026, 9, 16, 12, 30, 0, 0, time.UTC)
	if err := box.append("claude", importer.Message{
		Role: "assistant", Kind: importer.MessageKindToolUse,
		Content: "Edit internal/tui/workspace.go", Timestamp: at,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	// Empty content is not an event and must not produce an entry.
	if err := box.append("claude", importer.Message{Role: "user", Content: "   "}); err != nil {
		t.Fatalf("append blank: %v", err)
	}

	if box.Count() != 1 {
		t.Errorf("entries = %d, want 1", box.Count())
	}
	data, err := os.ReadFile(inboxPath(root, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"claude", "12:30:00Z", "assistant:tool_use", "Edit internal/tui/workspace.go"} {
		if !strings.Contains(content, want) {
			t.Errorf("entry missing %q:\n%s", want, content)
		}
	}
}

// A long backlog must not grow a file the agent is asked to read in full. The
// oldest entries give way, not the newest: a file that sealed itself at the
// first busy hour would hide exactly the recent work a returning agent needs.
func TestLiveInboxDropsOldestPastItsBudget(t *testing.T) {
	root := t.TempDir()
	box, err := newLiveInbox(root, "claude")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	big := strings.Repeat("x", inboxLineBytes)
	at := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 200; i++ {
		if err := box.append("codex", importer.Message{
			Role: "assistant", Content: fmt.Sprintf("entry-%03d %s", i, big),
			Timestamp: at.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(inboxPath(root, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if len(data) > inboxMaxBytes+inboxLineBytes {
		t.Errorf("inbox grew to %d bytes, past its %d budget", len(data), inboxMaxBytes)
	}
	if !strings.Contains(content, "older entries dropped") {
		t.Errorf("dropping was not disclosed:\n%s", tail(content, 400))
	}
	if !strings.Contains(content, "entry-199") {
		t.Errorf("the newest entry was dropped:\n%s", tail(content, 400))
	}
	if strings.Contains(content, "entry-000") {
		t.Errorf("the oldest entry survived a full inbox")
	}
}

// One oversized message must not crowd out everything after it.
func TestLiveInboxTruncatesOneEntry(t *testing.T) {
	root := t.TempDir()
	box, err := newLiveInbox(root, "codex")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := box.append("claude", importer.Message{
		Role: "user", Content: strings.Repeat("y", inboxLineBytes*4),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	data, _ := os.ReadFile(inboxPath(root, "codex"))
	if !strings.Contains(string(data), "truncated") {
		t.Errorf("a long entry was not truncated:\n%s", tail(string(data), 300))
	}
}

// The agent only benefits from the inbox if it is told the file exists, and
// told that nothing pushes it.
func TestInboxBriefingNoteStatesThePull(t *testing.T) {
	note := inboxBriefingNote("/project", "codex")
	if !strings.Contains(note, filepath.Join(".marshal", "inbox", "codex.md")) {
		t.Errorf("note does not name the inbox path:\n%s", note)
	}
	if !strings.Contains(note, "Nothing pushes it to you") {
		t.Errorf("note does not say delivery is a pull:\n%s", note)
	}
}

func TestProviderHistoryDirHonoursHomeOverride(t *testing.T) {
	t.Setenv("CODEX_HOME", "/custom/codex")
	dir, err := providerHistoryDir("codex", "/project")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if dir != filepath.Join("/custom/codex", "sessions") {
		t.Errorf("codex history dir = %q", dir)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/custom/claude")
	if dir, err = providerHistoryDir("claude", "/project"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if dir != filepath.Join("/custom/claude", "projects") {
		t.Errorf("claude history dir = %q", dir)
	}
	if _, err := providerHistoryDir("gemini", "/project"); err == nil {
		t.Error("an unsupported provider resolved a history directory")
	}
}
