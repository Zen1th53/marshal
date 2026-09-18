package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/update"
)

// Updates are two separate things, and the split is the point. Checking reads
// a public feed and changes nothing, so the workspace may do it on its own.
// Installing replaces the binary the user is running, so it happens when they
// press the key or type the command, and never because a check found something.

// updateCheckTimeout bounds the check the workspace runs for itself. A release
// feed that is slow is not worth waiting on: the workspace has already opened.
const updateCheckTimeout = 8 * time.Second

// handleUpdate implements /update.
//
//	/update           check, and say what is available
//	/update install   download the release and replace this binary
func (h *CommandHandler) handleUpdate(ctx context.Context, args []string) (string, error) {
	install := false
	if len(args) > 0 {
		switch args[0] {
		case "install", "apply":
			install = true
		case "check", "status":
		default:
			return "Usage: /update [check|install]", nil
		}
	}

	checker := update.NewChecker()
	current := h.ws.buildVersion()

	checkCtx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()
	release, newer, err := checker.Available(checkCtx, current)
	if err != nil {
		return fmt.Sprintf("Update check failed: %v\nThis says nothing about the build you are running; it is still %s.", err, current), nil
	}

	if !newer {
		h.ws.setUpdateAvailable("")
		if !install {
			return fmt.Sprintf("MARSHAL %s is the latest release.", current), nil
		}
		return fmt.Sprintf("Nothing to install. MARSHAL %s is the latest release.", current), nil
	}
	h.ws.setUpdateAvailable(release.Tag)

	if !install {
		var b strings.Builder
		fmt.Fprintf(&b, "MARSHAL %s is available. You are running %s.\n", release.Tag, current)
		if release.URL != "" {
			fmt.Fprintf(&b, "  %s\n", release.URL)
		}
		b.WriteString("  [F10] or /update install downloads it, checks it against the release's published checksums, and replaces this binary.")
		return b.String(), nil
	}

	// The download is given its own, longer window: a release archive is not a
	// feed lookup, and a slow connection is not a failure.
	installCtx, cancelInstall := context.WithTimeout(ctx, 10*time.Minute)
	defer cancelInstall()
	path, err := checker.Install(installCtx, release)
	if err != nil {
		return fmt.Sprintf("Update failed: %v\nThe binary in place was not changed; you are still running %s.", err, current), nil
	}
	h.ws.setUpdateAvailable("")
	return fmt.Sprintf("MARSHAL %s installed to %s.\nThis session keeps running %s: exit and start MARSHAL again to use it.",
		release.Tag, path, current), nil
}

// checkForUpdateInBackground looks the release feed up once, off the input
// loop, and records what it found so the workspace can show it.
//
// It never installs, never reports a failure to the user, and does nothing at
// all when MARSHAL_NO_UPDATE_CHECK is set: a workspace that opened is not the
// place to explain that GitHub was unreachable.
func (w *Workspace) checkForUpdateInBackground(ctx context.Context) {
	if update.Disabled() {
		return
	}
	go func() {
		checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), updateCheckTimeout)
		defer cancel()
		release, newer, err := update.NewChecker().Available(checkCtx, w.buildVersion())
		if err != nil || !newer {
			return
		}
		w.setUpdateAvailable(release.Tag)
		w.renderFullView()
	}()
}

// buildVersion is the version this binary was stamped with at release build
// time.
func (w *Workspace) buildVersion() string { return BuildVersion }

// updateKeyCommand is what F10 runs. With a release already found it is the
// install the notice offers; with none found it is the check, so the key means
// the same thing either way and never installs something the user has not been
// shown.
func (w *Workspace) updateKeyCommand() string {
	w.mu.RLock()
	available := w.state.UpdateAvailable
	w.mu.RUnlock()
	if available != "" {
		return "/update install"
	}
	return "/update"
}

func (w *Workspace) setUpdateAvailable(tag string) {
	w.mu.Lock()
	w.state.UpdateAvailable = tag
	w.mu.Unlock()
}

// BuildVersion is the running build's version, set from the same variable the
// CLI reports. It is a variable rather than an import so the TUI does not
// depend on the CLI package.
var BuildVersion = "v0.0.0"
