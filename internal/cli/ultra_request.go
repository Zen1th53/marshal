//go:build governed

package cli

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/cloud"
)

func (c *command) ultra(ctx context.Context, args []string) error {
	if len(args) != 1 || args[0] != "request" {
		return fmt.Errorf("usage: marshal ultra request")
	}
	cfg := cloud.LoadConfig()
	if !cfg.Enabled() {
		return fmt.Errorf("Community Cloud is disabled; ULTRA requires a connection")
	}
	dir, err := c.machineCloudDir()
	if err != nil {
		return err
	}
	client, st, session, err := cloud.PrepareEntitlementRequest(ctx, cfg, dir, cloudClientVersion(Version))
	if err != nil {
		return err
	}
	observation, callErr := client.RequestEntitlementTracked(ctx, st, session, cloud.NewRequestStore(dir), 0)
	if observation.LocalAttemptID == "" {
		return callErr
	}
	detail := fmt.Sprintf("ULTRA request: %s (local attempt %s).", observation.State, observation.LocalAttemptID)
	if observation.State == "active" {
		detail += " Access granted; start MARSHAL again to obtain a connected signed lease."
	}
	if observation.State == "unknown" || observation.State == "sending" {
		detail += " Server outcome is unknown; the attempt was recorded."
	}
	if err := c.print(observation, detail); err != nil {
		return err
	}
	return callErr
}
