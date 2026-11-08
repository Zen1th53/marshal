package nativehistory

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

func writeAntigravitySummariesFixture(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "antigravity_summaries.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	for id, uris := range entries {
		if _, err := db.Exec(`INSERT INTO conversation_summaries (conversation_id, workspace_uris) VALUES (?, ?)`, id, uris); err != nil {
			t.Fatal(err)
		}
	}
}

func setupAntigravityTestHome(t *testing.T) (home, conversations, summaries string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	conversations = filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(conversations, 0700); err != nil {
		t.Fatal(err)
	}
	summaries = filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")
	return home, conversations, summaries
}

func TestAntigravityMatchingWorkspaceImported(t *testing.T) {
	_, conversations, summaries := setupAntigravityTestHome(t)
	root := t.TempDir()

	writeAntigravitySummariesFixture(t, summaries, map[string]string{
		"conv-match": `["file://` + root + `"]`,
	})
	writeAntigravityFixture(t, filepath.Join(conversations, "conv-match.db"), "matching request")

	var got []importer.SessionTranscript
	c, err := New(Antigravity, root,
		WithCaptureTools(true),
		WithAntigravitySummaries(summaries),
		WithConsume(func(tr importer.SessionTranscript) error {
			got = append(got, tr)
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 transcript imported, got %d: %+v", len(got), got)
	}
	if got[0].SessionID != "conv-match" {
		t.Fatalf("expected session ID conv-match, got %q", got[0].SessionID)
	}
	if len(got[0].Messages) == 0 || got[0].Messages[0].Content != "matching request" {
		t.Fatalf("unexpected transcript messages: %+v", got[0].Messages)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolvedRoot = root
	}
	if got[0].CWD != filepath.Clean(resolvedRoot) {
		t.Fatalf("expected CWD %q, got %q", resolvedRoot, got[0].CWD)
	}
}

func TestAntigravityOtherWorkspaceSkipped(t *testing.T) {
	_, conversations, summaries := setupAntigravityTestHome(t)
	root := t.TempDir()
	otherRoot := t.TempDir()

	writeAntigravitySummariesFixture(t, summaries, map[string]string{
		"conv-other": `["file://` + otherRoot + `"]`,
	})
	writeAntigravityFixture(t, filepath.Join(conversations, "conv-other.db"), "other project request")

	var got []importer.SessionTranscript
	c, err := New(Antigravity, root,
		WithCaptureTools(true),
		WithAntigravitySummaries(summaries),
		WithConsume(func(tr importer.SessionTranscript) error {
			got = append(got, tr)
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 transcripts imported for other workspace, got %d: %+v", len(got), got)
	}
	if c.SeenIndex()["conv-other"] == "" {
		t.Fatalf("expected conv-other to be recorded in seen index, got empty")
	}
}

func TestAntigravityMissingWorkspaceSkipped(t *testing.T) {
	_, conversations, summaries := setupAntigravityTestHome(t)
	root := t.TempDir()

	// summaries database exists but has no entry for conv-missing.
	writeAntigravitySummariesFixture(t, summaries, map[string]string{})
	writeAntigravityFixture(t, filepath.Join(conversations, "conv-missing.db"), "unrecorded request")

	var got []importer.SessionTranscript
	c, err := New(Antigravity, root,
		WithCaptureTools(true),
		WithAntigravitySummaries(summaries),
		WithConsume(func(tr importer.SessionTranscript) error {
			got = append(got, tr)
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected conversation with missing workspace to be skipped, got %d: %+v", len(got), got)
	}
}

func TestAntigravityConcurrentConversationInAnotherProjectSkipped(t *testing.T) {
	_, conversations, summaries := setupAntigravityTestHome(t)
	rootA := t.TempDir()
	rootB := t.TempDir()

	writeAntigravitySummariesFixture(t, summaries, map[string]string{
		"conv-a": `["file://` + rootA + `"]`,
		"conv-b": `["file://` + rootB + `"]`,
	})

	writeAntigravityFixture(t, filepath.Join(conversations, "conv-a.db"), "project A work")
	writeAntigravityFixture(t, filepath.Join(conversations, "conv-b.db"), "project B work")
	writeAntigravityFixture(t, filepath.Join(conversations, "conv-absent.db"), "absent workspace work")

	var got []importer.SessionTranscript
	c, err := New(Antigravity, rootA,
		WithCaptureTools(true),
		WithAntigravitySummaries(summaries),
		WithConsume(func(tr importer.SessionTranscript) error {
			got = append(got, tr)
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 {
		t.Fatalf("expected exactly 1 transcript (conv-a), got %d: %+v", len(got), got)
	}
	if got[0].SessionID != "conv-a" || got[0].Messages[0].Content != "project A work" {
		t.Fatalf("expected conv-a transcript, got %+v", got[0])
	}
}

func TestAntigravityChatFileImportsLaunchedSessionEvenIfSummariesMissing(t *testing.T) {
	_, conversations, summaries := setupAntigravityTestHome(t)
	root := t.TempDir()

	// summaries database has no entry for conv-launched.
	writeAntigravitySummariesFixture(t, summaries, map[string]string{})
	targetPath := filepath.Join(conversations, "conv-launched.db")
	writeAntigravityFixture(t, targetPath, "launched session request")

	var got []importer.SessionTranscript
	c, err := New(Antigravity, root,
		WithCaptureTools(true),
		WithAntigravitySummaries(summaries),
		WithChatFile(func(path string) bool {
			return path == targetPath
		}),
		WithConsume(func(tr importer.SessionTranscript) error {
			got = append(got, tr)
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SessionID != "conv-launched" {
		t.Fatalf("expected launched conversation to be captured via chatFile, got %+v", got)
	}
}

func TestAntigravityChatFileRespectsOtherWorkspace(t *testing.T) {
	_, conversations, summaries := setupAntigravityTestHome(t)
	rootA := t.TempDir()
	rootB := t.TempDir()

	writeAntigravitySummariesFixture(t, summaries, map[string]string{
		"conv-other": `["file://` + rootB + `"]`,
	})
	targetPath := filepath.Join(conversations, "conv-other.db")
	writeAntigravityFixture(t, targetPath, "other session request")

	var got []importer.SessionTranscript
	c, err := New(Antigravity, rootA,
		WithCaptureTools(true),
		WithAntigravitySummaries(summaries),
		WithChatFile(func(path string) bool {
			return path == targetPath
		}),
		WithConsume(func(tr importer.SessionTranscript) error {
			got = append(got, tr)
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected conv-other to be rejected even with chatFile because workspace belongs to rootB, got: %+v", got)
	}
	if c.SeenIndex()["conv-other"] == "" {
		t.Fatalf("expected conv-other to be recorded as seen")
	}
}

func TestAntigravitySyncCancellationStopsScanAndProposal(t *testing.T) {
	_, conversations, summaries := setupAntigravityTestHome(t)
	root := t.TempDir()

	writeAntigravitySummariesFixture(t, summaries, map[string]string{
		"conv-1": `["file://` + root + `"]`,
		"conv-2": `["file://` + root + `"]`,
	})
	writeAntigravityFixture(t, filepath.Join(conversations, "conv-1.db"), "request one")
	writeAntigravityFixture(t, filepath.Join(conversations, "conv-2.db"), "request two")

	entered := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	consumed := 0
	c, err := New(Antigravity, root,
		WithCaptureTools(true),
		WithAntigravitySummaries(summaries),
		WithConsume(func(importer.SessionTranscript) error {
			mu.Lock()
			consumed++
			mu.Unlock()
			select {
			case <-entered:
			default:
				close(entered)
			}
			<-release
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Sync(ctx) }()

	<-entered      // the scan is inside a proposal
	cancel()       // cancel while it is blocked
	close(release) // let the proposal return so the loop reaches the next check

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Sync err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Sync did not return after cancellation")
	}

	// The scan stopped with Sync; nothing keeps proposing afterwards.
	mu.Lock()
	at := consumed
	mu.Unlock()
	if at != 1 {
		t.Fatalf("proposed %d times, want exactly 1", at)
	}
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	after := consumed
	mu.Unlock()
	if after != at {
		t.Fatalf("proposed after cancellation: %d -> %d", at, after)
	}
}

func TestAntigravitySyncAlreadyCanceledConsumesNothing(t *testing.T) {
	_, conversations, summaries := setupAntigravityTestHome(t)
	root := t.TempDir()

	writeAntigravitySummariesFixture(t, summaries, map[string]string{
		"conv-1": `["file://` + root + `"]`,
	})
	writeAntigravityFixture(t, filepath.Join(conversations, "conv-1.db"), "request one")

	consumed := 0
	c, err := New(Antigravity, root,
		WithCaptureTools(true),
		WithAntigravitySummaries(summaries),
		WithConsume(func(importer.SessionTranscript) error {
			consumed++
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Sync(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sync err = %v, want context.Canceled", err)
	}
	if consumed != 0 {
		t.Fatalf("consumed %d records with an already-cancelled context", consumed)
	}
}
