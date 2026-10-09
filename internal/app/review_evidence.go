package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/verification"
)

// References address runtime artifacts in the exact stored hand-in, never claims.
func resolveReviewReferences(review marshal.Review, h marshal.HandIn) error {
	if len(review.EvidenceRefs) == 0 {
		return errors.New("independent review has no evidence references")
	}
	for _, ref := range review.EvidenceRefs {
		resolved := ref == "result:"+h.ResultCommit && h.ResultCommit != "" || ref == "diff" && h.Diff != ""
		if strings.HasPrefix(ref, "file:") {
			resolved = slices.Contains(h.FilesTouched, strings.TrimPrefix(ref, "file:"))
		}
		if strings.HasPrefix(ref, "check:") {
			for _, check := range h.CheckResults {
				if check.ResultCommit == h.ResultCommit && ref == "check:"+check.Command {
					resolved = true
				}
			}
		}
		if !resolved {
			return fmt.Errorf("unresolved independent review evidence: %q", ref)
		}
	}
	return nil
}

func (s *MarshalService) captureIndependentVerification(ctx context.Context, runID string, run marshal.Run, head string, session verification.Session) error {
	record, err := s.IndependentVerify(ctx, run, head, session)
	if record.Commit != head || record.Reviewer == "" || record.Provider == "" || record.InputDigest == "" || record.InputDigest != verifierInputDigest(run, head, session) || record.Verdict != "pass" {
		err = errors.Join(err, errors.New("independent verifier evidence is incomplete or refused"))
	}
	data := map[string]any{"verifier": record}
	if err != nil {
		data["verification_error"] = err.Error()
	}
	if storeErr := s.record(context.WithoutCancel(ctx), runID, "", events.EventTypeMarshalVerifierRecorded, data); storeErr != nil {
		return errors.Join(err, storeErr)
	}
	return err
}
