//go:build !governed

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCodeBootstrapTreeValidation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "opencode", "node_modules", "@opencode-ai", "plugin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "package.json")
	if err := os.WriteFile(file, []byte(`{"name":"@opencode-ai/plugin","version":"1.18.16"}`), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := opencodeBootstrapDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOpenCodeBootstrap(root, digest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateOpenCodeBootstrap(root, digest); err == nil {
		t.Fatal("tampered dependency accepted")
	}
}

func TestOpenCodeBootstrapRejectsForeignFilesAndLinks(t *testing.T) {
	for _, name := range []string{"auth.json", "opencode/opencode.json", "opencode/plugins/plugin.js", "opencode/node_modules/link"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(name, "link") {
				if err := os.Symlink("/etc/passwd", file); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(file, []byte("foreign"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := opencodeBootstrapDigest(root); err == nil {
				t.Fatal("foreign config/auth/link accepted")
			}
		})
	}
}

func TestPrivateProviderBootstrapMissingIsActionable(t *testing.T) {
	t.Setenv("MARSHAL_OPENCODE_BOOTSTRAP_DIR", filepath.Join(t.TempDir(), "missing"))
	if _, _, err := privateProviderBootstrap("opencode"); err == nil || !strings.Contains(err.Error(), "prepare_opencode_bootstrap.py") {
		t.Fatalf("missing offline bootstrap error: %v", err)
	}
	if binds, env, err := privateProviderBootstrap("claude"); err != nil || len(binds) != 0 || len(env) != 0 {
		t.Fatalf("foreign provider affected: %v", err)
	}
}

func TestOpenCodeBootstrapDigestMatchesCrossLanguageFlatPathGolden(t *testing.T) {
	root := t.TempDir()
	for rel, data := range map[string]string{"opencode/node_modules/pkg/x.js": "first", "opencode/node_modules/pkg/x/child.js": "second"} {
		file := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := opencodeBootstrapDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != "bca24b87388d81b87e3212fa8a2dadfc4bb00cbec5180f9fa9bec7a4b4b07f4e" {
		t.Fatalf("cross-language flat path digest differs: %s", got)
	}
}

func TestPrivateOpenCodeAvoidsWritableHomePluginBootstrap(t *testing.T) {
	paths := []string{"/home/marshal/.opencode", "/home/marshal/.local", "/home/marshal/.local/share/opencode", "/home/marshal/.cache/opencode"}
	got := privateProviderStorageDirs("opencode", paths)
	if len(got) != 3 || strings.Join(got, "|") != strings.Join(paths[1:], "|") {
		t.Fatalf("writable HOME config would trigger unpinned npm bootstrap: %v", got)
	}
	if got := privateProviderStorageDirs("claude", paths); strings.Join(got, "|") != strings.Join(paths, "|") {
		t.Fatalf("foreign provider storage changed: %v", got)
	}
}

func TestPrivateOpenCodeLocalConfigUsesOnlyExplicitModelAndEndpoint(t *testing.T) {
	env, err := privateOpenCodeLocalConfig("ollama/qwythos-9b", "http://127.0.0.1:11434/")
	if err != nil || len(env) != 1 || !strings.HasPrefix(env[0], "OPENCODE_CONFIG_CONTENT=") {
		t.Fatalf("missing isolated local provider configuration: %v %v", env, err)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(env[0], "OPENCODE_CONFIG_CONTENT=")), &config); err != nil {
		t.Fatal(err)
	}
	provider := config["provider"].(map[string]any)["ollama"].(map[string]any)
	if len(config) != 1 || provider["npm"] != "@ai-sdk/openai-compatible" || provider["options"].(map[string]any)["baseURL"] != "http://127.0.0.1:11434/v1" || len(provider["models"].(map[string]any)) != 1 {
		t.Fatalf("unexpected configuration: %#v", config)
	}
	if _, ok := provider["models"].(map[string]any)["qwythos-9b"]; !ok {
		t.Fatal("explicit model not registered")
	}
	if env, err := privateOpenCodeLocalConfig("opencode/remote", "http://127.0.0.1:11434"); err != nil || len(env) != 0 {
		t.Fatalf("remote provider changed: %v %v", env, err)
	}
	if _, err := privateOpenCodeLocalConfig("ollama/", "http://127.0.0.1:11434"); err == nil {
		t.Fatal("empty local model accepted")
	}
}

func TestPrivateOpenCodeProbeConfigBoundsOnlyProbeGeneration(t *testing.T) {
	probeEnv, err := privateOpenCodeProbeConfig("ollama/qwythos-9b", "http://127.0.0.1:11434")
	if err != nil || len(probeEnv) != 1 {
		t.Fatalf("probe config: %v %v", probeEnv, err)
	}
	workerEnv, err := privateOpenCodeLocalConfig("ollama/qwythos-9b", "http://127.0.0.1:11434")
	if err != nil || len(workerEnv) != 1 {
		t.Fatalf("worker config: %v %v", workerEnv, err)
	}
	var probe, worker map[string]any
	for _, item := range []struct {
		raw    string
		target *map[string]any
	}{{probeEnv[0], &probe}, {workerEnv[0], &worker}} {
		if err := json.Unmarshal([]byte(strings.TrimPrefix(item.raw, "OPENCODE_CONFIG_CONTENT=")), item.target); err != nil {
			t.Fatal(err)
		}
	}
	probeModel := probe["provider"].(map[string]any)["ollama"].(map[string]any)["models"].(map[string]any)["qwythos-9b"].(map[string]any)
	workerModel := worker["provider"].(map[string]any)["ollama"].(map[string]any)["models"].(map[string]any)["qwythos-9b"].(map[string]any)
	if probeModel["options"].(map[string]any)["temperature"] != float64(0) || probeModel["limit"].(map[string]any)["output"] != float64(64) {
		t.Fatalf("probe model not bounded: %#v", probeModel)
	}
	if _, ok := workerModel["options"]; ok {
		t.Fatalf("probe options leaked to worker: %#v", workerModel)
	}
	if _, ok := workerModel["limit"]; ok {
		t.Fatalf("probe output cap leaked to worker: %#v", workerModel)
	}
}
