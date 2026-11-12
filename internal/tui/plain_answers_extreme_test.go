//go:build !governed

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Marshal build's answers say what is there in plain words: no
// "governed" runs, no qualification labels, no NOT_PROBED or UNKNOWN, no
// credential broker, no canonical IDs.
func TestShippedAnswersArePlain(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-plain-answers")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\necho 'No MCP servers configured yet.'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	InvalidateProbeCache()
	t.Cleanup(InvalidateProbeCache)
	for _, tc := range []struct{ line, want string }{
		{"/egress", "No network calls recorded yet."},
		{"/provider", "Codex: installed (" + filepath.Join(dir, "codex") + "). Sign-in is checked when it first runs."},
		{"/optimization cycle-1", `There is no optimization cycle "cycle-1" in this project.`},
		{"/permission credential request codex", "Asked to let tasks use codex's sign-in in this project. The Marshal decides."},
		{"/codex mcp list", "No MCP servers configured yet."},
	} {
		out, err := w.cmd.Handle(t.Context(), tc.line)
		text := out
		if err != nil {
			text += err.Error()
		}
		if !strings.Contains(text, tc.want) {
			t.Fatalf("%s = %q, want %q", tc.line, text, tc.want)
		}
		for _, jargon := range []string{"governed", "NOT_PROBED", "UNKNOWN", "unqualified", "pass-through", "Credential broker", "canonical", "Usage: /egress"} {
			if strings.Contains(text, jargon) {
				t.Fatalf("%s shows %q:\n%s", tc.line, jargon, text)
			}
		}
	}
	for _, state := range []string{"pending", "closed", ""} {
		if out := credentialRevocationMessage("codex", state); strings.Contains(strings.ToLower(out), "broker") {
			t.Fatalf("revocation says broker: %q", out)
		}
	}
}
