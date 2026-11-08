package execution

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const failedDiagnosticOnly = "failed execution diagnostic only"
const maxFailedDiagnosticRecords = 16

// Failed diagnostics retain canonical metadata, never completion evidence or
// raw provider output. The whole bounded batch is checked before any write.
func (e *Engine) captureFailedDiagnostics(run *ExecutionRun, task *TaskExecution, supplied []ExecutionEvidence) error {
	if len(supplied) > maxFailedDiagnosticRecords {
		return fmt.Errorf("%w: failed diagnostic count exceeds bound", ErrRunInvalid)
	}
	metadata := make([]ExecutionEvidence, 0, len(supplied))
	seen := make(map[string]bool, len(supplied))
	now := time.Now().UTC()
	for _, ev := range supplied {
		if !diagnosticIdentifier(ev.EvidenceID) || seen[ev.EvidenceID] || ev.TaskID != task.TaskID || (ev.RunID != "" && ev.RunID != run.RunID) || !diagnosticDigest(ev.OutputDigest) || ev.ExitCode < -1 || ev.ExitCode > 255 || ev.DurationMs < 0 {
			return fmt.Errorf("%w: failed diagnostic binding or metadata invalid", ErrRunInvalid)
		}
		seen[ev.EvidenceID] = true
		for _, ref := range run.Evidence {
			if ref.EvidenceID == ev.EvidenceID {
				return fmt.Errorf("%w: failed diagnostic identity already retained", ErrRunInvalid)
			}
		}
		observation := cloneProviderObservation(ev.ProviderObservation)
		if err := ValidateProviderObservation(observation); err != nil {
			return fmt.Errorf("%w: failed diagnostic observation invalid", ErrRunInvalid)
		}
		if observation != nil && (observation.RunID != run.RunID || observation.TaskID != task.TaskID || observation.CanonicalTaskID != task.CanonicalTaskID || observation.Attempt != task.Attempts || observation.Provider == "" || task.AssignedHarness == "" || executionHarnessName(observation.Provider) != executionHarnessName(task.AssignedHarness)) {
			return fmt.Errorf("%w: failed diagnostic observation binding differs", ErrRunInvalid)
		}
		if observation != nil && observation.ProcessOutcome != nil && observation.ProcessOutcome.ExitCode != ev.ExitCode {
			return fmt.Errorf("%w: failed diagnostic exit differs from process outcome", ErrRunInvalid)
		}
		captured := ev.CreatedAt
		if captured.IsZero() {
			captured = now
		}
		// Capture clocks describe retained metadata, not provider calls or grants.
		// Refuse supplied future clocks and capture before the reported end.
		if captured.After(now) || (observation != nil && observation.EndedAt != nil && (observation.EndedAt.After(now) || captured.Before(*observation.EndedAt))) {
			return fmt.Errorf("%w: failed diagnostic chronology invalid", ErrRunInvalid)
		}
		metadata = append(metadata, ExecutionEvidence{EvidenceID: ev.EvidenceID, RunID: run.RunID, TaskID: task.TaskID, ProviderObservation: observation, ExitCode: ev.ExitCode, OutputDigest: ev.OutputDigest, DurationMs: ev.DurationMs, CreatedAt: captured, Status: EvidenceInvalidated, StaleReason: failedDiagnosticOnly})
	}
	if encoded, err := json.Marshal(metadata); err != nil || len(encoded) > 64*1024 {
		return fmt.Errorf("%w: failed diagnostic serialization exceeds bound", ErrRunInvalid)
	}
	retained, err := e.oracle.recordFailedDiagnostics(metadata)
	if err != nil {
		return err
	}
	for _, ev := range retained {
		task.CollectedEvidence = append(task.CollectedEvidence, ev.EvidenceID)
		run.Evidence = append(run.Evidence, EvidenceRef{ProviderObservation: cloneProviderObservation(ev.ProviderObservation), EvidenceID: ev.EvidenceID, TaskID: task.TaskID, Type: "failed_tool_output", Digest: ev.OutputDigest, Status: EvidenceInvalidated, CapturedAt: ev.CreatedAt})
	}
	return nil
}

// Persisted references keep diagnostic identities separate even after the
// process-local oracle is recreated during a supported recovery or failover.
func hasFailedDiagnosticIdentity(run ExecutionRun, id string) bool {
	for _, ref := range run.Evidence {
		if ref.EvidenceID == id && ref.Type == "failed_tool_output" {
			return true
		}
	}
	return false
}

func diagnosticIdentifier(id string) bool {
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for _, c := range id {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

func diagnosticDigest(digest string) bool {
	if len(digest) != 71 || !strings.HasPrefix(digest, "sha256:") {
		return false
	}
	for _, c := range digest[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
