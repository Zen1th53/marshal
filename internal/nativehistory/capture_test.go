package nativehistory

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

func fixture(t *testing.T, name, root string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.ReplaceAll(string(data), "{{ROOT}}", root))
}

func TestJSONLBaselineCapturesOnlyPostBaselineChanges(t *testing.T) {
	for _, provider := range []Provider{Codex, Claude} {
		t.Run(string(provider), func(t *testing.T) {
			root, home := t.TempDir(), t.TempDir()
			if provider == Codex {
				t.Setenv("CODEX_HOME", home)
			} else {
				t.Setenv("CLAUDE_CONFIG_DIR", home)
			}
			dir, err := HistoryDir(provider, root)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			before := filepath.Join(dir, "before.jsonl")
			if err := os.WriteFile(before, fixture(t, string(provider)+".jsonl", root), 0600); err != nil {
				t.Fatal(err)
			}
			var got []importer.SessionTranscript
			c, err := New(provider, root, WithConsume(func(tr importer.SessionTranscript) error { got = append(got, tr); return nil }))
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Baseline(context.Background()); err != nil {
				t.Fatal(err)
			}
			newFile := filepath.Join(dir, "after.jsonl")
			if err := os.WriteFile(newFile, fixture(t, string(provider)+".jsonl", root), 0600); err != nil {
				t.Fatal(err)
			}
			if err := c.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].SessionID != string(provider)+"-fixture" || got[0].Provider != string(provider) {
				t.Fatalf("captured transcripts = %+v", got)
			}
			extra := `{"type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"fixture follow-up"}]}}` + "\n"
			if provider == Claude {
				extra = `{"type":"assistant","sessionId":"claude-fixture","cwd":"` + root + `","timestamp":"2026-09-14T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"fixture follow-up"}]}}` + "\n"
			}
			if err := os.WriteFile(before, append(fixture(t, string(provider)+".jsonl", root), extra...), 0600); err != nil {
				t.Fatal(err)
			}
			if err := c.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 {
				t.Fatalf("updated existing transcript was not captured: %d", len(got))
			}
			last := got[len(got)-1].Messages
			if len(last) == 0 || !strings.Contains(last[len(last)-1].Content, "fixture follow-up") {
				t.Fatalf("appended record not captured: %+v", last)
			}
		})
	}
}

