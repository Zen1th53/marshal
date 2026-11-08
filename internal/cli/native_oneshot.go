package cli

import (
	"context"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/nativehistory"
	"strings"

	"github.com/Zen1th53/marshal/internal/project"
)

// oneShotCaptureTimeout bounds the post-command history scan. The capture
// observes its context, so a large granted history stops at the next record
// once this deadline passes instead of reading to the end after the command
// has already returned.
const oneShotCaptureTimeout = 5 * time.Second

// oneShotCaptureDrain is how long the command waits, after the scan deadline,
// for the capture goroutine to actually observe cancellation and stop before
// the runtime is closed. It is a bounded grace, not a second scan budget.
const oneShotCaptureDrain = time.Second

// nativeProvider dispatches a native provider command (`marshal codex`,
// `marshal claude`, `marshal opencode`, `marshal agy`/`antigravity`). When the
// arguments request the provider's own one-shot mode, the provider binary runs
// directly as a child process in the project directory — no TUI, no tmux, and
// no terminal required — and MARSHAL exits with the provider's exit code.
// Anything else opens the interactive workspace exactly as it always has.
func (c *command) nativeProvider(ctx context.Context, provider string, args []string) int {
	if oneShotProviderArgs(provider, args) {
		return c.runNativeOneShot(ctx, provider, args)
	}
	tuiArgs := append([]string{nativeTuiFlag(provider)}, args...)
	if err := c.relaunchSession(c.tui(ctx, tuiArgs), tuiArgs); err != nil {
		if c.json {
			_ = c.print(map[string]any{"error": err.Error(), "exit_code": exitCode(err)}, "")
		} else {
			fmt.Fprintln(c.stderr, err)
		}
		return exitCode(err)
	}
	return 0
}

// nativeTuiFlag maps a provider to the flag parseWorkspaceArgs recognizes as
// the native-launch selector for that provider.
func nativeTuiFlag(provider string) string {
	switch provider {
	case "codex":
		return "--codex"
	case "claude":
		return "--claude"
	case "opencode":
		return "--opencode"
	case "antigravity":
		return "--antigravity"
	}
	return "--" + provider
}

// nativeLabel is the provider's display name, used only in diagnostics.
func nativeLabel(provider string) string {
	switch provider {
	case "claude":
		return "Claude"
	case "opencode":
		return "OpenCode"
	case "antigravity":
		return "Antigravity"
	default:
		return "Codex"
	}
}

// nativeBinaryName is the name resolved on PATH; "antigravity" is the agy CLI.
func nativeBinaryName(provider string) string {
	if provider == "antigravity" {
		return "agy"
	}
	return provider
}

// oneShotProviderArgs reports whether args request the provider's own
// non-interactive one-shot mode rather than an interactive session:
//
//	codex:  first argument exec
//	opencode: first argument run
//	claude: -p / --print
//	agy:    -p / --print / --prompt
func oneShotProviderArgs(provider string, args []string) bool {
	switch provider {
	case "codex":
		return len(args) > 0 && args[0] == "exec"
	case "opencode":
		return len(args) > 0 && args[0] == "run"
	case "claude":
		return hasNativeFlag(args, "-p", "--print")
	case "antigravity":
		return hasNativeFlag(args, "-p", "--print", "--prompt")
	}
	return false
}

// hasNativeFlag reports whether any argument before a "--" separator is one of
// the named flags, or carries one of them in a --flag=value form.
func hasNativeFlag(args []string, flags ...string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		for _, flag := range flags {
			if arg == flag || (strings.HasPrefix(flag, "--") && strings.HasPrefix(arg, flag+"=")) {
				return true
			}
		}
	}
	return false
}

