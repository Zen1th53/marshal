package codex

import (
	"fmt"
	"os"
	"path/filepath"

	providerspec "github.com/Zen1th53/marshal/internal/adapter/provider"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// The governed home keeps its historical directory name (codex_appserver) so
// existing installations keep their mirrored sign-in.

// GovernedCodexHomeDir returns the path to MARSHAL's managed config-free CODEX_HOME.
func GovernedCodexHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, statepath.DirName, "codex_appserver")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("marshal-codex-appserver-%d", os.Getuid()))
}

// EnsureGovernedCodexHome initializes and returns an isolated, config-free CODEX_HOME
// for MARSHAL execution. It automatically mirrors host credentials (auth.json), version info,
// and cached models from CODEX_HOME (or ~/.codex) so the operator does not need to re-login or modify
// their personal ~/.codex/config.toml. It guarantees no config.toml exists in the
// returned directory.
func EnsureGovernedCodexHome() (string, error) {
	target := GovernedCodexHomeDir()
	if err := os.MkdirAll(target, 0o700); err != nil {
		return "", fmt.Errorf("create governed CODEX_HOME: %w", err)
	}
	_ = os.Remove(filepath.Join(target, "config.toml"))

	hostCodex, _, _ := providerspec.Must("codex").StateDir(nil)
	if hostCodex != "" {
		filesToMirror := []string{"auth.json", "version.json", "models_cache.json"}
		for _, fname := range filesToMirror {
			hostPath := filepath.Join(hostCodex, fname)
			targetPath := filepath.Join(target, fname)
			if info, err := os.Stat(hostPath); err == nil && !info.IsDir() {
				if data, err := os.ReadFile(hostPath); err == nil {
					_ = os.WriteFile(targetPath, data, 0o600)
				}
			} else if fname == "auth.json" && os.IsNotExist(err) {
				// A different owner home must not reuse an earlier account.
				_ = os.Remove(targetPath)
			}
		}
	}
	return target, nil
}

// RequireAppServerConfigFree reports whether the Codex home a native Codex
// process would read (an owner CODEX_HOME override, else the governed home)
// carries a config.toml that could inject hooks, sandbox settings, approval
// behavior, or provider configuration outside the reviewed task envelope. The
// TUI Codex status shows the result. It never deletes config or credentials.
func RequireAppServerConfigFree() error {
	var root string
	if home, overridden, _ := providerspec.Must("codex").StateDir(nil); overridden {
		root = home
	} else if governed, err := EnsureGovernedCodexHome(); err == nil && governed != "" {
		root = governed
	} else {
		root = home
	}
	if root != "" {
		config := filepath.Join(root, "config.toml")
		if _, err := os.Stat(config); err == nil {
			return fmt.Errorf("%w: Codex home is not config-free; remove or migrate %s", model.ErrPolicyDenied, config)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("%w: cannot verify Codex home configuration boundary", model.ErrUnavailable)
		}
	}
	return nil
}
