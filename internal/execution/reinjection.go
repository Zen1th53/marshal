package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

// ConstraintPackage represents canonical constraints that must be injected into every worker context.
type ConstraintPackage struct {
	GoalOutcome             string   `json:"goal_outcome"`
	TaskID                  string   `json:"task_id"`
	TaskDescription         string   `json:"task_description"`
	HardConstraints         []string `json:"hard_constraints"`
	DoNotDo                 []string `json:"do_not_do"`
	CapabilityLimits        []string `json:"capability_limits,omitempty"`
	ApprovalRequired        bool     `json:"approval_required"`
	ExpectedOutputs         []string `json:"expected_outputs,omitempty"`
	VerificationObligations []string `json:"verification_obligations,omitempty"`
	Digest                  string   `json:"digest"`
}

// BuildConstraintPackage builds and digests a canonical constraint package for a task.
func BuildConstraintPackage(goal model.GoalContract, task TaskExecution, p plan.ExecutionPlan) (ConstraintPackage, error) {
	if goal.DesiredOutcome == "" {
		return ConstraintPackage{}, fmt.Errorf("%w: goal has no desired outcome", ErrConstraintViolation)
	}
	if task.TaskID == "" {
		return ConstraintPackage{}, fmt.Errorf("%w: task has no task ID", ErrRunInvalid)
	}

	var hard []string
	for _, c := range goal.Constraints {
		if c.IsHard {
			hard = append(hard, c.Text)
		}
	}
	sort.Strings(hard)

	dnd := append([]string(nil), goal.DoNotDo...)
	sort.Strings(dnd)

	var verifs []string
	for _, v := range p.Verification.Obligations {
		for _, tid := range v.Tasks {
			if tid == task.TaskID {
				verifs = append(verifs, fmt.Sprintf("%s (%s)", v.Criterion, v.Method))
				break
			}
		}
	}
	sort.Strings(verifs)

	pkg := ConstraintPackage{
		GoalOutcome:             goal.DesiredOutcome,
		TaskID:                  task.TaskID,
		TaskDescription:         task.Description,
		HardConstraints:         hard,
		DoNotDo:                 dnd,
		ApprovalRequired:        task.ApprovalRequired,
		ExpectedOutputs:         append([]string(nil), task.OutputArtifacts...),
		VerificationObligations: verifs,
	}

	pkg.Digest = ComputeConstraintDigest(pkg)
	return pkg, nil
}

// ComputeConstraintDigest calculates a SHA256 digest over the critical constraint fields.
func ComputeConstraintDigest(pkg ConstraintPackage) string {
	h := sha256.New()
	fmt.Fprintf(h, "goal:%s\n", pkg.GoalOutcome)
	fmt.Fprintf(h, "task:%s:%s\n", pkg.TaskID, pkg.TaskDescription)
	for _, c := range pkg.HardConstraints {
		fmt.Fprintf(h, "constraint:%s\n", c)
	}
	for _, d := range pkg.DoNotDo {
		fmt.Fprintf(h, "dnd:%s\n", d)
	}
	fmt.Fprintf(h, "approval:%t\n", pkg.ApprovalRequired)
	for _, v := range pkg.VerificationObligations {
		fmt.Fprintf(h, "verif:%s\n", v)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyConstraintPackage verifies the package integrity and ensures no constraints were omitted.
func VerifyConstraintPackage(pkg ConstraintPackage) error {
	if pkg.GoalOutcome == "" {
		return fmt.Errorf("%w: missing goal outcome in constraint package", ErrConstraintViolation)
	}
	if pkg.TaskID == "" {
		return fmt.Errorf("%w: missing task ID in constraint package", ErrConstraintViolation)
	}
	computed := ComputeConstraintDigest(pkg)
	if pkg.Digest != "" && computed != pkg.Digest {
		return fmt.Errorf("%w: constraint package digest mismatch: got %s, want %s",
			ErrConstraintViolation, computed, pkg.Digest)
	}
	return nil
}

// FormatPromptHeader creates an immutable constraint instructions banner for provider workers.
func (p ConstraintPackage) FormatPromptHeader() string {
	var b strings.Builder
	b.WriteString("=== MARSHAL GOVERNED EXECUTION CONSTRAINTS ===\n")
	b.WriteString(fmt.Sprintf("GOAL: %s\n", p.GoalOutcome))
	b.WriteString(fmt.Sprintf("ASSIGNED TASK: %s - %s\n", p.TaskID, p.TaskDescription))

	if len(p.HardConstraints) > 0 {
		b.WriteString("HARD CONSTRAINTS (MANDATORY - DO NOT BYPASS):\n")
		for _, c := range p.HardConstraints {
			b.WriteString(fmt.Sprintf("  * %s\n", c))
		}
	}

	if len(p.DoNotDo) > 0 {
		b.WriteString("PROHIBITED ACTIONS (DO NOT DO):\n")
		for _, d := range p.DoNotDo {
			b.WriteString(fmt.Sprintf("  ! %s\n", d))
		}
	}

	if p.ApprovalRequired {
		b.WriteString("WARNING: Hard human approval required before mutating changes.\n")
	}

	if len(p.VerificationObligations) > 0 {
		b.WriteString("VERIFICATION OBLIGATIONS (EVIDENCE REQUIRED):\n")
		for _, v := range p.VerificationObligations {
			b.WriteString(fmt.Sprintf("  - %s\n", v))
		}
	}

	b.WriteString(fmt.Sprintf("SECURITY DIGEST: %s\n", p.Digest))
	b.WriteString("==============================================\n\n")
	return b.String()
}
