package execution

import (
	"strings"
	"testing"
	"time"
)

func TestJournal_AppendAndVerifyIntegrity(t *testing.T) {
	js := NewMemoryJournalStore()
	runID := "run-test-01"

	for i := 1; i <= 5; i++ {
		ev, err := js.Append(JournalEvent{
			RunID:     runID,
			TaskID:    "task-1",
			Actor:     "MARSHAL_ENGINE",
			EventType: "PHASE_CHANGE",
			Summary:   "Transition phase",
		})
		if err != nil {
			t.Fatalf("failed to append event %d: %v", i, err)
		}
		if ev.Digest == "" {
			t.Fatalf("expected non-empty digest for event %d", i)
		}
	}

	if err := js.VerifyIntegrity(runID); err != nil {
		t.Fatalf("expected integrity verification to pass, got: %v", err)
	}
}

func TestJournal_SecretRedaction(t *testing.T) {
	js := NewMemoryJournalStore()
	runID := "run-secret-01"

	ev, err := js.Append(JournalEvent{
		RunID:       runID,
		Actor:       "worker",
		EventType:   "COMMAND_OUTPUT",
		Summary:     "Auth header: Bearer sk-ant-api03-secretkey1234567890",
		PayloadJSON: `{"api_key": "sk-1234567890abcdef1234", "password": "supersecretpassword123"}`,
	})
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	if strings.Contains(ev.Summary, "sk-ant-api03-secretkey1234567890") {
		t.Fatalf("Bearer secret was not redacted from summary: %s", ev.Summary)
	}
	if strings.Contains(ev.PayloadJSON, "sk-1234567890abcdef1234") {
		t.Fatalf("API key was not redacted from payload: %s", ev.PayloadJSON)
	}
	if strings.Contains(ev.PayloadJSON, "supersecretpassword123") {
		t.Fatalf("Password was not redacted from payload: %s", ev.PayloadJSON)
	}
}

func TestJournal_TamperDetection(t *testing.T) {
	js := NewMemoryJournalStore()
	runID := "run-tamper-01"

	for i := 1; i <= 3; i++ {
		_, err := js.Append(JournalEvent{
			RunID:     runID,
			Actor:     "engine",
			EventType: "TICK",
			Summary:   "Normal operation",
		})
		if err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	// Verify intact
	if err := js.VerifyIntegrity(runID); err != nil {
		t.Fatalf("initial verification should pass: %v", err)
	}

	// Tamper with event 1 in-place
	js.mu.Lock()
	js.events[runID][1].Summary = "Adversary modified summary"
	js.mu.Unlock()

	// VerifyIntegrity MUST FAIL
	err := js.VerifyIntegrity(runID)
	if err == nil {
		t.Fatalf("expected tamper detection error, got nil")
	}
	if !strings.Contains(err.Error(), "journal tamper detected") {
		t.Fatalf("expected 'journal tamper detected' in error, got: %v", err)
	}
}

func TestJournal_EventDeletionTamperDetection(t *testing.T) {
	js := NewMemoryJournalStore()
	runID := "run-deletion-01"

	for i := 1; i <= 4; i++ {
		_, _ = js.Append(JournalEvent{
			RunID:     runID,
			Actor:     "engine",
			EventType: "STEP",
			Timestamp: time.Now().UTC(),
		})
	}

	// Drop middle event (event at index 1)
	js.mu.Lock()
	js.events[runID] = append(js.events[runID][:1], js.events[runID][2:]...)
	js.mu.Unlock()

	err := js.VerifyIntegrity(runID)
	if err == nil {
		t.Fatalf("expected tamper detection when event is deleted, got nil")
	}
}
