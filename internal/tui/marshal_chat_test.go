package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarshalChatBriefingAndLaunchDecision(t *testing.T) {
	brief := marshalRoleBriefing([]string{"codex", "agy"})
	for _, phrase := range []string{"You are the Marshal", marshalDraftRelativePath, "Do not edit project files", "Only the person can approve", "/marshal approve", `{"tasks":[`, "codex, agy"} {
		if !strings.Contains(brief, phrase) {
			t.Errorf("briefing lacks %q", phrase)
		}
	}
	// The model is asked for tasks only; plan identity and digests are the
	// runtime's to compute.
	for _, phrase := range []string{"MarshalDraft", "ExecutionPlan", "project_id", "digest"} {
		if strings.Contains(brief, phrase) {
			t.Errorf("briefing asks the model for %q", phrase)
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
	const written = `{"tasks":[]}`
	if err := os.WriteFile(path, []byte(written), 0600); err != nil {
		t.Fatal(err)
	}
	data, found, err := consumeMarshalDraft(root)
	if err != nil || !found || string(data) != written {
		t.Fatalf("data=%q found=%v err=%v", data, found, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("draft not consumed: %v", err)
	}
	if _, err := os.Stat(path + ".consumed"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := consumeMarshalDraft(root); found || err != nil {
		t.Fatalf("consumed draft picked up again: found=%v err=%v", found, err)
	}
}
