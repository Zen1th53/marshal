//go:build unix && governed

package sessionmcp

import (
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
)

// Governed Antigravity configuration files exist only in the private build.
const governedAntigravityRequiresHost = false

func configureAntigravityFiles(*bridge, string, []string, bool) error {
	return fmt.Errorf("%w: governed Antigravity is unavailable", model.ErrUnavailable)
}

func antigravitySandboxConfiguration(_ *bridge, request model.SandboxRequest) (model.SandboxRequest, error) {
	return request, fmt.Errorf("%w: governed Antigravity is unavailable", model.ErrUnavailable)
}
