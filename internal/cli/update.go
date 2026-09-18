package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/update"
)

// update reports whether a newer release exists and, when asked, installs it.
//
//	marshal update           check only
//	marshal update install   download, verify and replace this binary
//
// The check reads a public feed and changes nothing. The install is the same
// verified path install.sh takes: the archive is checked against the release's
// published SHA-256, and a download that fails that check is not installed.
func (c command) update(ctx context.Context, args []string) error {
	install := false
	if len(args) > 0 {
		switch args[0] {
		case "install", "apply":
			install = true
		case "check", "status":
		default:
			return fmt.Errorf("%w: usage: marshal update [check|install]", model.ErrInvalid)
		}
	}

	checker := update.NewChecker()
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	release, newer, err := checker.Available(checkCtx, Version)
	if err != nil {
		return fmt.Errorf("update check failed: %w", err)
	}

	if !newer {
		return c.print(map[string]any{
			"current":   Version,
			"latest":    release.Tag,
			"available": false,
		}, fmt.Sprintf("MARSHAL %s is the latest release.", Version))
	}

	if !install {
		return c.print(map[string]any{
			"current":   Version,
			"latest":    release.Tag,
			"url":       release.URL,
			"available": true,
		}, fmt.Sprintf("MARSHAL %s is available. You are running %s.\n  %s\n  Install it with: marshal update install",
			release.Tag, Version, release.URL))
	}

	installCtx, cancelInstall := context.WithTimeout(ctx, 10*time.Minute)
	defer cancelInstall()
	path, err := checker.Install(installCtx, release)
	if err != nil {
		// The binary in place was not touched, and saying so is the part the
		// user needs: a failed update leaves a working MARSHAL behind.
		return fmt.Errorf("update failed, and %s was left in place: %w", Version, err)
	}
	return c.print(map[string]any{
		"current":   Version,
		"installed": release.Tag,
		"path":      path,
	}, fmt.Sprintf("MARSHAL %s installed to %s.\nRunning processes keep the build they started with; start MARSHAL again to use it.", release.Tag, path))
}
