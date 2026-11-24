//go:build !governed

package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

// verifierInputOfSize builds the verifier summary for one criterion padded
// so the summary is size bytes long, or returns its refusal.
func verifierInputOfSize(t *testing.T, size int) ([]byte, error) {
	t.Helper()
	f := &verifierProofFiles{files: map[string]verifierProofFile{}, checkLine: map[string]int{}}
	proof := &verification.RuntimeProof{}
	criteria := []requiredVerifierCriterion{{ID: "C1", Text: "x"}}
	base, err := f.compactVerifierInput("head", criteria, proof)
	if err != nil {
		t.Fatal(err)
	}
	criteria[0].Text += strings.Repeat("x", size-len(base))
	return f.compactVerifierInput("head", criteria, proof)
}

// irreducibleVerifierInput is the refusal of a verifier summary one byte
// over its cap; a summary exactly at the cap is sent whole.
func irreducibleVerifierInput(t *testing.T) error {
	t.Helper()
	at, err := verifierInputOfSize(t, verifierPromptBudget)
	if err != nil || len(at) != verifierPromptBudget {
		t.Fatalf("summary at the cap: %d bytes, %v", len(at), err)
	}
	_, err = verifierInputOfSize(t, verifierPromptBudget+1)
	limit, ok := model.LimitOf(err)
	if !errors.Is(err, errVerifierProofFile) || !ok || limit.Kind != "verifier_prompt" || limit.ActualBytes != verifierPromptBudget+1 || limit.CapBytes != verifierPromptBudget {
		t.Fatalf("summary over the cap: %v %+v", err, limit)
	}
	return err
}
