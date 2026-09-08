package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

const optimizationUsage = "Usage: marshal optimization start INPUT.json | show CYCLE-ID | candidates CYCLE-ID | counterfactuals CYCLE-ID | manifests CYCLE-ID | canaries CYCLE-ID"

func (c command) optimization(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: %s", model.ErrInvalid, optimizationUsage)
	}
	runtime, err := app.Open(ctx, c.root)
	if err != nil {
		return err
	}
	defer runtime.Close()
	service := runtime.Optimization()
	switch args[0] {
	case "start":
		if len(args) != 2 {
			return fmt.Errorf("%w: %s", model.ErrInvalid, optimizationUsage)
		}
		body, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		var input app.StartCycleInput
		if err := json.Unmarshal(body, &input); err != nil {
			return fmt.Errorf("decode optimization input: %w", err)
		}
		got, err := service.StartCycle(ctx, input)
		if err != nil {
			return err
		}
		return c.print(got, fmt.Sprintf("optimization=%s version=%d candidates=%d vetoes=%d", got.ID, got.Version, len(got.Candidates), len(got.Vetoes)))
	case "show", "candidates", "counterfactuals", "manifests", "canaries":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			return fmt.Errorf("%w: cycle ID required", model.ErrInvalid)
		}
		var got any
		switch args[0] {
		case "show":
			got, err = service.Get(ctx, args[1])
		case "candidates":
			got, err = service.Candidates(ctx, args[1])
		case "counterfactuals":
			got, err = service.Counterfactuals(ctx, args[1])
		case "manifests":
			got, err = service.Manifests(ctx, args[1])
		case "canaries":
			got, err = service.Canaries(ctx, args[1])
		}
		if err != nil {
			return err
		}
		return c.print(got, fmt.Sprintf("optimization %s: %s", args[0], args[1]))
	default:
		return fmt.Errorf("%w: %s", model.ErrInvalid, optimizationUsage)
	}
}
