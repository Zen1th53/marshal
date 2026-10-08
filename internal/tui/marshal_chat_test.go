package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestMarshalChatBriefingAndLaunchDecision(t *testing.T) {
	brief, err := marshalRoleBriefing([]string{"codex", "agy"}, marshal.DefaultSettings(), marshal.Standard)
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"You are the Marshal", marshalDraftRelativePath, app.MarshalPackRelativePath, "tasks/<id>.md", "Do not edit project files", "Only the person approves the plan", "/marshal approve", `{"tasks":[`, "codex, agy", "acceptance mode marshal-then-user", "Current control level: free", "This run:\n- Tier: Standard.\n", "write the pack first, then plan-draft.json, then read back", "Do not read a planned path before its write succeeds", "Confirm each write succeeded"} {
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
	if err := os.WriteFile(path+".ready", []byte("ready\n"), 0600); err != nil {
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

func TestDraftReadBackBeforePublication(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, marshalDraftRelativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"tasks":[]}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := consumeMarshalDraft(root); err != nil || found {
		t.Fatalf("unpublished draft consumed: %v %v", found, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(data) {
		t.Fatalf("producer read-back: %s %v", got, err)
	}
	if err := os.WriteFile(path+".ready", []byte("ready\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := consumeMarshalDraft(root); err != nil || !found {
		t.Fatalf("published draft: %v %v", found, err)
	}
	brief, err := marshalRoleBriefing([]string{"codex"}, marshal.DefaultSettings(), marshal.Standard)
	if err != nil || !strings.Contains(brief, marshalDraftRelativePath+".ready") {
		t.Fatalf("completion marker absent: %v", err)
	}
}

func TestWatcherDoesNotMoveDraftDuringProducerReadBack(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, marshalDraftRelativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	poll := make(chan struct{})
	result := make(chan error)
	go func() {
		defer close(result)
		for range poll {
			_, found, err := consumeMarshalDraft(root)
			if err == nil && found {
				err = fmt.Errorf("watcher consumed before completion marker")
			}
			result <- err
		}
	}()
	defer close(poll)
	for i := 1; i <= 100; i++ {
		written := strings.Repeat(" ", i) + `{"tasks":[]}`
		if err := os.WriteFile(path, []byte(written), 0600); err != nil {
			t.Fatal(err)
		}
		poll <- struct{}{}
		data, readErr := os.ReadFile(path)
		pollErr := <-result
		if readErr != nil || pollErr != nil || string(data) != written {
			t.Fatalf("read-back raced watcher: read=%v watcher=%v data=%q", readErr, pollErr, data)
		}
	}
}
