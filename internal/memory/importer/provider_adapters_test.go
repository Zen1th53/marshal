package importer_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestCodexJSONLImportsOnlyUserAndFinalAnswer(t *testing.T) {
	const history = `{"timestamp":"2026-08-22T10:00:00Z","type":"session_meta","payload":{"id":"codex-session-1","model_provider":"openai","cwd":"/work/repository","timestamp":"2026-08-22T10:00:00Z"}}
{"timestamp":"2026-08-22T10:00:01Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"system-only material"}]}}
{"timestamp":"2026-08-22T10:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Why did the test fail?"}]}}
{"timestamp":"2026-08-22T10:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"unverified intermediate reasoning"}]}}
{"timestamp":"2026-08-22T10:00:04Z","type":"response_item","payload":{"type":"function_call_output","output":"tool-secret-must-not-be-imported"}}
{"timestamp":"2026-08-22T10:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"The verified fix is to enable busy_timeout."}]}}
`

	imp := importer.NewSessionImporter(importer.Config{})
	result, err := imp.ImportProviderHistory(context.Background(), "PROJECT-1", importer.FormatCodexJSONL, []byte(history), false)
	if err != nil {
		t.Fatalf("ImportProviderHistory: %v", err)
	}
	if len(result.ImportedRecords) != 1 {
		t.Fatalf("expected one candidate, got %+v", result)
	}
	rec := result.ImportedRecords[0]
	if rec.Authority != model.AuthorityAgent || rec.Lifecycle != model.MemoryCandidate {
		t.Fatalf("provider imports must remain low-authority candidates: %+v", rec)
	}
	if strings.Contains(rec.Body, "system-only") || strings.Contains(rec.Body, "intermediate") || strings.Contains(rec.Body, "tool-secret") {
		t.Fatalf("non-public provider content leaked into record: %q", rec.Body)
	}
	if !strings.Contains(rec.Body, "Why did the test fail?") || !strings.Contains(rec.Body, "verified fix") {
		t.Fatalf("expected public conversation content, got %q", rec.Body)
	}
	if got := rec.ExtMeta["source_format"]; got != string(importer.FormatCodexJSONL) {
		t.Fatalf("source format = %v", got)
	}
	if got := rec.ExtMeta["source_cwd"]; got != "/work/repository" {
		t.Fatalf("source cwd = %v", got)
	}
	if _, exists := rec.ExtMeta["execution_success"]; exists {
		t.Fatal("provider adapter must not invent an execution outcome")
	}
}

func TestClaudeJSONLExcludesThinkingAndTools(t *testing.T) {
	const history = `{"type":"user","sessionId":"claude-session-1","timestamp":"2026-08-20T12:00:00Z","cwd":"/work/repository","gitBranch":"feature/memory","message":{"role":"user","content":"Continue the memory task"}}
{"type":"assistant","sessionId":"claude-session-1","timestamp":"2026-08-20T12:01:00Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"private reasoning"},{"type":"tool_use","name":"Read","input":{"path":"secret"}},{"type":"text","text":"Validation completed successfully."}]}}
{"type":"user","sessionId":"claude-session-1","timestamp":"2026-08-20T12:02:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"sensitive tool output"}]}}
`

	imp := importer.NewSessionImporter(importer.Config{})
	result, err := imp.ImportProviderHistory(context.Background(), "PROJECT-1", importer.FormatClaudeJSONL, []byte(history), false)
	if err != nil {
		t.Fatalf("ImportProviderHistory: %v", err)
	}
	rec := result.ImportedRecords[0]
	if strings.Contains(rec.Body, "private reasoning") || strings.Contains(rec.Body, "sensitive tool output") {
		t.Fatalf("hidden provider content leaked into record: %q", rec.Body)
	}
	if !strings.Contains(rec.Body, "Continue the memory task") || !strings.Contains(rec.Body, "Validation completed") {
		t.Fatalf("expected public conversation content, got %q", rec.Body)
	}
	if got := rec.ExtMeta["source_branch"]; got != "feature/memory" {
		t.Fatalf("source branch = %v", got)
	}
}

func TestProviderImportIsDeterministicAndSecretSafe(t *testing.T) {
	const safe = `{"type":"user","sessionId":"claude-session-2","message":{"role":"user","content":"Document the runtime behavior"}}
`
	imp := importer.NewSessionImporter(importer.Config{})
	first, err := imp.ImportProviderHistory(context.Background(), "PROJECT-1", importer.FormatClaudeJSONL, []byte(safe), false)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	second, err := imp.ImportProviderHistory(context.Background(), "PROJECT-1", importer.FormatClaudeJSONL, []byte(safe), false)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if len(first.ImportedRecords) != 1 || len(second.ImportedRecords) != 1 || first.ImportedRecords[0].ID != second.ImportedRecords[0].ID || first.ImportedRecords[0].ContentDigest != second.ImportedRecords[0].ContentDigest {
		t.Fatalf("expected deterministic normalized record, got %+v", second)
	}

	const tainted = `{"type":"user","sessionId":"claude-secret","message":{"role":"user","content":"Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345"}}
`
	_, err = imp.ImportProviderHistory(context.Background(), "PROJECT-1", importer.FormatClaudeJSONL, []byte(tainted), false)
	if !errors.Is(err, security.ErrSecretDetected) {
		t.Fatalf("expected secret rejection, got %v", err)
	}
}

func TestProviderHistoryRejectsUnknownMalformedAndOversizedInput(t *testing.T) {
	imp := importer.NewSessionImporter(importer.Config{})
	ctx := context.Background()
	if _, err := imp.ImportProviderHistory(ctx, "PROJECT-1", "unknown", []byte("{}"), false); err == nil {
		t.Fatal("expected unknown format rejection")
	}
	if _, err := imp.ImportProviderHistory(ctx, "PROJECT-1", importer.FormatCodexJSONL, []byte("{torn"), false); err == nil {
		t.Fatal("expected malformed JSONL rejection")
	}
	oversized := make([]byte, (16<<20)+1)
	if _, err := imp.ImportProviderHistory(ctx, "PROJECT-1", importer.FormatCodexJSONL, oversized, false); err == nil {
		t.Fatal("expected oversized history rejection")
	}
	mixedSessions := `{"type":"user","sessionId":"one","message":{"content":"first"}}
{"type":"user","sessionId":"two","message":{"content":"second"}}
`
	if _, err := imp.ImportProviderHistory(ctx, "PROJECT-1", importer.FormatClaudeJSONL, []byte(mixedSessions), false); err == nil {
		t.Fatal("expected mixed-session history rejection")
	}
}
