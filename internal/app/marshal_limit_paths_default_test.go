//go:build governed

package app

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

// This edition's final check sends no bounded verifier summary, so the pause
// path is exercised with the same limit the other edition's summary raises.
func irreducibleVerifierInput(t *testing.T) error {
	t.Helper()
	return model.NewLimitError(errors.New("verifier proof file: verifier summary exceeds 40960 bytes"), model.Limit{
		Kind: "verifier_prompt", Subject: "final check summary of 1 criteria and 0 checks", ActualBytes: 40961, CapBytes: 40960,
		Action: "Split the final check: amend the plan so its criteria and checks are spread over smaller tasks, approve it, then /marshal use-plan."})
}
