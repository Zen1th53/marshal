//go:build !governed && linux

package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/worker"
)

// Metadata can reject an unsupported installation before account consent. It
// cannot establish a harness profile or carry authority into the real probe.
// Probe the same host CLI that Extreme workers execute, without requiring bwrap.
func privateHarnessProbePreflight(ctx context.Context, provider string) error {
	if provider != "agy" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	binary, err := project.FindBinary("agy")
	if err != nil {
		return fmt.Errorf("%w: agy CLI is missing", model.ErrUnavailable)
	}
	// No caller values (worker MCP, credential lease or execution authority)
	// enter metadata discovery; its deadline and cancellation remain bounded.
	deadline := time.Now().Add(10 * time.Second)
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	metadataCtx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	runner := worker.New(5*time.Second, 200*time.Millisecond, 64<<10)
	_, err = antigravity.NewGovernedProbe(binary, runner).Probe(metadataCtx)
	return err
}
