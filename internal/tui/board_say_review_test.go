package tui

import (
	"strings"
	"testing"
)

// TestChatReviewAskPlainWords verifies a permission prompt for the Marshal's
// own draft review is described in plain words, keeps the command for the
// details note, and only says read-only when the command's sandbox is.
func TestChatReviewAskPlainWords(t *testing.T) {
	const codexArgv = `codex exec --ephemeral --skip-git-repo-check -s read-only "Review .marshal/design/design_draft.md and list findings"`
	for name, tc := range map[string]struct {
		frame     string
		ok        bool
		plain     string
		argvParts []string
	}{
		"claude bash prompt for codex design review": {
			frame: "Bash command\n│ " + codexArgv + " │\nDo you want to proceed?\n❯ 1. Yes\n  2. No, and tell Claude what to do differently (esc)\n",
			ok:    true, plain: "The Marshal asks Codex to review its design draft (read-only)",
			argvParts: []string{"codex exec --ephemeral --skip-git-repo-check -s read-only", "design_draft.md"},
		},
		"command wrapped in a narrow pane": {
			frame: "Would you like to run the following command?\n$ codex exec --ephemeral\n--skip-git-repo-check -s read-only\n\"Review design_draft.md\"\n› 1. Yes, proceed\n",
			ok:    true, plain: "The Marshal asks Codex to review its design draft (read-only)",
			argvParts: []string{"codex exec --ephemeral --skip-git-repo-check -s read-only \"Review design_draft.md\""},
		},
		"concepts brief by claude in plan mode": {
			frame: "Bash command\nclaude -p --permission-mode plan \"Review concepts_brief.md\"\nDo you want to proceed?\n",
			ok:    true, plain: "The Marshal asks Claude to review its concepts brief (read-only)",
		},
		"writable sandbox is never called read-only": {
			frame: "Bash command\ncodex exec -s read-only --dangerously-bypass-approvals-and-sandbox \"Review design_draft.md\"\nDo you want to proceed?\n",
			ok:    true, plain: "The Marshal asks Codex to review its design draft",
		},
		"other commands keep the generic words": {
			frame: "Bash command\nrm -rf build\nDo you want to proceed?\n",
		},
	} {
		got, ok := chatReviewAskIn(tc.frame)
		if ok != tc.ok || got.plain != tc.plain {
			t.Errorf("%s: got %+v ok=%v, want %q ok=%v", name, got, ok, tc.plain, tc.ok)
			continue
		}
		for _, part := range tc.argvParts {
			if !strings.Contains(got.argv, part) {
				t.Errorf("%s: argv %q misses %q", name, got.argv, part)
			}
		}
		if strings.Contains(got.argv, "Do you want") || strings.Contains(got.argv, "1. Yes") {
			t.Errorf("%s: argv took in the prompt's chrome: %q", name, got.argv)
		}
	}

	review, _ := chatReviewAskIn("Bash command\n" + codexArgv + "\nDo you want to proceed?\n")
	msg := chatWaitWords("claude", chatWaitPermission, false, false, review)
	if !strings.Contains(msg, "Waiting for you: The Marshal asks Codex to review its design draft (read-only)") || !strings.Contains(msg, "F11") {
		t.Fatalf("review wait words = %q", msg)
	}
	if strings.Contains(msg, "codex exec") || strings.Contains(msg, "--skip-git-repo-check") {
		t.Fatalf("review wait words show the raw command: %q", msg)
	}
	if !strings.Contains(msg, "Activity has the exact command") {
		t.Fatalf("review wait words do not say where the command is: %q", msg)
	}
}
