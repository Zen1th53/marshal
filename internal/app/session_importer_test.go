package app

import (
	"context"
	"strings"
	"sync"
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

	// 4. Provider metadata may change while visible content stays the same.
	// The deterministic record ID still identifies the same memory and must not
	// turn a harmless re-import into a primary-key constraint failure.
	withCWD := []byte(strings.Replace(string(validTranscript),
		`"task_id": "TASK-CLI-10",`,
		`"task_id": "TASK-CLI-10", "cwd": "/tmp/exported-later",`, 1))
	res3, err := svc.ImportSessionTranscript(ctx, p, projectID, withCWD, false)
	if err != nil {
		t.Fatalf("re-import with changed metadata: %v", err)
	}
	if len(res3.ImportedRecords) != 0 || res3.SkippedCount != 1 {
		t.Fatalf("expected stable-ID duplicate to be skipped: %+v", res3)
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

// Two MARSHAL sessions watching the same provider history import the same
// message at the same moment. Both can find the record absent before either
// writes it; the one that loses must skip, not fail the whole capture.
func TestM16_ConcurrentImportsOfOneMessageBothSucceed(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	p := testPrincipal("operator-1")
	transcript := []byte(`{
		"session_id": "SES-RACE-1",
		"provider": "codex",
		"messages": [{"role": "user", "content": "Two watchers import this message together."}],
		"success": true
	}`)

	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	imported := make(chan int, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.ImportSessionTranscript(ctx, p, projectID, transcript, false)
			if err != nil {
				errs <- err
				return
			}
			imported <- len(res.ImportedRecords)
		}()
	}
	wg.Wait()
	close(errs)
	close(imported)
	for err := range errs {
		t.Errorf("concurrent import failed: %v", err)
	}
	total := 0
	for n := range imported {
		total += n
	}
	if total != 1 {
		t.Fatalf("expected exactly one import to commit the record, got %d", total)
	}
}
