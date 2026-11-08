//go:build !governed

package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// This explicit offline operator command is not a model MCP tool. The dry-run
// precedes opening the target runtime so it cannot initialize or adopt state.
func (c command) memoryLegacyMigration(ctx context.Context, args []string) (bool, error) {
	if len(args) == 0 || (args[0] != "migrate-legacy" && args[0] != "rollback-migration") {
		return false, nil
	}
	flags := flag.NewFlagSet("memory "+args[0], flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	source := flags.String("source-kind", "", "explicit private legacy source declaration")
	dry := flags.Bool("dry-run", false, "read-only preservation plan")
	digest := flags.String("digest", "", "exact dry-run migration digest")
	initialize := flags.Bool("initialize-target", false, "create only an absent Marshal namespace with the checked original project identity")
	if err := flags.Parse(args[1:]); err != nil {
		return true, err
	}
	if flags.NArg() != 0 {
		return true, model.ErrInvalid
	}
	var plan store.LegacyMemoryPlan
	var err error
	switch {
	case args[0] == "rollback-migration":
		if *dry || *source != "" || *initialize {
			return true, fmt.Errorf("%w: rollback requires only the exact migrated --digest", model.ErrInvalid)
		}
		plan, err = store.RollbackLegacyMemoryMigration(ctx, c.root, *digest)
	case *dry:
		if *digest != "" || *initialize {
			return true, model.ErrInvalid
		}
		plan, err = store.PlanLegacyMemoryMigration(ctx, c.root, *source)
	case *initialize:
		plan, err = store.InitializeAndApplyLegacyMemoryMigration(ctx, c.root, *source, *digest)
	default:
		plan, err = store.ApplyLegacyMemoryMigration(ctx, c.root, *source, *digest)
	}
	if err != nil {
		return true, err
	}
	return true, c.print(plan, fmt.Sprintf("%s records=%d digest=%s; original legacy data retained; no live authority transferred", plan.Status, plan.Records, plan.Digest))
}
