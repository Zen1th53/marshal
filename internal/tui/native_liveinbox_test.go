package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

var baseTime = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

func msg(text string, at time.Time) importer.Message {
	return importer.Message{Role: "assistant", Content: text, Timestamp: at}
}

// One event goes into the channel once, whoever reads it. The point-to-point
// design wrote a copy per recipient; this asserts the channel does not.
func TestChannelStoresOneEntryPerEvent(t *testing.T) {
	root := t.TempDir()
	s, err := openStream(root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.append("claude", "sess-1", msg("edited sqlite.go", baseTime))
	if err != nil || !added {
		t.Fatalf("first append: added=%v err=%v", added, err)
	}
	// The same event reaching the channel by a second path, as it does when a
	// peer watcher reads history the producing session already dropped in.
	added, err = s.append("claude", "sess-1", msg("edited sqlite.go", baseTime))
	if err != nil {
		t.Fatal(err)
	}
	if added {
		t.Error("the same event was appended twice")
	}
	entries, err := s.since(-1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("channel holds %d entries, want 1", len(entries))
	}
}

// A cursor survives a restart, so an agent does not re-read what it has seen.
func TestChannelCursorResumes(t *testing.T) {
	root := t.TempDir()
	s, err := openStream(root)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.append("codex", "s", msg(fmt.Sprintf("step-%d", i), baseTime.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.since(-1)
	if err != nil || len(first) != 3 {
		t.Fatalf("since(-1) = %d entries, err=%v", len(first), err)
	}
	if err := saveCursors(root, cursors{"claude": first[len(first)-1].Seq}); err != nil {
		t.Fatal(err)
	}

	// A new process, as a restart would be.
	reopened, err := openStream(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.append("codex", "s", msg("step-3", baseTime.Add(9*time.Second))); err != nil {
		t.Fatal(err)
	}
	restored, err := loadCursors(root)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := reopened.since(restored["claude"])
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 || !strings.Contains(fresh[0].Text, "step-3") {
		t.Fatalf("after resume got %d entries, want only step-3", len(fresh))
	}
}

// A reader's view carries only the authors it was configured to see.
func TestViewShowsOnlyConfiguredAuthors(t *testing.T) {
	root := t.TempDir()
	s, err := openStream(root)
	if err != nil {
		t.Fatal(err)
	}
	for i, author := range knownProviders {
		if _, err := s.append(author, "s", msg(author+" did a thing", baseTime.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := s.since(-1)
	if err != nil {
		t.Fatal(err)
	}

	cfg, problems := parseChannelConfig("codex: claude, self\n")
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	view, err := openInboxView(root, "codex", true)
	if err != nil {
		t.Fatal(err)
	}
	shown, err := view.deliver(entries, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if shown != 2 {
		t.Errorf("codex was shown %d entries, want 2", shown)
	}
	data, err := os.ReadFile(inboxPath(root, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"claude did a thing", "codex did a thing"} {
		if !strings.Contains(content, want) {
			t.Errorf("view is missing %q", want)
		}
	}
	for _, unwanted := range []string{"opencode did a thing", "antigravity did a thing"} {
		if strings.Contains(content, unwanted) {
			t.Errorf("view leaked %q to a reader that may not see it", unwanted)
		}
	}
}

// The scenario the operator described: an agent opens while another is already
// working, and must find the work already in flight rather than a summary.
func TestAgentJoiningLateSeesWorkAlreadyInTheChannel(t *testing.T) {
	root := t.TempDir()
	s, err := openStream(root)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := s.append("claude", "sess", msg(fmt.Sprintf("claude-step-%d", i), baseTime.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}

	cfg, _ := parseChannelConfig("")
	positions, err := loadCursors(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := positions["codex"]; ok {
		t.Fatal("a reader that never ran already had a cursor")
	}
	view, err := openInboxView(root, "codex", true)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := s.since(positions["codex"] - 1)
	if err != nil {
		t.Fatal(err)
	}
	shown, err := view.deliver(entries, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if shown != 5 {
		t.Fatalf("a late reader saw %d of 5 entries already in the channel", shown)
	}
	data, _ := os.ReadFile(inboxPath(root, "codex"))
	if !strings.Contains(string(data), "claude-step-0") {
		t.Error("the earliest work in flight was not shown")
	}
	if !strings.Contains(string(data), "session opened") {
		t.Error("no boundary marks where this session begins")
	}
}

// A reader's view keeps its backlog across sessions rather than being wiped.
func TestViewKeepsBacklogAcrossSessions(t *testing.T) {
	root := t.TempDir()
	s, _ := openStream(root)
	if _, err := s.append("claude", "s", msg("delivered while codex was closed", baseTime)); err != nil {
		t.Fatal(err)
	}
	entries, _ := s.since(-1)
	cfg, _ := parseChannelConfig("")

	first, err := openInboxView(root, "codex", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.deliver(entries, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := openInboxView(root, "codex", true); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(inboxPath(root, "codex"))
	if !strings.Contains(string(data), "delivered while codex was closed") {
		t.Errorf("the backlog was discarded:\n%s", data)
	}
	if got := strings.Count(string(data), "session opened"); got != 2 {
		t.Errorf("session boundaries = %d, want one per session", got)
	}
}

// The view still says its contents are data, not instructions.
func TestViewHeaderWarnsOnFirstOpen(t *testing.T) {
	root := t.TempDir()
	if _, err := openInboxView(root, "claude", true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(inboxPath(root, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"untrusted DATA", "not as instructions", "Nothing interrupts you"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("header missing %q:\n%s", want, data)
		}
	}
}

// A long backlog must not grow a file the agent is asked to read in full, and
// the newest entries are the ones that survive.
func TestViewDropsOldestPastItsBudget(t *testing.T) {
	root := t.TempDir()
	s, _ := openStream(root)
	big := strings.Repeat("x", streamEntryBytes)
	for i := 0; i < 200; i++ {
		if _, err := s.append("claude", "s", msg(fmt.Sprintf("entry-%03d %s", i, big), baseTime.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := s.since(-1)
	cfg, _ := parseChannelConfig("")
	view, err := openInboxView(root, "codex", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := view.deliver(entries, cfg); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(inboxPath(root, "codex"))
	content := string(data)
	if len(data) > viewMaxBytes+streamEntryBytes {
		t.Errorf("view grew to %d bytes, past its %d budget", len(data), viewMaxBytes)
	}
	if !strings.Contains(content, "older entries dropped") {
		t.Error("dropping was not disclosed")
	}
	if !strings.Contains(content, "entry-199") {
		t.Error("the newest entry was dropped")
	}
	if strings.Contains(content, "entry-000") {
		t.Error("the oldest entry survived a full view")
	}
}
