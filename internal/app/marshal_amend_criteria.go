package app

import (
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// bindAmendCriteria keeps an amendment from failing on criterion wording the
// operator never asked to change. A routing amendment ("move this review to
// codex") returns the whole task list again, and the model restates criteria
// it should have copied: a check's criterion with different spacing, case or
// final punctuation, a paraphrase on an existing check, or a check of a new
// single-criterion task that names it in other words. The runtime binds each
// to the criterion text it already has:
//
//   - a task criterion that matches an approved criterion of the same task
//     up to spacing, case and final punctuation takes the approved text;
//   - a check that keeps an approved command and names a criterion the task
//     does not state takes the approved mapping, when every approved
//     criterion is still a task criterion;
//   - a check criterion that matches a task criterion up to spacing, case and
//     final punctuation takes that criterion's text;
//   - a remaining check criterion of a task with exactly one criterion is
//     that criterion, the only one the check can evidence.
//
// Anything else is left as proposed and validation still refuses it, so a
// check can never be mapped to a criterion the task does not state.
func bindAmendCriteria(proposal *marshalTaskProposal, existing []marshal.Task) {
	approved := map[string]marshal.Task{}
	for _, task := range existing {
		approved[task.PlanTaskID] = task
	}
	for i := range proposal.Tasks {
		item := &proposal.Tasks[i]
		old, known := approved[item.ID]
		if known {
			for j, criterion := range item.Criteria {
				if canonical, ok := matchCriterion(old.Criteria, criterion); ok {
					item.Criteria[j] = canonical
				}
			}
		}
		for j := range item.Checks {
			check := &item.Checks[j]
			if allCriteriaStated(item.Criteria, check.Criteria) {
				continue
			}
			if known {
				for _, previous := range old.Checks {
					if previous.Command == check.Command && len(previous.Criteria) > 0 && allCriteriaStated(item.Criteria, previous.Criteria) {
						check.Criteria = append([]string(nil), previous.Criteria...)
						break
					}
				}
			}
			bound := make([]string, 0, len(check.Criteria))
			for _, criterion := range check.Criteria {
				if canonical, ok := matchCriterion(item.Criteria, criterion); ok {
					criterion = canonical
				} else if len(item.Criteria) == 1 && strings.TrimSpace(item.Criteria[0]) != "" {
					criterion = item.Criteria[0]
				}
				if !containsMarshal(bound, criterion) {
					bound = append(bound, criterion)
				}
			}
			check.Criteria = bound
		}
	}
}

func allCriteriaStated(stated, named []string) bool {
	for _, criterion := range named {
		if strings.TrimSpace(criterion) == "" || !containsMarshal(stated, criterion) {
			return false
		}
	}
	return true
}

// matchCriterion finds the criterion in stated that equals text exactly, or
// failing that up to spacing, case and final punctuation.
func matchCriterion(stated []string, text string) (string, bool) {
	if containsMarshal(stated, text) {
		return text, true
	}
	key := criterionKey(text)
	if key == "" {
		return "", false
	}
	for _, candidate := range stated {
		if criterionKey(candidate) == key {
			return candidate, true
		}
	}
	return "", false
}

func criterionKey(text string) string {
	text = strings.ToLower(strings.Join(strings.Fields(text), " "))
	return strings.TrimSpace(strings.TrimRight(text, ".;:!"))
}
