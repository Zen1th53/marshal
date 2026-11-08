//go:build !governed

package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

// projectContractSubmissionShape is the exact proposal the runtime admits
// through marshal_project_contract_submit, stated for the Marshal.
const projectContractSubmissionShape = `{"action":"project-contract","expected_version":<current_version from marshal_project_contract_inventory>,"contract":{"format_version":1,"project":<project from the inventory>,"id":"<contract id>","revision":<expected_version+1>,"purpose":"...","components":[{"id":"...","responsibility":"...","paths":["<repo path>"],"dependencies":["<component id>"]}],"invariants":["..."],"required":[<one ref per inventory entry that must stay mandatory, else []>],"reviewed_inventory_digest":"<completeness_digest from the last inventory page>"}}`

// explainProjectContractSubmission names what makes a contract draft
// inadmissible. A bare "required context invalid" left the Marshal guessing
// the shape; nothing here relaxes a check, it only reports which one failed.
func explainProjectContractSubmission(sub store.ProjectContractSubmission, err error) error {
	if !errors.Is(err, projectcontext.ErrInvalid) {
		return err
	}
	var problems []string
	c := sub.Contract
	if sub.Action != "project-contract" {
		problems = append(problems, `action must be "project-contract"`)
	}
	if sub.ExpectedVersion < 0 {
		problems = append(problems, "expected_version must be the inventory's current_version")
	}
	if c.FormatVersion != projectcontext.FormatVersion {
		problems = append(problems, fmt.Sprintf("contract.format_version must be %d", projectcontext.FormatVersion))
	}
	if strings.TrimSpace(c.ID) == "" {
		problems = append(problems, "contract.id is required")
	}
	if c.Revision != sub.ExpectedVersion+1 {
		problems = append(problems, "contract.revision must be expected_version+1")
	}
	if strings.TrimSpace(c.Purpose) == "" {
		problems = append(problems, "contract.purpose is required")
	}
	if len(c.Components) == 0 {
		problems = append(problems, "contract.components needs at least one component")
	}
	for _, component := range c.Components {
		if strings.TrimSpace(component.ID) == "" || strings.TrimSpace(component.Responsibility) == "" || len(component.Paths) == 0 {
			problems = append(problems, "every component needs id, responsibility and at least one repository path in paths")
			break
		}
	}
	if len(c.Invariants) == 0 {
		problems = append(problems, "contract.invariants needs at least one invariant")
	}
	if c.ReviewedInventoryDigest == "" {
		problems = append(problems, "contract.reviewed_inventory_digest must be the inventory's completeness_digest")
	}
	if len(problems) == 0 {
		problems = append(problems, "a component path, dependency or required reference is malformed")
	}
	return fmt.Errorf("%w: %s; expected %s", err, strings.Join(problems, "; "), projectContractSubmissionShape)
}
