package app

import (
	"context"
	"errors"
	"fmt"
	"html"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/verification"
)

// References address runtime artifacts in the exact stored hand-in, never claims.
// A file the reviewer read for context but the hand-in did not change is not
// a hand-in artifact: it is dropped from the evidence instead of stopping the
// review, and the review must still cite at least one resolved artifact. Any
// other unresolved reference is refused. Unmatched checks are likewise dropped;
// callers record the notes, and at least one artifact must still resolve.
func resolveReviewReferences(review marshal.Review, h marshal.HandIn) ([]string, error) {
	refs, _, err := resolveReviewReferencesWithNotes(review, h)
	return refs, err
}

// Notes are stored with the review and its decision event, never as evidence.
func resolveReviewReferencesWithNotes(review marshal.Review, h marshal.HandIn) ([]string, []string, error) {
	if len(review.EvidenceRefs) == 0 {
		return nil, nil, errors.New("independent review has no evidence references")
	}
	var refs, notes []string
	for _, ref := range review.EvidenceRefs {
		resolved := ref == "result:"+h.ResultCommit && h.ResultCommit != "" || ref == "diff" && h.Diff != ""
		if path, ok := strings.CutPrefix(ref, "file:"); ok {
			if !slices.Contains(h.FilesTouched, path) {
				if path == "" {
					return nil, nil, fmt.Errorf("unresolved independent review evidence: %q", ref)
				}
				notes = append(notes, fmt.Sprintf("dropped unresolved review reference %q: file is not a hand-in artifact", ref))
				continue
			}
			resolved = true
		}
		if strings.HasPrefix(ref, "check:") {
			for _, check := range h.CheckResults {
				if check.ResultCommit == h.ResultCommit && normalizeCheckReference(strings.TrimPrefix(ref, "check:")) == normalizeCheckReference(check.Command) {
					ref = "check:" + check.Command
					resolved = true
					break
				}
			}
			if !resolved {
				notes = append(notes, fmt.Sprintf("dropped unresolved review reference %q: check is not bound to the hand-in commit", ref))
				continue
			}
		}
		if !resolved {
			return nil, nil, fmt.Errorf("unresolved independent review evidence: %q", ref)
		}
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		return nil, nil, errors.New("independent review cites no artifact of the hand-in")
	}
	return refs, notes, nil
}

// Decode JSON Unicode escapes without interpreting shell backslashes, then
// HTML entities. This changes matching only; evidence retains the stored
// command and must still belong to the exact hand-in commit.
var checkUnicodeEscape = regexp.MustCompile(`\\u[0-9a-fA-F]{4}`)

func normalizeCheckReference(command string) string {
	command = checkUnicodeEscape.ReplaceAllStringFunc(command, func(escaped string) string {
		decoded, err := strconv.Unquote(`"` + escaped + `"`)
		if err != nil {
			return escaped
		}
		return decoded
	})
	return html.UnescapeString(command)
}

func (s *MarshalService) captureIndependentVerification(ctx context.Context, runID string, run marshal.Run, head string, session verification.Session) error {
	if err := s.validateVerifierRuntimeProof(ctx, run, head, session); err != nil {
		return err
	}
	expectedInput := verifierInputDigest(run, head, session)
	record, err := s.IndependentVerify(ctx, run, head, session)
	if session.RuntimeProof == nil {
		expectedInput = verifierInputDigest(run, head, session)
	} else if verifierInputDigest(run, head, session) != expectedInput {
		err = errors.Join(err, errors.New("runtime verifier proof changed during review"))
	}
	err = errors.Join(err, s.validateVerifierRuntimeProof(ctx, run, head, session))
	if record.Commit != head || record.Reviewer == "" || record.Provider == "" || record.InputDigest == "" || record.InputDigest != expectedInput || record.Verdict != "pass" {
		err = errors.Join(err, errors.New("independent verifier evidence is incomplete or refused"))
	}
	data := map[string]any{"verifier": record}
	var workers []string
	for _, task := range run.Tasks {
		workers = append(workers, task.Worker)
	}
	data["worker_providers"] = workers
	err = errors.Join(err, s.checkVerifierFreshSession(record, workers, workerSessions(session.RuntimeProof), data))
	if session.RuntimeProof != nil {
		data["runtime_proof"] = session.RuntimeProof
	}
	if err != nil {
		data["verification_error"] = err.Error()
	}
	if storeErr := s.record(context.WithoutCancel(ctx), runID, "", events.EventTypeMarshalVerifierRecorded, data); storeErr != nil {
		return errors.Join(err, storeErr)
	}
	return err
}

