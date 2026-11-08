package epistemic

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestDeterministicEvidenceRefDeterminism(t *testing.T) {
	id := "EV-DET-1"
	tool := "go test -v ./..."
	commit := "abc12345"
	summary := "all 10 tests passed"
	files := []string{"internal/epistemic/engine.go"}

	ref1 := NewDeterministicEvidenceRef(id, tool, commit, summary, files)
	ref2 := NewDeterministicEvidenceRef(id, tool, commit, summary, files)

	// 1. Identical inputs must yield identical digests
	if ref1.Digest != ref2.Digest {
		t.Fatalf("expected deterministic digests to be equal, got %q vs %q", ref1.Digest, ref2.Digest)
	}

	// 2. Digest must not contain fake timestamp prefix "sha256:det-"
	if strings.Contains(ref1.Digest, "sha256:det-") {
		t.Fatalf("digest contains fake det- timestamp prefix: %s", ref1.Digest)
	}

	// 3. Digest must match valid 64-hex SHA-256 pattern
	pattern := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	if !pattern.MatchString(ref1.Digest) {
		t.Fatalf("digest does not match sha256 hex format: %s", ref1.Digest)
	}

	// 4. Changing input changes digest
	ref3 := NewDeterministicEvidenceRef(id, tool, commit, "different summary", files)
	if ref1.Digest == ref3.Digest {
		t.Fatalf("expected different digest when summary changed, got same %s", ref1.Digest)
	}
}

func TestPostMortemCardDefaultsHonest(t *testing.T) {
	card := PostMortemCard{
		GoalID:       "goal-defaults",
		GoalRevision: 1,
		GeneratedAt:  time.Now().UTC(),
	}

	md := GeneratePostMortemCard(card)

	// In Wave 1, optimistic fakes like "Zero rework required" or "All initial assumptions held"
	// are removed in favor of honest "None recorded"
	if strings.Contains(md, "Zero rework required") {
		t.Fatalf("postmortem contains optimistic fake 'Zero rework required':\n%s", md)
	}
	if strings.Contains(md, "All initial assumptions held") {
		t.Fatalf("postmortem contains optimistic fake 'All initial assumptions held':\n%s", md)
	}
	if strings.Contains(md, "No notable failures recorded") {
		t.Fatalf("postmortem contains optimistic fake 'No notable failures recorded':\n%s", md)
	}
	if strings.Contains(md, "Default routes optimal") {
		t.Fatalf("postmortem contains optimistic fake 'Default routes optimal':\n%s", md)
	}

	// Must contain honest "None recorded"
	count := strings.Count(md, "- None recorded")
	if count < 4 {
		t.Fatalf("expected at least 4 '- None recorded' entries, got %d in:\n%s", count, md)
	}
}
