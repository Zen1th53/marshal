package importer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT94SessionTranscriptImportIdempotency(t *testing.T) {
	imp := importer.NewSessionImporter(importer.Config{})
	ctx := context.Background()

	rawTranscript := `{"session_id":"sess-123","provider":"codex","task_id":"TASK-01","messages":[{"role":"user","content":"Fix SQLite timeout"},{"role":"assistant","content":"Added busy_timeout=5000 in configuration."}],"success":true}`

	// 1. First import
	res1, err := imp.ImportRawJSON(ctx, "PROJ-1", []byte(rawTranscript), false)
	if err != nil {
		t.Fatalf("ImportRawJSON: %v", err)
	}
	if len(res1.ImportedRecords) != 1 {
		t.Fatalf("expected 1 imported record, got %d", len(res1.ImportedRecords))
	}
	rec := res1.ImportedRecords[0]
	if rec.Kind != model.MemoryKindEpisodic {
		t.Fatalf("imported record must be episodic, got %s", rec.Kind)
	}
	if rec.Lifecycle != model.MemoryCandidate {
		t.Fatalf("imported record must have candidate lifecycle, got %s", rec.Lifecycle)
	}

	// 2. Normalization is deterministic. Durable idempotency is enforced by the
	// canonical store rather than fallible process-local importer state.
	res2, err := imp.ImportRawJSON(ctx, "PROJ-1", []byte(rawTranscript), false)
	if err != nil {
		t.Fatalf("Second ImportRawJSON: %v", err)
	}
	if len(res2.ImportedRecords) != 1 || res2.ImportedRecords[0].ID != rec.ID || res2.ImportedRecords[0].ContentDigest != rec.ContentDigest {
		t.Fatalf("expected deterministic normalization, got: %+v", res2)
	}
}

func TestT94TornOrCorruptedJSONHandledSafely(t *testing.T) {
	imp := importer.NewSessionImporter(importer.Config{})
	ctx := context.Background()

	// Partially written / torn JSON
	tornJSON := `{"session_id":"sess-torn","messages":[{"role":"user"`

	_, err := imp.ImportRawJSON(ctx, "PROJ-1", []byte(tornJSON), false)
	if err == nil {
		t.Fatal("expected error when importing malformed/torn JSON")
	}
}

func TestT94SecretsInImportedTranscriptRejected(t *testing.T) {
	imp := importer.NewSessionImporter(importer.Config{})
	ctx := context.Background()

	secretTranscript := `{"session_id":"sess-sec","provider":"claude","task_id":"TASK-02","messages":[{"role":"assistant","content":"Here is the key: ghp_1234567890abcdefghijklmnopqrstuvwxyzAB"}]}`

	_, err := imp.ImportRawJSON(ctx, "PROJ-1", []byte(secretTranscript), false)
	if !errors.Is(err, security.ErrSecretDetected) {
		t.Fatalf("expected ErrSecretDetected from firewall on secret-bearing transcript, got: %v", err)
	}
}
