package app

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
	"github.com/Zen1th53/marshal/internal/verification"
)

// The final check, like a cross-review, is independent when it runs in an
// agent session of its own: started clean, with no shared conversation and
// no worker's transcript or session resumed. It need not come from another
// provider. A provider that did none of the run's tasks is preferred when
// one can take the role; otherwise a worker's provider checks the work in a
// fresh session, and the evidence records that it did.

// finalCheckerGaps lists why provider cannot do the final check: it is not
// installed, cannot reach Marshal's tools, reported a quota or rate limit
// recently (when limits is set), or its answer does not name its model.
func (r *Runtime) finalCheckerGaps(provider string, installed []string, limits bool) []string {
	var reasons []string
	if !slices.Contains(installed, provider) {
		reasons = append(reasons, "not installed")
	}
	if !sessionmcp.MarshalProvider(provider) {
		reasons = append(reasons, "Marshal MCP unavailable")
	}
	if limits {
		if reason, limited := r.providerLimited(provider); limited {
			reasons = append(reasons, "limited: "+reason)
		}
	}
	if reason := requiredVerifierAttributionGap(provider); reason != "" {
		reasons = append(reasons, reason)
	}
	return reasons
}

// workerSessions lists the provider sessions the runtime observed for the
// run's workers, from the verified runtime proof. A verifier or reviewer
// whose session is one of them did not start clean.
func workerSessions(proof *verification.RuntimeProof) []string {
	if proof == nil {
		return nil
	}
	var out []string
	for _, p := range proof.ProviderObservations {
		out = appendObservedSessions(out, p.Observation)
	}
	return out
}

// handInWorkerSessions lists the provider sessions the runtime observed for
// the worker that produced h (governed hand-ins carry them).
func handInWorkerSessions(h marshal.HandIn) []string {
	const prefix = "process05 provider-observation "
	var out []string
	for _, record := range h.RuntimeObserved {
		if strings.HasPrefix(record.Command, prefix) {
			raw, _, _ := strings.Cut(record.Output, "\n")
			out = appendObservedSessions(out, []byte(raw))
		}
	}
	return out
}

// appendObservedSessions adds the session and thread ids of one provider
// observation to out, once each.
func appendObservedSessions(out []string, raw []byte) []string {
	var observed execution.ProviderObservation
	if json.Unmarshal(raw, &observed) != nil {
		return out
	}
	for _, id := range []string{observed.SessionID, observed.ThreadID} {
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// checkCrossReviewFreshSession refuses a cross-review that did not run in a
// session of its own: one that reports the worker's session, or one by the
// worker's own provider without proof that the runtime started it clean and
// the provider gave it a new session id. data records what was compared and
// marks the worker-provider fallback.
func checkCrossReviewFreshSession(review marshal.Review, provider string, h marshal.HandIn, data map[string]any) error {
	sessions := handInWorkerSessions(h)
	if review.SessionID != "" {
		data["cross_review_session_id"] = review.SessionID
	}
	data["cross_review_fresh_session"] = review.FreshSession
	if review.SessionID != "" && slices.Contains(sessions, review.SessionID) {
		return errors.New("independent cross-review reused the worker's session")
	}
	// A same-provider review is recorded as an explicit fallback, never
	// left implicit: it means no other provider was available.
	if sameProviderAsWorker(provider, h.Provider, h.Worker) {
		data["cross_review_same_provider_fallback"] = true
		data["cross_review_fallback_reason"] = "no provider other than the worker's was available; it reviewed in a fresh session"
		if !review.FreshSession || review.SessionID == "" {
			return errors.New("independent cross-review by the worker's provider did not run in a fresh session of its own")
		}
	}
	return nil
}
