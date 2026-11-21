package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The Marshal chat can never read or write the architecture review's state
// (where the owner's Accept lives), drive the Board's tmux pane, or fetch the
// owner's loopback pages, with each provider's own rules. These run on every
// platform; marshal_state_guard_test.go checks the full launches on Linux.
func TestMarshalChatIsDeniedTheOwnersLinksAndPane(t *testing.T) {
	claude := claudeMarshalStateDenyRules()
	for _, want := range []string{
		"Read(./.marshal/architecture/**)", "Edit(./.marshal/architecture/**)",
		"Bash(tmux:*)", "WebFetch(domain:127.0.0.1)", "WebFetch(domain:localhost)", "Read(//proc/**)",
	} {
		if !slices.Contains(claude, want) {
			t.Errorf("Claude deny rules lack %q: %q", want, claude)
		}
	}
	settings, ok := claudeMarshalStateSettings(`{"permissions":{"allow":["Bash(tmux:*)","Read(./.marshal/**)"]}}`)
	if !ok || !strings.Contains(settings, `"Bash(tmux:*)"`) || !strings.Contains(settings, `"Read(./.marshal/architecture/**)"`) {
		// Deny wins over allow in Claude Code, so an allow the person added
		// cannot reopen these.
		t.Errorf("merged settings %s", settings)
	}

	codex := codexMarshalConfineFlags()
	if fs := codex[len(codex)-1]; !strings.Contains(fs, `".marshal/architecture"="none"`) {
		t.Errorf("Codex profile leaves the architecture readable: %s", fs)
	}

	root := t.TempDir()
	agy := antigravityMarshalStateDeny(antigravityRootPaths(root))
	state := filepath.ToSlash(filepath.Join(antigravityRootPaths(root)[0], ".marshal"))
	for _, want := range []string{"read_file(" + state + "/architecture/)", "write_file(" + state + "/architecture/)", "command(tmux)"} {
		if !slices.Contains(agy, want) {
			t.Errorf("agy deny rules lack %q: %q", want, agy)
		}
	}
}

// No footer or Activity text about the architecture review names a link
// file or carries a key.
func TestArchitectureTextsNameNoLinkFile(t *testing.T) {
	link := "http://127.0.0.1:12345/?token=fixture-secret-key"
	for _, text := range []string{architectureOpened, architectureNoBrowser, architectureOpenedNote(link), architectureFailedNote(link)} {
		if strings.Contains(text, "url.txt") || strings.Contains(text, "token") || strings.Contains(text, "fixture-secret-key") {
			t.Errorf("text %q names the link file or the key", text)
		}
	}
}
