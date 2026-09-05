package epistemic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var (
	timestampRegex = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T\s]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	hexAddressRegex = regexp.MustCompile(`0x[0-9a-fA-F]+`)
)

// FailureFingerprint records a normalized failure signature across attempts.
type FailureFingerprint struct {
	Fingerprint string `json:"fingerprint"`
	Normalized  string `json:"normalized"`
	Occurrences int    `json:"occurrences"`
	LastTaskID  string `json:"last_task_id"`
	Action      string `json:"action"` // "RETRY_ALLOWED", "CUT_RETRY_AND_ESCALATE"
}

// FingerprintRegistry tracks recurring failure patterns to prevent unproductive retry loops.
// Invariant: Two identical failure signatures trigger immediate escalation review and cut blind retries.
type FingerprintRegistry struct {
	mu           sync.RWMutex
	fingerprints map[string]*FailureFingerprint
}

func NewFingerprintRegistry() *FingerprintRegistry {
	return &FingerprintRegistry{
		fingerprints: make(map[string]*FailureFingerprint),
	}
}

// RecordFailure evaluates an error output/stacktrace, normalizes it, and returns a FailureFingerprint.
func (r *FingerprintRegistry) RecordFailure(taskID, rawError string) FailureFingerprint {
	r.mu.Lock()
	defer r.mu.Unlock()

	norm := normalizeError(rawError)
	h := sha256.Sum256([]byte(norm))
	fpHash := hex.EncodeToString(h[:16])

	entry, exists := r.fingerprints[fpHash]
	if !exists {
		entry = &FailureFingerprint{
			Fingerprint: fpHash,
			Normalized:  norm,
			Occurrences: 1,
			LastTaskID:  taskID,
			Action:      "RETRY_ALLOWED",
		}
		r.fingerprints[fpHash] = entry
		return *entry
	}

	entry.Occurrences++
	entry.LastTaskID = taskID
	if entry.Occurrences >= 2 {
		entry.Action = "CUT_RETRY_AND_ESCALATE"
	}

	return *entry
}

// ShouldCutRetry returns true if a failure has been repeated >= 2 times.
func (r *FingerprintRegistry) ShouldCutRetry(rawError string) (bool, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	norm := normalizeError(rawError)
	h := sha256.Sum256([]byte(norm))
	fpHash := hex.EncodeToString(h[:16])

	if entry, exists := r.fingerprints[fpHash]; exists && entry.Occurrences >= 2 {
		return true, fmt.Sprintf("repeated failure fingerprint %s observed %d times; cutting blind retry and requesting route escalation",
			fpHash, entry.Occurrences)
	}

	return false, ""
}

func normalizeError(raw string) string {
	s := strings.TrimSpace(raw)
	s = timestampRegex.ReplaceAllString(s, "[TIMESTAMP]")
	s = hexAddressRegex.ReplaceAllString(s, "[ADDR]")
	// Collapse multiple spaces/newlines
	lines := strings.Split(s, "\n")
	var cleaned []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.Join(cleaned, "\n")
}
