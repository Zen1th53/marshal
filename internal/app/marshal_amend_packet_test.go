package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func TestMarshalAmendOverCapRunUsesScopedPacket(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "codex")
	capture := filepath.Join(root, "prompt")
	tasks := []marshal.Task{
		{PlanTaskID: "fix", Title: "Fix scope", Worker: "codex", Mode: marshal.Governed, Type: marshal.TaskChange, Files: []string{"a.go"}, Criteria: []string{"a.go is updated"}, Checks: []marshal.Check{{Command: "go test ./...", Criteria: []string{"a.go is updated"}}}, Instructions: "Change a.go", ExpectedOutput: "updated a.go"},
		{PlanTaskID: "other", Title: "Other work", Worker: "codex", Mode: marshal.Governed, Type: marshal.TaskChange, Files: []string{"b.go"}, Criteria: []string{"b.go is updated"}, Checks: []marshal.Check{{Command: "go test ./...", Criteria: []string{"b.go is updated"}}}, Instructions: "Change b.go", ExpectedOutput: "updated b.go"},
	}
	run := marshal.Run{PlanID: "PLAN-test", PlanVersion: 1, Tasks: tasks, Pack: &marshal.PlanPack{Requirements: strings.Repeat("history ", projectcontext.MaximumPacketBytes/8+2000)}}
	full, err := json.Marshal(run)
	if err != nil || len(full) <= projectcontext.MaximumPacketBytes {
		t.Fatalf("fixture must exceed the packet cap: %d bytes, %v", len(full), err)
	}
	amended := proposalFromRun(marshal.Run{Tasks: []marshal.Task{tasks[0]}}).Tasks[0]
	amended.Paths = []string{"a.go", "new.go"}
	answer, err := json.Marshal(marshalTaskProposal{Tasks: []marshalProposedTask{amended}})
	if err != nil {
		t.Fatal(err)
	}
	event, err := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": string(answer)}})
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\ncat > '%s'\nprintf '%%s\\n' '%s'\n", capture, string(event))
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	// The amendment may only use workers found on PATH, so the fixture codex
	// must be on it; a host without an installed codex otherwise refuses it.
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	m := &MarshalCLI{Provider: "codex", Binary: binary, ProjectID: "PROJECT-11111111111111111111111111111111"}
	ctx := requiredMarshalTestContext(t, m)
	draft, err := m.Amend(ctx, run, "fix add new.go to the task scope")
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile(capture)
	if err != nil || strings.Contains(string(prompt), "history history") || !strings.Contains(string(prompt), "a.go is updated") {
		t.Fatalf("amendment prompt lost target or included run history: %v", err)
	}
	if !reflect.DeepEqual(draft.Tasks[0].Files, []string{"a.go", "new.go"}) || !reflect.DeepEqual(draft.Tasks[1].Files, tasks[1].Files) {
		t.Fatalf("incorrect scoped draft: %+v", draft.Tasks)
	}
}

func TestMarshalAmendMinimalPacketNamesOversizedField(t *testing.T) {
	run := marshal.Run{Tasks: []marshal.Task{{PlanTaskID: "fix", Criteria: []string{strings.Repeat("x", marshalAmendPacketBytes)}}}}
	_, _, err := marshalAmendPacket(run, "fix update scope")
	if err == nil || !strings.Contains(err.Error(), "target task criteria") {
		t.Fatalf("expected named required field error, got %v", err)
	}
}
