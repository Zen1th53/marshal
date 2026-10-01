package verification

import "time"

type CriterionProgress struct {
	Criterion   string
	Status      Status
	Detail      string
	EvidenceIDs []string
}

// ProjectCriteria keeps observations distinct from completion. Criterion IDs
// are exact goal criterion strings, as established by Plan; no positional join.
func ProjectCriteria(criteria []string, s Session, current Binding, now time.Time) []CriterionProgress {
	out := make([]CriterionProgress, 0, len(criteria))
	for _, name := range criteria {
		row := CriterionProgress{Criterion: name, Status: StatusNotRun, Detail: "missing evidence"}
		for _, criterion := range s.Criteria {
			if criterion.ID != name {
				continue
			}
			subset := s
			subset.Criteria = []Criterion{criterion}
			subset.Claims = nil
			subset.Evidence = nil
			for _, claim := range s.Claims {
				if claim.CriterionID == name {
					subset.Claims = append(subset.Claims, claim)
					for _, ev := range s.Evidence {
						if ev.ClaimID == claim.ID {
							subset.Evidence = append(subset.Evidence, ev)
						}
					}
				}
			}
			for _, claim := range s.Claims {
				for _, id := range criterion.ClaimIDs {
					if claim.ID != id {
						continue
					}
					for _, evidenceID := range claim.EvidenceIDs {
						row.EvidenceIDs = append(row.EvidenceIDs, evidenceID)
						found := false
						for _, ev := range s.Evidence {
							if ev.ID != evidenceID {
								continue
							}
							found = true
							if ev.Status != StatusPass {
								row.Status = ev.Status
								row.Detail = "canonical evidence status"
							}
							if ev.ContentDigest == "" {
								row.Status = StatusUnknown
								row.Detail = "missing evidence digest"
							}
							if ev.TreeDigest != s.Binding.TreeDigest || ev.EnvironmentDigest != s.Binding.EnvironmentDigest || (ev.ExpiresAt != nil && !now.Before(*ev.ExpiresAt)) {
								row.Status = StatusUnknown
								row.Detail = "stale evidence"
							}
						}
						if !found {
							row.Status = StatusUnknown
							row.Detail = "missing evidence reference"
						}
					}
				}
			}
			if s.Binding != current {
				row.Status = StatusUnknown
				row.Detail = "stale or unavailable binding"
			}
			if s.Binding.GoalRevision != current.GoalRevision {
				row.Detail = "stale goal revision"
			}
			if Evaluate(subset, current, now) == VerifiedComplete {
				row.Status = StatusPass
				row.Detail = "verified criterion"
			} else if row.Detail == "missing evidence" && len(row.EvidenceIDs) > 0 {
				// PASS observations alone are insufficient: independence, attempts,
				// contradictions and required checks still belong to Evaluate.
				row.Status = StatusUnknown
				row.Detail = "verification requirements not satisfied"
			}
			break
		}
		out = append(out, row)
	}
	return out
}