// Every verification stop must survive reload with its cause and recovery action.
func (s *MarshalService) pauseUnavailableVerifier(ctx context.Context, runID string, run marshal.Run, revision int64, cause error) error {
	if cause == nil {
		return nil
	}
	if cause.Error() == "verification refused" {
		return cause
	}
	pause := marshal.Pause{Reason: cause.Error(), Resolutions: []string{"inspect the verification error, repair the check output/source or re-review the current goal and plan after a runtime upgrade, then /marshal resume to rerun verification"}, ResumeState: marshal.Verifying, Code: marshal.CodeVerificationStopped, Action: marshal.PauseResume}
	data := map[string]any{"reason": cause.Error(), "role": "independent-verifier"}
	if errors.Is(cause, errIndependentVerifierUnavailable) || cause.Error() == "independent verifier is unavailable" || cause.Error() == "verifier is unavailable" {
		// The person must make an independent checker available; the
		// exclusions stay in the detail and the recorded event.
		pause.Reason, pause.Detail = "The final check has no checker: each installed AI tool is limited or cannot take this role.", cause.Error()
		pause.Resolutions = []string{independentCheckerResolution()}
		data["summary"] = pause.Reason
	} else if reason, next, ok := plainContextVerificationStop(cause); ok {
		// A bare refusal code reads as a plain sentence; the code stays in
		// the detail and the recorded event.
		pause.Reason, pause.Resolutions, pause.Detail = reason, []string{next}, cause.Error()
		data["summary"] = pause.Reason
	}
	data["resolutions"] = pause.Resolutions
	ctx = context.WithoutCancel(ctx)
	pauseMarshal(&run, pause)
	if err := s.save(ctx, runID, run, revision); err != nil {
		return errors.Join(cause, err)
	}
	return errors.Join(cause, s.record(ctx, runID, "", events.EventTypeMarshalEscalated, data))
}

// independentCheckerResolution says what makes a final checker available.
func independentCheckerResolution() string {
	return "Enable and sign in to a qualified AI tool allowed by final-provider, or wait until its limit passes. Then press Enter to retry."
}

// checkVerifierFreshSession refuses a final check that did not run in a
// session of its own. A verifier of any provider must not report a worker's
// session or the Marshal's own conversation. One of a worker's provider is
// accepted only with proof that it started clean: the runtime started its
// session fresh, and the provider reported a new session id. data records
// the sessions compared and whether the worker-provider fallback was used.
func (s *MarshalService) checkVerifierFreshSession(record marshal.VerifierEvidence, workers, sessions []string, data map[string]any) error {
	data["verifier_fresh_session"] = record.FreshSession
	if len(sessions) > 0 {
		data["worker_sessions"] = sessions
	}
	var err error
	if record.SessionID != "" && slices.Contains(sessions, record.SessionID) {
		err = errors.New("independent verifier reused a worker's session")
	}
	if model, ok := s.Model.(interface{ MarshalConversationID() string }); ok && record.SessionID != "" && record.SessionID == model.MarshalConversationID() {
		err = errors.Join(err, errors.New("independent verifier reused the Marshal's session"))
	}
	if sameProviderAsWorker(record.Provider, workers...) {
		data["verifier_same_provider_fallback"] = true
		data["verifier_fallback_reason"] = "no provider that did none of the run's tasks could do the final check; " + record.Provider + " checked it in a fresh session"
		if !record.FreshSession || record.SessionID == "" {
			err = errors.Join(err, errors.New("independent verifier of a worker's provider did not run in a fresh session of its own"))
		}
	}
	return err
}

// A missing reviewer leaves the hand-in pending until an independent session can review it.
// The person's stop or a shutdown during the review is not a failure: the
// hand-in stays pending and the run is not paused.
func (s *MarshalService) pauseUnavailableCrossReviewer(ctx context.Context, runID string, run marshal.Run, revision int64, cause error) error {
	if ctx.Err() != nil || errors.Is(cause, context.Canceled) {
		return cause
	}
	ctx = context.WithoutCancel(ctx)
	task := firstMarshalTask(run, marshal.HandedIn)
	reason := "Independent cross-review is unavailable."
	if task != "" {
		reason = "Independent cross-review of task " + task + " is unavailable."
	}
	pauseMarshal(&run, marshal.Pause{Reason: reason, Detail: boundedRedacted("review: "+cause.Error(), runNoticeDetailLimit), TaskID: task, Resolutions: []string{"Make a reviewer available, then /marshal resume to retry the hand-in review."}, ResumeState: run.State, Code: marshal.CodeReviewFailed, Action: marshal.PauseResume})
	if err := s.save(ctx, runID, run, revision); err != nil {
		return errors.Join(cause, err)
	}
	return errors.Join(cause, s.record(ctx, runID, "", events.EventTypeMarshalEscalated, map[string]any{"reason": RedactApprovalText(cause.Error()), "summary": reason, "role": "independent-cross-reviewer"}))
}