// runNativeOneShot runs the provider binary directly, passing stdin, stdout and
// stderr through so the caller drives the agent exactly as if it had invoked
// the provider itself. It returns the provider's exit code.
func (c *command) runNativeOneShot(ctx context.Context, provider string, args []string) int {
	binary, err := project.FindBinary(nativeBinaryName(provider))
	if err != nil {
		fmt.Fprintf(c.stderr, "%v; Install %s and make %s available on PATH, then retry\n",
			err, nativeLabel(provider), nativeBinaryName(provider))
		return exitCode(err)
	}
	root := c.canonicalRoot(ctx)
	var runtime nativeOneShotRuntime
	var capture *nativehistory.Capture
	var denied bool
	var dropped bool
	proposalCtx := ctx
	if !nativeHelpOrVersionArgs(args) {
		if _, statErr := os.Stat(statepath.Path(root, "state.db")); statErr == nil {
			if rt, openErr := openNativeOneShotRuntime(ctx, root); openErr == nil {
				runtime = rt
				providerID := nativehistory.Provider(provider)
				index := statepath.Path(root, provider, "history-oneshot-index.json")
				check := func(path string) bool {
					allowed := runtime.HasReadGrant(path)
					if providerID == nativehistory.Antigravity && filepath.Base(path) == "conversations" {
						home, homeErr := os.UserHomeDir()
						if homeErr == nil {
							summary := filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")
							if _, statErr := os.Stat(summary); statErr == nil && !runtime.HasReadGrant(summary) {
								allowed = false
							}
						}
					}
					if !allowed {
						denied = true
					}
					return allowed
				}
				cap, capErr := nativehistory.New(providerID, root,
					nativehistory.WithBinary(binary),
					nativehistory.WithCaptureTools(true),
					nativehistory.WithIndex(index),
					nativehistory.WithAuthorize(check),
					nativehistory.WithTimeout(4*time.Second),
					nativehistory.WithConsume(func(tr importer.SessionTranscript) error {
						_, secretDropped, proposeErr := runtime.ProposeContinuation(proposalCtx, tr)
						dropped = dropped || secretDropped
						return proposeErr
					}),
				)
				if capErr == nil {
					capture = cap
					if baselineErr := capture.Baseline(ctx); baselineErr != nil {
						fmt.Fprintf(c.stderr, "warning: native %s history capture skipped: %s\n", nativeLabel(provider), shortCaptureError(baselineErr))
						capture = nil
					}
				} else {
					fmt.Fprintf(c.stderr, "warning: native %s history capture skipped: %s\n", nativeLabel(provider), shortCaptureError(capErr))
				}
			}
		}
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = root
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.stdin, c.stdout, c.stderr
	cmd.Env = os.Environ()
	runErr := cmd.Run()
	code := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			code = providerExitCode(exitErr)
		} else {
			fmt.Fprintf(c.stderr, "run %s: %v\n", nativeBinaryName(provider), runErr)
			code = 1
		}
	}
	if capture != nil {
		postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), oneShotCaptureTimeout)
		defer cancel()
		proposalCtx = postCtx
		syncDone := make(chan error, 1)
		go func() { syncDone <- capture.Sync(postCtx) }()
		var syncErr error
		select {
		case syncErr = <-syncDone:
		case <-postCtx.Done():
			// The scan observes its context, so once the deadline passes it
			// stops at the next record and reports. Wait a bounded grace for the
			// capture goroutine to actually finish before the runtime closes, so
			// a large granted history cannot keep reading and proposing after
			// the command has returned.
			select {
			case syncErr = <-syncDone:
			case <-time.After(oneShotCaptureDrain):
				syncErr = postCtx.Err()
			}
		}
		if syncErr != nil {
			fmt.Fprintf(c.stderr, "warning: native %s history capture incomplete: %s\n", nativeLabel(provider), shortCaptureError(syncErr))
		} else if denied {
			fmt.Fprintf(c.stderr, "note: native %s history capture skipped sources without a read grant\n", nativeLabel(provider))
		} else if dropped {
			fmt.Fprintf(c.stderr, "note: native %s history contained content excluded from memory capture\n", nativeLabel(provider))
		}
	}
	if runtime != nil {
		_ = runtime.Close()
	}
	return code
}

func shortCaptureError(err error) string {
	message := strings.Join(strings.Fields(err.Error()), " ")
	if len(message) > 180 {
		message = message[:177] + "..."
	}
	return message
}

func nativeHelpOrVersionArgs(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--help" || arg == "-h" || arg == "--version" || arg == "-V" || arg == "-v" || strings.HasPrefix(arg, "--help=") || strings.HasPrefix(arg, "--version=") {
			return true
		}
	}
	return false
}

// providerExitCode reports the provider's own exit code. A negative value means
// the process was terminated by a signal rather than exiting normally, which has
// no faithful single exit code; 1 is the conventional stand-in.
func providerExitCode(err *exec.ExitError) int {
	if code := err.ExitCode(); code >= 0 {
		return code
	}
	return 1
}
