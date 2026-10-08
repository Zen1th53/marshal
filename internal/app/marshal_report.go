package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// MarshalCriterionReport is the stored evidence status for one criterion.
type MarshalCriterionReport struct {
	TaskID    string
	Criterion string
	Status    string
}

// MarshalCompletionReport is a read-only projection of the durable run data.
type MarshalCompletionReport struct {
	Criteria []MarshalCriterionReport
	Untested []string
	Risks    []string
	Usage    marshal.Charge
}

// CompletionReport reports only evidence and usage already stored for a run.
func (s *MarshalService) CompletionReport(ctx context.Context, runID string) (MarshalCompletionReport, error) {
	run, _, err := s.load(ctx, runID)
	if err != nil {
		return MarshalCompletionReport{}, err
	}
	report := MarshalCompletionReport{}
	_, report.Usage, err = s.marshalUsage(ctx, runID, "")
	if err != nil {
		return report, err
	}
	decisions, err := s.Store.MarshalDecisions(ctx, runID)
	if err != nil {
		return report, err
	}
	hasUsage := false
	for _, decision := range decisions {
		hasUsage = hasUsage || decision.Data["charge_phase"] != nil
	}
	if !hasUsage {
		report.Usage.Tokens.Known = false
		report.Usage.Money.Known = false
	}
	for _, task := range run.Tasks {
		attempts := 1 + task.EvidenceAttemptBase
		for _, count := range task.ReturnsByAgent {
			attempts += count
		}
		handin, found := marshal.HandIn{}, false
		attempt := attempts
		for attempt > task.EvidenceAttemptBase {
			stored, handinErr := s.Store.GetMarshalHandIn(ctx, runID, task.PlanTaskID, attempt)
			if handinErr == nil {
				handin, found = stored.Value, true
				break
			}
			if !errors.Is(handinErr, model.ErrNotFound) {
				return report, handinErr
			}
			attempt--
		}
		for _, criterion := range task.Criteria {
			status := "not tested"
			if found {
				passed, failed := false, false
				for _, result := range handin.CheckResults {
					if result.ResultCommit != handin.ResultCommit || !containsMarshal(result.Criteria, criterion) {
						continue
					}
					passed = passed || result.Passed
					failed = failed || !result.Passed
				}
				switch {
				case passed:
					status = "verified"
				case failed:
					status = "failed"
					report.Risks = append(report.Risks, fmt.Sprintf("%s: check failed for %s", task.PlanTaskID, criterion))
				}
			}
			report.Criteria = append(report.Criteria, MarshalCriterionReport{TaskID: task.PlanTaskID, Criterion: criterion, Status: status})
			if status == "not tested" {
				report.Untested = append(report.Untested, fmt.Sprintf("%s: %s", task.PlanTaskID, criterion))
			}
		}
		if found {
			latest := attempts
			for latest > 0 {
				review, reviewErr := s.Store.GetMarshalReview(ctx, runID, task.PlanTaskID, latest)
				if reviewErr == nil {
					report.Risks = append(report.Risks, review.Value.Reasons...)
					if review.Value.Independent != nil {
						report.Risks = append(report.Risks, review.Value.Independent.Reasons...)
					}
					break
				}
				if !errors.Is(reviewErr, model.ErrNotFound) {
					return report, reviewErr
				}
				latest--
			}
		}
	}
	return report, nil
}
