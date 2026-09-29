package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarshalChatBriefingAndLaunchDecision(t *testing.T) {
	brief := marshalRoleBriefing("PROJECT-test")
	for _, phrase := range []string{"You are the Marshal", marshalDraftRelativePath, "Do not edit project files", "Only the person can approve", "/marshal approve"} {
		if !strings.Contains(brief, phrase) {
			t.Errorf("briefing lacks %q", phrase)
		}
	}
	if marshalChatProvider("agy") != "antigravity" || marshalChatProvider("claude") != "claude" || marshalChatProvider("codex") != "codex" {
		t.Fatal("Marshal provider did not select the native session")
	}
}

func TestConsumeMarshalDraft(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, marshalDraftRelativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"plan":{"id":"PLAN-test"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	draft, found, err := consumeMarshalDraft(root)
	if err != nil || !found || draft.Plan.ID != "PLAN-test" {
		t.Fatalf("draft=%+v found=%v err=%v", draft, found, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("draft not consumed: %v", err)
	}
	if _, err := os.Stat(path + ".consumed"); err != nil {
		t.Fatal(err)
	}
}
