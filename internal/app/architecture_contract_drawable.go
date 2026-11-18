package app

import (
	"fmt"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// ValidateContractDrawable refuses a project contract the owner could not
// open: it builds the same drawing the browser review builds. A contract with
// component IDs like "internal/architecturecodec" used to be accepted on
// submit and only fail when the owner pressed y.
func ValidateContractDrawable(c projectcontext.Contract) error {
	if _, err := contractReviewSeedFor(c, "sha256:submit-check", "en"); err != nil {
		return fmt.Errorf("%w. Component IDs must be short identifiers (letters, digits, '.', '_' or '-', no '/'): put package paths in paths, not in id", err)
	}
	return nil
}
