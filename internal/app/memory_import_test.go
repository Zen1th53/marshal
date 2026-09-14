package app

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestProviderSessionHistoryUsesCanonicalPersistence(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)
	principal := testPrincipal("provider-importer")
	const history = `{"timestamp":"2026-08-22T10:00:00Z","type":"session_meta","payload":{"id":"codex-production-session","model_provider":"openai","cwd":"/work/repository"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"How is runtime memory persisted?"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Canonical records are persisted in SQLite."}]}}
`

	result, err := svc.ImportProviderSessionHistory(ctx, principal, "PROJECT-local", importer.FormatCodexJSONL, []byte(history), false)
	if err != nil {
		t.Fatalf("ImportProviderSessionHistory: %v", err)
	}
	if len(result.ImportedRecords) != 1 {
		t.Fatalf("expected one committed record, got %+v", result)
	}
	rec, err := rt.Store().GetMemoryV2ByID(ctx, result.ImportedRecords[0].ID)
	if err != nil {
		t.Fatalf("canonical record lookup: %v", err)
	}
	if rec.Authority != model.AuthorityAgent || rec.Lifecycle != model.MemoryCandidate {
		t.Fatalf("native import escaped candidate policy: %+v", rec)
	}

	// A fresh service instance has no process-local digest cache. Canonical
	// SQLite deduplication still prevents a duplicate after restart.
	restarted := NewMemoryService(rt.Store())
	second, err := restarted.ImportProviderSessionHistory(ctx, principal, "PROJECT-local", importer.FormatCodexJSONL, []byte(history), false)
	if err != nil {
		t.Fatalf("restart import: %v", err)
	}
	if second.SkippedCount != 1 || len(second.ImportedRecords) != 0 {
		t.Fatalf("expected canonical restart deduplication, got %+v", second)
	}
}

// Native capture imports each message separately and re-reads a session file on
// every poll. The deterministic import ID covers project, session and body only,
// while the content digest also covers timestamps, so the same message re-read
// under a later session timestamp misses the digest check and previously
// collided on the primary key. That surfaced to the operator as
// "Memory capture incomplete" and aborted the whole poll.
func TestRepeatedSessionImportIsIdempotentAcrossTimestamps(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestMemoryService(t)
	principal := testPrincipal("repeat-importer")

	const earlier = `{"session_id":"repeat-session","provider":"codex","timestamp":"2026-08-22T10:00:00Z","messages":[{"role":"user","content":"same message body"}]}`
	const later = `{"session_id":"repeat-session","provider":"codex","timestamp":"2026-08-22T11:00:00Z","messages":[{"role":"user","content":"same message body"}]}`

	first, err := svc.ImportSessionTranscript(ctx, principal, "PROJECT-local", []byte(earlier), false)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if len(first.ImportedRecords) != 1 {
		t.Fatalf("expected one committed record, got %+v", first)
	}

	// Identical payload: caught by the digest check.
	same, err := svc.ImportSessionTranscript(ctx, principal, "PROJECT-local", []byte(earlier), false)
	if err != nil {
		t.Fatalf("identical repeat import: %v", err)
	}
	if same.SkippedCount != 1 || len(same.ImportedRecords) != 0 {
		t.Fatalf("expected digest deduplication, got %+v", same)
	}

	// Same deterministic ID, different digest: must skip, never error.
	shifted, err := svc.ImportSessionTranscript(ctx, principal, "PROJECT-local", []byte(later), false)
	if err != nil {
		t.Fatalf("duplicate import surfaced as a capture failure: %v", err)
	}
	if shifted.SkippedCount != 1 || len(shifted.ImportedRecords) != 0 {
		t.Fatalf("expected idempotent skip on import-ID collision, got %+v", shifted)
	}
}
