//go:build governed

package cli

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
)

// privateGoalCommand refuses the goal management subcommands this edition does
// not provide, instead of silently treating "goal create ..." as a free-text
// request that reads "create ...". A request that really starts with one of
// these words can be written as a single quoted argument or via goal explain.
func (c *command) privateGoalCommand(_ context.Context, args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "create", "approve", "edit", "version":
		return true, fmt.Errorf("%w: goal %s is not available in this edition; usage: goal <request> | goal explain <request> (quote the request if it starts with %q)", model.ErrInvalid, args[0], args[0])
	}
	return false, nil
}
