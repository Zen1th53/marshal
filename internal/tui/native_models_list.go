package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
)

const (
	nativeModelListTimeout = 20 * time.Second
	nativeModelListLines   = 200
)

// listNativeModels runs `<provider> models` without a terminal and returns the
// list. These CLIs print the models and exit, so opening a window for them
// flashed shut with nothing shown.
func listNativeModels(ctx context.Context, provider, title string) string {
	d := app.ObserveProviderDialect(ctx, provider)
	if d.Binary == "" {
		return fmt.Sprintf("%s MODELS: %s is not installed, so there are no models to list. Install it, then run /models %s again.", title, provider, provider)
	}
	ctx, cancel := context.WithTimeout(ctx, nativeModelListTimeout)
	defer cancel()
	proc := exec.CommandContext(ctx, d.Binary, "models")
	proc.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "CI=1")
	var stdout, stderr bytes.Buffer
	proc.Stdout, proc.Stderr = &stdout, &stderr
	err := proc.Run()
	out := strings.TrimSpace(stdout.String())
	if ctx.Err() != nil {
		return fmt.Sprintf("%s MODELS: %s did not answer within %s. Run /%s in its own session to browse models.", title, provider, nativeModelListTimeout, provider)
	}
	if err != nil && out == "" {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		if len(reason) > 300 {
			reason = reason[:300] + "…"
		}
		return fmt.Sprintf("%s MODELS: could not list models: %s", title, reason)
	}
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" || strings.HasPrefix(line, "Fetching available models") {
			continue
		}
		lines = append(lines, "  "+line)
	}
	if len(lines) == 0 {
		return fmt.Sprintf("%s MODELS: %s reported no models. Sign in with /login %s and try again.", title, provider, provider)
	}
	total := len(lines)
	more := ""
	if total > nativeModelListLines {
		more = fmt.Sprintf("\n  … and %d more (run %s models in a terminal for the full list)", total-nativeModelListLines, provider)
		lines = lines[:nativeModelListLines]
	}
	return fmt.Sprintf("%s MODELS (%d):\n%s%s", title, total, strings.Join(lines, "\n"), more)
}
