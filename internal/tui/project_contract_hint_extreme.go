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
	ids := map[string]int{}
	for _, component := range c.Components {
		ids[component.ID]++
	}
	for _, component := range c.Components {
		if ids[component.ID] > 1 {
			problems = append(problems, fmt.Sprintf("component id %q is used more than once", component.ID))
			ids[component.ID] = 0
		}
		for _, p := range component.Paths {
			if !projectcontext.ValidPath(p) {
				problems = append(problems, fmt.Sprintf("component %q path %q must be a clean repository path such as \"src/app\": no leading \"/\" or \"./\", no trailing \"/\", no \"..\"", component.ID, p))
			}
		}
		for _, dep := range component.Dependencies {
			if dep == component.ID {
				problems = append(problems, fmt.Sprintf("component %q cannot depend on itself", component.ID))
			} else if _, ok := ids[dep]; !ok {
				problems = append(problems, fmt.Sprintf("component %q depends on %q, which is not a component id in this contract", component.ID, dep))
			}
		}
	}
	if len(c.Invariants) == 0 {
		problems = append(problems, "contract.invariants needs at least one invariant")
	}
	if c.ReviewedInventoryDigest == "" {
		problems = append(problems, "contract.reviewed_inventory_digest must be the inventory's completeness_digest")
	}
	if len(problems) == 0 && len(c.Required) > 0 {
		problems = append(problems, "a required entry is malformed: copy scope, scope_id, id, project_id, revision, content_digest and state_fingerprint from its inventory entry and give class, reason, acceptance_occurrence and applies; when the inventory has no entries, required is []")
	}
	if len(problems) == 0 {
		problems = append(problems, "a component path, dependency or required reference is malformed")
	}
	return fmt.Errorf("%w: %s; expected %s", err, strings.Join(problems, "; "), projectContractSubmissionShape)
}
