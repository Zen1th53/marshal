package marshal

import (
	"strings"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// VerificationRetry is what the person does about a verification stop that
// only a rerun can clear: Enter resumes the run and reruns the final check.
const VerificationRetry = "Press Enter to let the Marshal retry."

// RequiredVerifierOutputInvalid is the code of a verifier answer that does not
// fit the required assessment shape (app.errRequiredVerifierOutput).
const RequiredVerifierOutputInvalid = "REQUIRED_VERIFIER_OUTPUT_INVALID"

var plainContextStops = []struct{ code, text string }{
	{projectcontext.ErrReviewPending.Error(), "Marshal could not finish checking the run: the final check could not be tied to the project's accepted design."},
	{projectcontext.ErrChanged.Error(), "Marshal could not finish checking the run: the project changed while it was being checked."},
	{projectcontext.ErrMissing.Error(), "Marshal could not finish checking the run: part of what the final check needs is missing."},
	{projectcontext.ErrBudget.Error(), "Marshal could not finish checking the run: what the final check had to send was too large."},
	{projectcontext.ErrInvalid.Error(), "Marshal could not finish checking the run: the project's saved context did not pass its own checks."},
	{RequiredVerifierOutputInvalid, "Marshal could not finish checking the run: the independent checker's answer was incomplete."},
}

// PlainVerificationStop returns the plain sentence and next step for a
// verification stop whose raw reason is one of the runtime's required-context
// or verifier-output refusal codes, or false for any other reason (a failed
// check keeps its own words). It is display text only: no gate reads it, and
// the raw reason stays in the pause detail and the recorded event.
func PlainVerificationStop(raw string) (reason, next string, ok bool) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "verification stopped: ")
	raw = strings.TrimPrefix(raw, "integrated result is not verified: ")
	for _, stop := range plainContextStops {
		if strings.HasPrefix(raw, stop.code) {
			return stop.text, VerificationRetry, true
		}
	}
	return "", "", false
}
