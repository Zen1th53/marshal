package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// Mount only the resolved CLI installation, including sibling helpers. A home
// or credential root (or its ancestor) can never be an installation mount.
func cliInstallationMount(binary string) (string, model.Bind, error) {
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return "", model.Bind{}, err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", model.Bind{}, err
	}
	dir := filepath.Dir(resolved)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", model.Bind{}, err
	}
	protected := []string{home}
	for _, name := range []string{".codex", ".claude", ".gemini", ".opencode", ".config", ".aws", ".ssh", ".local/share/keyrings", ".local/share/opencode"} {
		protected = append(protected, filepath.Join(home, name))
	}
	for _, key := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME"} {
		if value := os.Getenv(key); value != "" {
			protected = append(protected, value)
		}
	}
	for _, root := range protected {
		if canonical, e := filepath.EvalSymlinks(root); e == nil {
			root = canonical
		}
		root, err = filepath.Abs(root)
		if err != nil {
			return "", model.Bind{}, err
		}
		rel, e := filepath.Rel(dir, root)
		if e != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return "", model.Bind{}, fmt.Errorf("CLI installation mount refused: home or credential directory")
		}
	}
	// A directory holding sign-in storage is unsafe even with a custom install.
	for _, name := range []string{"auth.json", ".credentials.json"} {
		if _, e := os.Lstat(filepath.Join(dir, name)); e == nil || !os.IsNotExist(e) {
			return "", model.Bind{}, fmt.Errorf("CLI installation mount refused: credential storage")
		}
	}
	return resolved, model.Bind{Source: dir, Target: dir}, nil
}
