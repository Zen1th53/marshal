package epistemic

import "errors"

var (
	ErrOverbroadClaim                       = errors.New("over-broad claim rejected: monolithic claims must be decomposed into scoped claims")
	ErrUncertaintyPresentedAsFact           = errors.New("uncertainty presented as fact: hedged claim cannot be VERIFIED")
	ErrWrongToolForClaim                    = errors.New("wrong tool for claim: evidence tool is unsuitable for this claim type")
	ErrUserConfirmationCannotVerifyTechnical = errors.New("epistemic invariant: user confirmation cannot verify technical or security facts")
	ErrToolExecutionFailed                  = errors.New("tool execution failed: non-zero exit code or error output cannot verify claim")
	ErrEvidenceWrongCommit                  = errors.New("evidence commit does not match claim code binding commit")
	ErrInsufficientTestCoverage             = errors.New("insufficient test coverage: test execution did not cover modified files for claim")
	ErrVerificationTheaterDetected          = errors.New("verification theater detected: test oracle derived from implementation reproduces the same bug")
	ErrCapitulationWithoutEvidence          = errors.New("no capitulation without evidence: cannot reverse verified claim without deterministic counter-evidence")
	ErrRepetitionWithoutEvidence            = errors.New("repetition effect rejected: multiple agents repeating the same assertion remains UNSUPPORTED")
	ErrCircularProvenance                   = errors.New("circular provenance detected in claim/evidence chain")
	ErrUnresolvedContradiction              = errors.New("unresolved contradiction prevents Goal SUCCESS")
	ErrMissingCriticalClaim                 = errors.New("missing critical claim: required critical claim not present in claim graph")
	ErrCriticalClaimNotVerified             = errors.New("critical claim is not VERIFIED: Goal cannot succeed")
)
