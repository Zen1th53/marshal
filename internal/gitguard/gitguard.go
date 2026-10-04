// Package gitguard disables Git facilities that can execute worker-selected
// commands during automatic bookkeeping on a worker-produced tree.
package gitguard

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func Options(ctx context.Context, dir string) ([]string, error) {
	options := []string{"--no-lazy-fetch", "-c", "protocol.allow=never", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "diff.external=", "-c", "core.pager=cat", "-c", "submodule.recurse=false", "-c", "diff.ignoreSubmodules=all"}
	// Read keys only: configuration values may contain credentials. Config
	// inspection executes no hooks, filters or worker code.
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "config", "--null", "--name-only", "--get-regexp", `^(filter|diff|merge)\.`)
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("inspect Git execution configuration: %w", err)
		}
	}
	for _, key := range strings.Split(string(out), "\x00") {
		lower := strings.ToLower(key)
		switch {
		case strings.HasPrefix(lower, "filter.") && (strings.HasSuffix(lower, ".clean") || strings.HasSuffix(lower, ".smudge") || strings.HasSuffix(lower, ".process")),
			strings.HasPrefix(lower, "diff.") && (strings.HasSuffix(lower, ".command") || strings.HasSuffix(lower, ".textconv")),
			strings.HasPrefix(lower, "merge.") && strings.HasSuffix(lower, ".driver"):
			options = append(options, "-c", key+"=")
		case strings.HasPrefix(lower, "filter.") && strings.HasSuffix(lower, ".required"):
			options = append(options, "-c", key+"=false")
		}
	}
	return options, nil
}
