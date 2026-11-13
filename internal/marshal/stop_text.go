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

// VerificationNotAccepted opens the plain reason of a final check that ran and
// did not pass; the runtime appends each criterion not accepted and why.
const VerificationNotAccepted = "The final check did not accept the result."

// VerificationNotAcceptedNext is what the person does about it: the result
// itself has to change, so a plain retry checks the same result again.
const VerificationNotAcceptedNext = "Fix the project or amend the plan for these points; press Enter only to check the same result again."

// PlainVerificationFailed returns the plain reason for a failed-check pause an
// older build recorded as the bare code ("verification failed: <DECISION>"),
// or false for any other reason. Display text only.
func PlainVerificationFailed(raw string) (string, bool) {
	if strings.HasPrefix(strings.TrimSpace(raw), "verification failed: ") {
		return VerificationNotAccepted + " Its reasons were not kept by the build that ran it; press Enter to check again and see them.", true
	}
	return "", false
}

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
