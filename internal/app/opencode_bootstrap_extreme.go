//go:build !governed

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// Pinned official package contents; preparation verifies every npm tarball's
// SHA-512 integrity first. No native config, plugins or credentials are reused.
const opencodeBootstrapTreeSHA256 = "07f61eec0c41559e9ae27d7af116b72d7fc84162043ce3c54584e70f479ed58c"
const opencodeBootstrapVersion = "1.18.16"
const opencodeBootstrapRevision = "1.18.16-toml4.2.0"
const opencodeBootstrapTarget = "/home/marshal/provider-bootstrap"

func privateOpenCodeVersion() string { return opencodeBootstrapVersion }

func privateProviderBootstrap(provider string) ([]model.Bind, []string, error) {
	if provider != "opencode" {
		return nil, nil, nil
	}
	root := os.Getenv("MARSHAL_OPENCODE_BOOTSTRAP_DIR")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, nil, err
		}
		root = filepath.Join(home, ".cache", "marshal", "opencode-bootstrap", opencodeBootstrapRevision)
	}
	root, err := filepath.Abs(root)
	if err == nil {
		err = validateOpenCodeBootstrap(root, opencodeBootstrapTreeSHA256)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%w: isolated OpenCode %s offline bootstrap unavailable; run python3 tools/prepare_opencode_bootstrap.py before retrying: %v", model.ErrUnavailable, opencodeBootstrapVersion, err)
	}
	return []model.Bind{{Source: root, Target: opencodeBootstrapTarget}}, []string{
		"XDG_CONFIG_HOME=" + opencodeBootstrapTarget,
		"OPENCODE_CONFIG_DIR=" + opencodeBootstrapTarget + "/opencode",
		"OPENCODE_DISABLE_PROJECT_CONFIG=true",
		"OPENCODE_PURE=true",
		"OPENCODE_DISABLE_MODELS_FETCH=true",
		"OPENCODE_DISABLE_AUTOUPDATE=true",
	}, nil
}

func validateOpenCodeBootstrap(root, want string) error {
	got, err := opencodeBootstrapDigest(root)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("dependency tree differs from pinned official contents")
	}
	return nil
}

func opencodeBootstrapDigest(root string) (string, error) {
	var paths []string
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("dependency symlink refused")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("dependency special file refused")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel != "opencode/package.json" && rel != "opencode/package-lock.json" && rel != "opencode/.gitignore" && !strings.HasPrefix(rel, "opencode/node_modules/") {
			return fmt.Errorf("foreign bootstrap configuration refused")
		}
		total += info.Size()
		if info.Size() > 16<<20 || total > 128<<20 || len(paths) >= 20000 {
			return fmt.Errorf("dependency tree exceeds bound")
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	tree := sha256.New()
	for _, rel := range paths {
		file, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		hash := sha256.New()
		n, readErr := io.Copy(hash, io.LimitReader(file, (16<<20)+1))
		closeErr := file.Close()
		if readErr != nil {
			return "", readErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		if n > 16<<20 {
			return "", fmt.Errorf("dependency file exceeds bound")
		}
		fmt.Fprintf(tree, "%s%c%s\n", rel, 0, hex.EncodeToString(hash.Sum(nil)))
	}
	return hex.EncodeToString(tree.Sum(nil)), nil
}

// OpenCode searches HOME/.opencode even with project config disabled. Keep that
// config absent: an empty writable directory triggers an unpinned npm install.
// SQLite/cache state remains writable in the separate XDG data/cache trees.
func privateProviderStorageDirs(provider string, paths []string) []string {
	if provider == "agy" && privateAGYEnabled() {
		return append(paths, "/home/marshal/.gemini/antigravity-cli")
	}
	if provider != "opencode" {
		return paths
	}
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if path != "/home/marshal/.opencode" {
			filtered = append(filtered, path)
		}
	}
	return filtered
}

// Register the explicitly selected local model without loading host config.
// The SDK is bundled in the pinned CLI. Egress still uses the governed proxy.
func privateOpenCodeLocalConfig(modelName, host string) ([]string, error) {
	return privateOpenCodeModelConfig(modelName, host, false)
}

func privateOpenCodeProbeConfig(modelName, host string) ([]string, error) {
	return privateOpenCodeModelConfig(modelName, host, true)
}

func privateOpenCodeModelConfig(modelName, host string, probe bool) ([]string, error) {
	if !strings.HasPrefix(modelName, "ollama/") {
		return nil, nil
	}
	id := strings.TrimPrefix(modelName, "ollama/")
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("local OpenCode model is required")
	}
	modelConfig := map[string]any{"name": id}
	if probe {
		// Local qualification needs a short deterministic reply before its
		// fixed 90-second deadline. Ordinary governed workers retain their
		// configured model budget and output capacity.
		modelConfig["options"] = map[string]any{"temperature": 0}
		modelConfig["limit"] = map[string]any{"context": 131072, "output": 64}
	}
	raw, err := json.Marshal(map[string]any{"provider": map[string]any{"ollama": map[string]any{
		"npm":     "@ai-sdk/openai-compatible",
		"name":    "Local Ollama",
		"options": map[string]string{"baseURL": strings.TrimRight(host, "/") + "/v1"},
		"models":  map[string]any{id: modelConfig},
	}}})
	if err != nil {
		return nil, err
	}
	return []string{"OPENCODE_CONFIG_CONTENT=" + string(raw)}, nil
}
