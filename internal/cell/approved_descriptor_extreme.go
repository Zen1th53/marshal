//go:build !governed

package cell

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/processgroup"
)

// ApprovedCheckDescriptor identifies one future, one-shot confined check. It
// is retained data, not an approval or a grant. Each operation must reread its
// immutable plan, current goal/task and scoped caller capability.
type ApprovedCheckDescriptor struct {
	ID                  CellID                 `json:"id"`
	ProjectID           string                 `json:"project_id"`
	SessionID           string                 `json:"session_id"`
	TaskID              string                 `json:"task_id"`
	TaskRevision        int64                  `json:"task_revision"`
	GoalID              string                 `json:"goal_id"`
	GoalRevision        int64                  `json:"goal_revision"`
	PlanID              string                 `json:"plan_id"`
	PlanVersion         int64                  `json:"plan_version"`
	PlanTaskID          string                 `json:"plan_task_id"`
	CheckIndex          int                    `json:"check_index"`
	CheckDigest         string                 `json:"check_digest"`
	ApprovalScopeDigest string                 `json:"approval_scope_digest"`
	Spec                Spec                   `json:"spec"`
	Revision            int64                  `json:"revision"`
	State               State                  `json:"state"`
	Process             processgroup.Reference `json:"process"`
	Settled             bool                   `json:"settled"`
	ExitCode            *int                   `json:"exit_code,omitempty"`
}

func (d ApprovedCheckDescriptor) Validate() error {
	for _, value := range []string{string(d.ID), d.ProjectID, d.SessionID, d.TaskID, d.GoalID, d.PlanID, d.PlanTaskID, d.ApprovalScopeDigest} {
		if !validID(value) || len(value) > 256 {
			return fmt.Errorf("invalid private check descriptor identity")
		}
	}
	if d.TaskRevision < 1 || d.GoalRevision < 1 || d.PlanVersion < 1 || d.Revision < 1 || d.CheckIndex < 0 || d.CheckIndex >= 128 ||
		len(d.CheckDigest) != 64 || strings.Trim(d.CheckDigest, "0123456789abcdef") != "" {
		return fmt.Errorf("invalid private check descriptor binding")
	}
	if err := d.Spec.Validate(); err != nil {
		return err
	}
	if d.Spec.TaskID != TaskID(d.TaskID) || d.Spec.Backend != BackendBubblewrap || d.Spec.CPUQuota != 0 || d.Spec.MemoryBytes != 0 || len(d.Spec.SecretRefs) != 0 || d.Spec.NetworkProfile != "deny-by-default" {
		return fmt.Errorf("unsupported private check descriptor resources")
	}
	if d.ID != cellIDFor(d.Spec) {
		return fmt.Errorf("private check descriptor differs from its cell identity")
	}
	if d.State != StateReady && d.State != StateRunning && d.State != StateStopping && d.State != StateDestroyed && d.State != StateFailed {
		return fmt.Errorf("invalid private check descriptor state")
	}
	if d.Process.PID < 0 || (d.Process.PID == 0) != (d.Process.Start == "") || (d.State == StateDestroyed && !d.Settled) || (d.Settled && d.State != StateDestroyed) || (d.ExitCode != nil && !d.Settled) {
		return fmt.Errorf("invalid private check descriptor settlement")
	}
	if d.Process.Start != "" {
		if start, err := strconv.ParseUint(d.Process.Start, 10, 64); err != nil || start == 0 {
			return fmt.Errorf("invalid private check supervisor identity")
		}
	}
	return nil
}
