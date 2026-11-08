package sandbox

import (
	"github.com/Zen1th53/marshal/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestUnsealedAntigravityConfigurationDirectoryRefused(t *testing.T) {
	source := t.TempDir()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "mcp_config.json"), []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	req := model.SandboxRequest{Worktree: root, ReadOnlyBinds: []model.Bind{{Source: source, Target: "/home/marshal/.gemini/config"}}}
	if _, err := NewBwrap("/usr/bin/bwrap").Wrap(req, []string{"/bin/true"}); err == nil {
		t.Fatal("unsealed provider configuration directory accepted")
	}
}
