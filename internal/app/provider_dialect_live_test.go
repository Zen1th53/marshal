package app

import (
	"os"
	"path/filepath"
	"testing"
)

// A provider at a version no table was written for is read from its own
// help: listed commands are SUPPORTED, unlisted ones stay UNKNOWN.
func TestDialectReadsGrammarFromTheInstalledHelp(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  --version) echo "9.9.9" ;;
  "--help") printf 'Usage: agy [flags]\n\nAvailable subcommands:\n  mcp             Manage MCP servers\n  models          List available models\n  plugin          Manage plugins\n' ;;
  "mcp --help") printf 'Usage: agy mcp <command>\n\nCommands:\n  list      List servers\n  add       Add a server\n' ;;
  "plugin --help") printf 'Commands:\n  list\n  install\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	d := ObserveProviderDialect(t.Context(), "agy")
	if d.Version != "9.9.9" {
		t.Fatalf("version = %q", d.Version)
	}
	for op, want := range map[string]ProviderSupport{
		"mcp": ProviderSupported, "models": ProviderSupported, "mcp list": ProviderSupported, "mcp add": ProviderSupported,
		"plugin install": ProviderSupported, "mcp remove": ProviderUnknown, "changelog": ProviderUnknown,
	} {
		if got := d.Operation(op).Status; got != want {
			t.Errorf("%s = %s, want %s", op, got, want)
		}
	}
	if err := d.Check("mcp remove", true); err != nil {
		t.Fatalf("an unlisted operation must still pass through: %v", err)
	}
}
