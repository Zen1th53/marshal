//go:build !governed

package plan

import (
	"encoding/json"
	"fmt"
)

func sealPrivateProducerAssignments(p ExecutionPlan, s *ApprovalScope) error {
	if p.Version < 1 {
		return nil
	}
	snapshot := ProducerApprovalSeal{ReviewedPlanVersion: p.Version}
	for _, a := range p.Assignments.Assignments {
		if a.Role == RoleDeveloper && !a.Independent {
			snapshot.Assignments = append(snapshot.Assignments, a)
		}
	}
	if len(snapshot.Assignments) == 0 {
		return nil
	}
	raw, err := json.Marshal(snapshot)
	if err != nil || len(raw) > 64<<10 {
		return fmt.Errorf("%w: producer approval snapshot unavailable", ErrPlanInvalid)
	}
	var detached ProducerApprovalSeal
	if err = json.Unmarshal(raw, &detached); err != nil {
		return fmt.Errorf("%w: producer approval snapshot unavailable", ErrPlanInvalid)
	}
	s.ProducerAssignments = &detached
	return nil
}
func pinPrivateFallbackProfile(c HarnessCandidate, f *Fallback) {
	f.HarnessVersion = c.InstalledVersion
	f.ProbeEvidenceID = c.Profile.ProbeEvidenceID
	if !c.Profile.ProbedAt.IsZero() {
		when := c.Profile.ProbedAt
		f.ProbedAt = &when
	}
	if !c.Profile.ExpiresAt.IsZero() {
		when := c.Profile.ExpiresAt
		f.ExpiresAt = &when
	}
}
