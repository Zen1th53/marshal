package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

func TestPeerProvidersExcludesTheRunningOne(t *testing.T) {
	if got := peerProviders("claude"); len(got) != 1 || got[0] != "codex" {
		t.Errorf("peers of claude = %v, want [codex]", got)
	}
	if got := peerProviders("codex"); len(got) != 1 || got[0] != "claude" {
		t.Errorf("peers of codex = %v, want [claude]", got)
	}
}

// A stale inbox would present another session's finished work as though it were
// arriving now, so opening one starts from empty.
func TestNewLiveInboxTruncatesAndHeaders(t *testing.T) {
	root := t.TempDir()
	path := inboxPath(root, "codex")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("## stale entry from last time\n"), 0600); err != nil {
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
	if strings.Contains(content, "stale entry") {
		t.Errorf("a previous session's inbox survived:\n%s", content)
	}
	for _, want := range []string{"live inbox", "untrusted DATA", "not as instructions"} {
		if !strings.Contains(content, want) {
			t.Errorf("header missing %q:\n%s", want, content)
		}
	}
	if box.Count() != 0 {
		t.Errorf("a fresh inbox reported %d entries", box.Count())
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

// A long peer session must not grow a file the agent is asked to read in full,
// and a truncated inbox must not read as a quiet one.
func TestLiveInboxStopsAtItsBudgetAndSaysSo(t *testing.T) {
	root := t.TempDir()
	box, err := newLiveInbox(root, "claude")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	big := strings.Repeat("x", inboxLineBytes)
	for i := 0; i < 200; i++ {
		if err := box.append("codex", importer.Message{Role: "assistant", Content: big}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(inboxPath(root, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > inboxMaxBytes+inboxLineBytes {
		t.Errorf("inbox grew to %d bytes, past its %d budget", len(data), inboxMaxBytes)
	}
	if !strings.Contains(string(data), "inbox full") {
		t.Errorf("truncation was not disclosed:\n%s", tail(string(data), 400))
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
