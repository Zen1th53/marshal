package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestM16_RetroactiveSessionImporter(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	p := testPrincipal("operator-1")

	validTranscript := []byte(`{
		"session_id": "SES-CLI-9001",
		"provider": "claude-3-5-sonnet",
		"task_id": "TASK-CLI-10",
		"messages": [
			{"role": "user", "content": "How do we run the integration test suite?"},
			{"role": "assistant", "content": "Run go test -v -count=1 ./internal/integration/..."}
		],
		"success": true
	}`)

	// 1. Dry run inspection
	dryRes, err := svc.ImportSessionTranscript(ctx, p, projectID, validTranscript, true)
	if err != nil {
		t.Fatalf("dry run import: %v", err)
	}
	if len(dryRes.ImportedRecords) != 1 {
		t.Fatalf("expected 1 record previewed in dry run, got %d", len(dryRes.ImportedRecords))
	}
	// Verify dry run record properties
	preview := dryRes.ImportedRecords[0]
	if preview.Authority != model.AuthorityAgent || preview.Lifecycle != model.MemoryCandidate {
		t.Fatalf("expected low-authority candidate for imported transcript, got auth=%s life=%s", preview.Authority, preview.Lifecycle)
	}

	// 2. Real commit import
	res1, err := svc.ImportSessionTranscript(ctx, p, projectID, validTranscript, false)
	if err != nil {
		t.Fatalf("commit import: %v", err)
	}
	if len(res1.ImportedRecords) != 1 || res1.SkippedCount != 0 {
		t.Fatalf("unexpected import result: %+v", res1)
	}

	// 3. Re-importing identical transcript must produce zero new records and skip
	res2, err := svc.ImportSessionTranscript(ctx, p, projectID, validTranscript, false)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if len(res2.ImportedRecords) != 0 || res2.SkippedCount != 1 {
		t.Fatalf("expected duplicate to be skipped: %+v", res2)
	}
}

func TestM16_SessionImporterRejectsCredentials(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	p := testPrincipal("operator-1")

	// Transcript containing hardcoded AWS secret key
	taintedTranscript := []byte(`{
		"session_id": "SES-LEAK-9999",
		"provider": "unsafe-model",
		"task_id": "TASK-LEAK-99",
		"messages": [
			{"role": "user", "content": "Here is our production AWS key AKIAIOSFODNN7EXAMPLE for deploy"}
		],
		"success": false
	}`)

	_, err := svc.ImportSessionTranscript(ctx, p, projectID, taintedTranscript, false)
	if err == nil {
		t.Fatalf("expected secret firewall to reject tainted transcript containing AWS key")
	}
	if !strings.Contains(err.Error(), "secret detected") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
