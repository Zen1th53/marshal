package opencode

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

// ParseModels reads `opencode models` output: one provider/model identifier
// per line. Lines that are not a single identifier (banners, notices) are
// skipped, and each identifier is listed once, in the CLI's order.
func ParseModels(out string) []string {
	var models []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.ContainsAny(line, " \t") || !strings.Contains(line, "/") || seen[line] {
			continue
		}
		seen[line] = true
		models = append(models, line)
	}
	return models
}

// DiscoverModels asks the installed OpenCode which models it offers now.
// OpenCode reports no current model, so none is marked as the default.
func DiscoverModels(ctx context.Context, binary string, runner adapter.ProcessRunner) ([]string, error) {
	if binary == "" || runner == nil {
		return nil, fmt.Errorf("%w: opencode binary and runner required", model.ErrInvalid)
	}
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := runner.Run(listCtx, adapter.Command{
		Path: binary, Args: []string{"models"},
		Env: append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "CI=1"),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: opencode models: %v", model.ErrUnavailable, err)
	}
	if result.ExitCode != 0 || result.TimedOut || result.Cancelled {
		return nil, fmt.Errorf("%w: opencode models did not finish (exit %d)", model.ErrUnavailable, result.ExitCode)
	}
	models := ParseModels(string(result.Stdout))
	if len(models) == 0 {
		return nil, fmt.Errorf("%w: opencode listed no models; configure a provider in opencode and try again", model.ErrUnavailable)
	}
	return models, nil
}
