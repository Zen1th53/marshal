package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// MarshalCriterionReport is the stored evidence status for one criterion.
type MarshalCriterionReport struct {
	TaskID     string
	Criterion  string
	Mixed      bool
	Incomplete bool
	Status     string
}

// MarshalCompletionReport is a read-only projection of the durable run data.
type MarshalCompletionReport struct {
	ReviewLabel string
	ChecksLabel string
	Verifiers   []marshal.VerifierEvidence
	Criteria    []MarshalCriterionReport
	Untested    []string
	Risks       []string
	Usage       marshal.Charge
}

// CompletionReport reports only evidence and usage already stored for a run.
func (s *MarshalService) CompletionReport(ctx context.Context, runID string) (MarshalCompletionReport, error) {
	run, _, err := s.load(ctx, runID)
	if err != nil {
		return MarshalCompletionReport{}, err
	}
	report := MarshalCompletionReport{
		ReviewLabel: "Standard: review by the Marshal itself; independent review in ULTRA",
		ChecksLabel: "not tested",
	}
	_, report.Usage, err = s.marshalUsage(ctx, runID, "")
	if err != nil {
		return report, err
	}
	decisions, err := s.Store.MarshalDecisions(ctx, runID)
	if err != nil {
		return report, err
	}
	for _, decision := range decisions {
		if decision.Type != events.EventTypeMarshalVerifierRecorded {
			continue
		}
		if reason, ok := decision.Data["verification_error"].(string); ok && reason != "" {
			report.Risks = append(report.Risks, reason)
		}
		if raw, ok := decision.Data["verifier"]; ok {
			data, err := json.Marshal(raw)
			if err != nil {
				return report, err
			}
			var record marshal.VerifierEvidence
			if err := json.Unmarshal(data, &record); err != nil {
				return report, err
			}
			report.Verifiers = append(report.Verifiers, record)
		}
	}
	hasUsage := false
	for _, decision := range decisions {
		hasUsage = hasUsage || decision.Data["charge_phase"] != nil
	}
	if !hasUsage {
		report.Usage.Tokens.Known = false
		report.Usage.Money.Known = false
	}
	passedCount := 0
	failedCount := 0
	incompleteCount := 0
	untestedCount := 0
	applicableTotal := 0
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

			result := marshal.CriterionResult{Status: "not tested"}
			if found {
				result = marshal.CriterionEvidence(task, handin, criterion)
			}
			report.Criteria = append(report.Criteria, MarshalCriterionReport{TaskID: task.PlanTaskID, Criterion: criterion, Status: result.Status, Mixed: result.Mixed, Incomplete: result.Incomplete})
			if result.Status == "failed" {
				report.Risks = append(report.Risks, fmt.Sprintf("%s: check failed for %s", task.PlanTaskID, criterion))
			}
			if result.Status == "not tested" || result.Incomplete {
				report.Untested = append(report.Untested, fmt.Sprintf("%s: %s", task.PlanTaskID, criterion))
			}
			applicableTotal++
			switch result.Status {
			case "failed":
				failedCount++
			case "incomplete":
				incompleteCount++
			case "verified":
				passedCount++
			default:
				untestedCount++
			}

		}
		if len(task.Criteria) == 0 {
			for _, check := range task.Checks {
				applicableTotal++
				if !found {
					untestedCount++
					continue
				}
				matched := false
				for _, res := range handin.CheckResults {
					if res.Command == check.Command {
						matched = true
						if res.Passed {
							passedCount++
						} else {
							failedCount++
						}
					}
				}
				if !matched {
					untestedCount++
				}
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
	switch {
	case failedCount > 0:
		report.ChecksLabel = "failed"
	case incompleteCount > 0:
		report.ChecksLabel = "incomplete"
	case passedCount > 0 && untestedCount > 0:
		report.ChecksLabel = "incomplete"
	case applicableTotal > 0 && passedCount == applicableTotal:
		report.ChecksLabel = "approved checks passed"
	default:
		report.ChecksLabel = "not tested"
	}
	return report, nil
}