// A live chat appends to one file many times a minute. Once read, a Claude
// history that only grew yields just its new records; one that was rewritten
// (it shrank, or its first or last consumed bytes changed) is read again.
func TestClaudeHistoryContinuesAfterConsumedRecords(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir, err := HistoryDir(Claude, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	var got []importer.SessionTranscript
	c, err := New(Claude, root, WithConsume(func(tr importer.SessionTranscript) error { got = append(got, tr); return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Baseline(context.Background()); err != nil {
		t.Fatal(err)
	}
	record := func(n int) string {
		return fmt.Sprintf(`{"type":"assistant","sessionId":"claude-fixture","cwd":"%s","timestamp":"2026-09-14T00:00:%02dZ","message":{"role":"assistant","content":[{"type":"text","text":"answer %d"}]}}`+"\n", root, n, n)
	}
	path := filepath.Join(dir, "chat.jsonl")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if err := c.Sync(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	count := func(tr importer.SessionTranscript) int { return len(tr.Messages) }
	write(record(1) + record(2))
	// A torn final record is not consumed until it is complete.
	torn := record(3)
	write(record(1) + record(2) + torn[:20])
	write(record(1) + record(2) + torn)
	if len(got) != 2 || count(got[0]) != 2 || count(got[1]) != 1 || !strings.Contains(got[1].Messages[0].Content, "answer 3") {
		t.Fatalf("growth re-read or lost records: %+v", got)
	}
	// A history rewritten shorter is read from the start again.
	write(record(4) + record(5))
	if len(got) != 3 || count(got[2]) != 2 || !strings.Contains(got[2].Messages[0].Content, "answer 4") {
		t.Fatalf("rewritten history not read again: %+v", got)
	}
}

func TestWithOpenCodeRunNilSyncsJSONL(t *testing.T) {
	for _, provider := range []Provider{Codex, Claude} {
		t.Run(string(provider), func(t *testing.T) {
			root, home := t.TempDir(), t.TempDir()
			if provider == Codex {
				t.Setenv("CODEX_HOME", home)
			} else {
				t.Setenv("CLAUDE_CONFIG_DIR", home)
			}
			dir, err := HistoryDir(provider, root)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "own.jsonl"), fixture(t, string(provider)+".jsonl", root), 0600); err != nil {
				t.Fatal(err)
			}
			var got []importer.SessionTranscript
			c, err := New(provider, root,
				WithOpenCodeRun(nil),
				WithConsume(func(tr importer.SessionTranscript) error { got = append(got, tr); return nil }),
			)
			if err != nil {
				t.Fatal(err)
			}
			if c.openCodeRun != nil {
				t.Fatalf("WithOpenCodeRun(nil) left a non-nil runner for %s", provider)
			}
			if err := c.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].SessionID != string(provider)+"-fixture" || got[0].Provider != string(provider) {
				t.Fatalf("JSONL not synced for %s: %+v", provider, got)
			}
		})
	}
}

func TestOpenCodeBaselineDoesNotReplayExistingSessions(t *testing.T) {
	root := t.TempDir()
	list := []byte(`[{"id":"old","updated":1,"directory":"` + root + `"}]`)
	export := fixture(t, "opencode.json", root)
	c := newNativeHistoryWatch("", root)
	c.openCodeRun = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "session" {
			return list, nil
		}
		return export, nil
	}
	var got []importer.SessionTranscript
	c.consume = func(tr importer.SessionTranscript) error { got = append(got, tr); return nil }
	if err := c.Baseline(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("baseline session replayed: %+v", got)
	}
	list = []byte(`[{"id":"opencode-fixture","updated":2,"directory":"` + root + `"}]`)
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SessionID != "opencode-fixture" || got[0].Messages[0].Content != "fixture request" {
		t.Fatalf("changed OpenCode session = %+v", got)
	}
}

func TestAntigravityBaselineLeavesUnchangedConversationAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	conversations := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(conversations, 0700); err != nil {
		t.Fatal(err)
	}
	summaries := filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")
	writeAntigravitySummariesFixture(t, summaries, map[string]string{
		"old": `["file://` + root + `"]`,
		"new": `["file://` + root + `"]`,
	})
	writeAntigravityFixture(t, filepath.Join(conversations, "old.db"), "old request")
	var got []importer.SessionTranscript
	c, err := New(Antigravity, root, WithCaptureTools(true), WithConsume(func(tr importer.SessionTranscript) error { got = append(got, tr); return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Baseline(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("unchanged Antigravity session consumed: %+v", got)
	}
	writeAntigravityFixture(t, filepath.Join(conversations, "new.db"), "one-shot request")
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SessionID != "new" || got[0].Messages[0].Content != "one-shot request" {
		t.Fatalf("changed Antigravity session = %+v", got)
	}
}

func TestSyncCancellationStopsScanAndProposal(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	dir, err := HistoryDir(Codex, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// Enough records to reach the in-loop consume, plus a second batch that the
	// scan would keep reading and proposing if it did not observe cancellation.
	var body bytes.Buffer
	body.WriteString(fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":\"large\",\"cwd\":%q}}\n", root))
	record := "{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"record\"}]}}\n"
	for i := 0; i < 200; i++ {
		body.WriteString(record)
	}
	if err := os.WriteFile(filepath.Join(dir, "large.jsonl"), body.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	consumed := 0
	c, err := New(Codex, root, WithConsume(func(importer.SessionTranscript) error {
		mu.Lock()
		consumed++
		mu.Unlock()
		close(entered) // the first proposal signals; it then blocks
		<-release
		return nil
	}))
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

func TestSyncAlreadyCanceledConsumesNothing(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	dir, err := HistoryDir(Codex, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "own.jsonl"), fixture(t, "codex.jsonl", root), 0600); err != nil {
		t.Fatal(err)
	}
	consumed := 0
	c, err := New(Codex, root, WithConsume(func(importer.SessionTranscript) error {
		consumed++
		return nil
	}))
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

func writeAntigravityFixture(t *testing.T, path, text string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "antigravity.sql"))
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
	inner := appendWireBytes(nil, 2, []byte(text))
	payload := appendWireBytes(nil, 19, inner)
	if _, err := db.Exec(`INSERT INTO steps(idx, step_type, metadata, step_payload) VALUES(0,14,?,?)`, []byte{}, payload); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func appendWireBytes(dst []byte, field int, value []byte) []byte {
	dst = appendWireVarint(dst, uint64(field<<3|2))
	dst = appendWireVarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendWireVarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}
