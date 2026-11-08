package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// Only the stored acceptance for the current task attempt can supply review
// evidence. Neither a worker claim nor a review of an older result qualifies.
func (s *MarshalService) acceptedCrossReviewPayloads(ctx context.Context, runID string, run marshal.Run) (map[string][]byte, error) {
	payloads := map[string][]byte{}
	for _, task := range run.Tasks {
		if task.State != marshal.Accepted && task.State != marshal.Merged {
			continue
		}
		attempt := 1 + task.EvidenceAttemptBase + taskReturns(task)
		stored, err := s.Store.GetMarshalReview(ctx, runID, task.PlanTaskID, attempt)
		if errors.Is(err, model.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		review := stored.Value
		independent := review.Independent
		if review.Verdict != marshal.VerdictAccept || independent == nil || independent.Verdict != marshal.VerdictAccept || independent.Reviewer == "" || independent.Provider == "" {
			continue
		}
		handin, err := s.Store.GetMarshalHandIn(ctx, runID, task.PlanTaskID, attempt)
		if err != nil {
			return nil, err
		}
		h := handin.Value
		if h.ResultCommit == "" || h.ResultCommit != task.ResultCommit || h.Provider == "" || sameProviderAsWorker(independent.Provider, h.Provider, h.Worker, task.Worker) {
			continue
		}
		refs, err := resolveReviewReferences(marshal.Review{EvidenceRefs: independent.EvidenceRefs}, h)
		if err != nil {
			continue
		}
		raw, err := json.Marshal(struct {
			Run              string          `json:"run"`
			Task             string          `json:"task"`
			Attempt          int             `json:"attempt"`
			ResultCommit     string          `json:"result_commit"`
			WorkerProvider   string          `json:"worker_provider"`
			ReviewerProvider string          `json:"reviewer_provider"`
			Reviewer         string          `json:"reviewer"`
			Verdict          marshal.Verdict `json:"verdict"`
			EvidenceRefs     []string        `json:"evidence_refs"`
		}{runID, task.PlanTaskID, attempt, h.ResultCommit, h.Provider, independent.Provider, independent.Reviewer, independent.Verdict, refs})
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256([]byte(task.PlanTaskID))
		payloads["reviews/"+hex.EncodeToString(sum[:])+".json"] = raw
	}
	return payloads, nil
}
