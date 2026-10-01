//go:build linux

package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// The real binary selects a Codex model through the operator boundary,
// validated against a catalog from a provider double, and reads it back.
func TestPTYModelSelectAppliesToFutureRuns(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	doubles := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = debug ] && [ \"$2\" = models ]; then\n" +
		"  printf '%s' '{\"models\":[{\"slug\":\"gpt-test\",\"display_name\":\"Test\",\"visibility\":\"list\",\"is_default\":true}]}'\n" +
		"  exit 0\nfi\nexit 1\n"
	if err := os.WriteFile(filepath.Join(doubles, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", doubles+string(os.PathListSeparator)+os.Getenv("PATH"))
	project := initProject(t, bin)
	t.Chdir(project)

	terminal := startFrozenTUIInProject(t, 40, 200, bin, project, "tui", "SESSION-pty-model")
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/model select codex gpt-unknown")
	terminal.mustSee("NOT applied")
	terminal.sendLine("/model select codex gpt-test")
	terminal.mustSee("Future governed codex runs will use gpt-test (preference revision 1)")
	terminal.sendLine("/model show")
	terminal.mustSee("gpt-test (revision 1)")
	terminal.sendLine("/model select opencode some-model")
	terminal.mustSee("do not read a model preference")
}
