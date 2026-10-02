package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// reviewPrompt runs one review turn against a stand-in CLI and returns the
// prompt the Marshal model was given.
func reviewPrompt(t *testing.T, control marshal.Control) string {
	t.Helper()
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt")
	script := "#!/bin/sh\nprintf '%s' \"$2\" > " + promptFile + "\n" +
		`printf '%s' '{"session_id":"s","structured_output":{"Verdict":"accept","Reviewer":"marshal","Reasons":[],"EvidenceRefs":[]}}'` + "\n"
	binary := filepath.Join(dir, "claude")
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cli := &MarshalCLI{Provider: "claude", Binary: binary, Dir: dir}
	task := marshal.Task{PlanTaskID: "a", Instructions: "wrap the handler"}
	if _, err := cli.Review(t.Context(), task, marshal.HandIn{}, control); err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(prompt)
}

func TestMarshalReviewPromptFollowsControlLevel(t *testing.T) {
	strict := reviewPrompt(t, marshal.ControlStrict)
	if !strings.Contains(strict, "return it for any departure from the instructions") || !strings.Contains(strict, "wrap the handler") {
		t.Fatalf("strict review prompt: %s", strict)
	}
	free := reviewPrompt(t, marshal.ControlFree)
	if !strings.Contains(free, "that choice is not a reason to return it") || strings.Contains(free, "departure from the instructions") {
		t.Fatalf("free review prompt: %s", free)
	}
}
