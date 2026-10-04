// Package hostgit confines the runtime's host Git invocations to built-in operations.
package hostgit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Command disables repository-selected programs, preserving the configured identity.
func Command(ctx context.Context, dir string, args ...string) (*exec.Cmd, error) {
	binary := "/usr/bin/git"
	if _, err := os.Stat(binary); err != nil {
		binary = "/bin/git"
	}
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "GIT_CONFIG_GLOBAL" || key == "GIT_CONFIG_SYSTEM" || !strings.HasPrefix(key, "GIT_") || strings.HasPrefix(key, "GIT_AUTHOR_") || strings.HasPrefix(key, "GIT_COMMITTER_") {
			env = append(env, entry)
		}
	}
	env = append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_ATTR_NOSYSTEM=1", "GIT_PAGER=cat", "GIT_ALLOW_PROTOCOL=", "GIT_NO_LAZY_FETCH=1")
	flags := []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.alternateRefsCommand=", "-c", "gc.auto=0", "-c", "maintenance.auto=false", "-c", "core.pager=cat", "-c", "diff.external=", "-c", "log.diffMerges=separate", "-c", "commit.gpgSign=false", "-c", "tag.gpgSign=false", "-c", "core.attributesFile=/dev/null", "-c", "submodule.recurse=false", "-C", dir}
	if len(args) > 0 && args[0] == "merge" {
		for _, arg := range args[1:] {
			if arg == "--" {
				break
			}
			if arg == "--verify-signatures" || strings.HasPrefix(arg, "--verify-signatures=") {
				return nil, fmt.Errorf("host merge cannot enforce required signature verification without external programs")
			}
		}
	}
	if len(args) > 0 && args[0] == "merge" && (len(args) == 1 || args[1] != "--abort") {
		policy := exec.CommandContext(ctx, binary, append(append([]string(nil), flags...), "config", "--bool", "--get-all", "merge.verifySignatures")...)
		policy.Env = env
		output, err := policy.Output()
		var exit *exec.ExitError
		if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
			return nil, fmt.Errorf("inspect Git signature policy: %w", err)
		}
		for _, value := range strings.Fields(string(output)) {
			if value == "true" {
				return nil, fmt.Errorf("host merge cannot enforce required signature verification without external programs")
			}
		}
	}
	// Also prevent helpers if configuration changes after inspection. Git still
	// enforces its signature policy, but cannot launch a repository-selected program.
	for _, key := range []string{"gpg.program", "gpg.openpgp.program", "gpg.x509.program", "gpg.ssh.program"} {
		flags = append(flags, "-c", key+"=/usr/bin/false")
	}
	config := exec.CommandContext(ctx, binary, append(append([]string(nil), flags...), "config", "--null", "--name-only", "--get-regexp", "^(filter|diff|merge)\\.")...)
	config.Env = env
	output, err := config.Output()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return nil, fmt.Errorf("inspect Git helper configuration: %w", err)
	}
	for _, key := range strings.Split(string(output), "\x00") {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "filter.") {
			switch {
			case strings.HasSuffix(lower, ".clean"), strings.HasSuffix(lower, ".smudge"), strings.HasSuffix(lower, ".process"):
				flags = append(flags, "-c", key+"=")
			case strings.HasSuffix(lower, ".required"):
				flags = append(flags, "-c", key+"=false")
			}
		}
		if strings.HasPrefix(lower, "merge.") && strings.HasSuffix(lower, ".driver") && len(args) > 0 && args[0] == "merge" && (len(args) == 1 || args[1] != "--abort") {
			return nil, fmt.Errorf("host merge requires built-in merge drivers")
		}
	}
	if len(args) > 0 && (args[0] == "diff" || args[0] == "show" || args[0] == "log") {
		safe := []string{"--no-ext-diff", "--no-textconv"}
		if args[0] != "diff" {
			safe = append(safe, "--no-show-signature")
		}
		end := len(args)
		for i, arg := range args {
			if arg == "--" {
				end = i
				break
			}
			if arg == "--remerge-diff" || arg == "--diff-merges=remerge" || arg == "--diff-merges=r" || (arg == "--diff-merges" && i+1 < len(args) && (args[i+1] == "remerge" || args[i+1] == "r")) {
				return nil, fmt.Errorf("host inspection requires built-in merge drivers")
			}
		}
		args = append(append(append([]string(nil), args[:end]...), safe...), args[end:]...)
	}
	cmd := exec.CommandContext(ctx, binary, append(flags, args...)...)
	cmd.Env = env
	return cmd, nil
}
