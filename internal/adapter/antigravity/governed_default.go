//go:build governed || !unix

package antigravity

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

// NewGoverned creates an Antigravity client with governed mode requested.
func NewGoverned(binary string, runner adapter.ProcessRunner) *Client {
	c := New(binary, runner)
	c.governed = true
	return c
}

// NewGovernedProbe proves headless transport without a worker principal or tools.
func NewGovernedProbe(binary string, runner adapter.ProcessRunner) *Client {
	c := NewGoverned(binary, runner)
	c.probeOnly = true
	return c
}

func (c *Client) prepareInvocation(ctx context.Context, _ adapter.Request, args []string, _ string) (context.Context, []string, []string, func(), error) {
	if c.governed {
		return ctx, nil, nil, func() {}, fmt.Errorf("%w: governed Antigravity is unavailable", model.ErrUnavailable)
	}
	return ctx, args, nil, func() {}, nil
}

func (c *Client) validateGovernedVersion(version string) error {
	if !c.governed {
		return nil
	}
	if !isQualifiedGovernedVersion(version) {
		observed := "UNKNOWN"
		if safeObservedVersion.MatchString(version) {
			observed = version
		}
		return fmt.Errorf("%w: governed Antigravity CLI version mismatch (observed=%s; qualified=%s); this installation requires separate OAuth/configuration and transport qualification before governed use", model.ErrUnavailable, observed, GovernedCLIVersionRange)
	}
	return nil
}
