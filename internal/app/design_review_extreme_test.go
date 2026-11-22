//go:build !governed

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeDesignReviewerFreshHostLaunch(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	bin := t.TempDir()
	out := filepath.Join(t.TempDir(), "args")
	t.Setenv("MARSHAL_TEST_REVIEW_ARGS", out)
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	script := `#!/bin/sh
[ -n "$HOME" ] && [ -n "$CODEX_HOME" ] && [ -n "$CLAUDE_CONFIG_DIR" ] || exit 8
printf '%s\n' "$@" > "$MARSHAL_TEST_REVIEW_ARGS"
printf '%s\n' '{"type":"result","session_id":"independent-review","structured_output":{"findings":"Consider reconnect recovery."}}'
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	req := DesignReviewRequest{Provider: "claude", Draft: "Design draft only", ReadingList: []string{"README.md"}}
	result, err := r.ReviewDesign(t.Context(), "codex", req)
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID != "independent-review" || result.Provider != "claude" || result.Findings == "" {
		t.Fatal(result)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"--resume", "--mcp-config", "required_context", "owner transcript"} {
		if strings.Contains(string(data), bad) {
			t.Errorf("review inherited %q: %s", bad, data)
		}
	}
	for _, want := range []string{"--restricted", "Design draft only", "README.md"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("review missing %q", want)
		}
	}
	other := req
	other.Provider = "agy"
	if _, err := r.ReviewDesign(t.Context(), "claude", other); err == nil {
		t.Fatal("a provider that cannot review designs was accepted")
	}
	req.ReadingList = []string{"../private"}
	if _, err := r.ReviewDesign(t.Context(), "codex", req); err == nil {
		t.Fatal("outside reading path accepted")
	}
}
