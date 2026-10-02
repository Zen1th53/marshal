package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// fakeWorkerOnPath makes one worker CLI discoverable, as an installed one is.
func fakeWorkerOnPath(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// The interactive Marshal writes only the task list its briefing asks for.
// The runtime must turn that into a draft planning accepts; before, the model
// had to reproduce the graph digest itself and planning refused the draft.
func TestMarshalChatProposalStartsPlanning(t *testing.T) {
	fakeWorkerOnPath(t, "codex")
	s, _ := marshalFixture(t, 1)
	proposal := `{"tasks":[
		{"id":"a","title":"write a.txt","criteria":["file a exists"],"paths":["a.txt"],"depends_on":[],"worker":"codex","checks":[{"command":"test -f a.txt","criteria":["file a exists"]}]},
		{"id":"b","title":"write b.txt","criteria":["file b exists"],"paths":["b.txt"],"depends_on":["a"],"worker":"codex","checks":[{"command":"test -f b.txt","criteria":["file b exists"]}]}
	]}`
	draft, err := s.DraftFromProposal([]byte(proposal), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Plan.Graph.Digest == "" || string(draft.Plan.ProjectID) != s.ProjectID {
		t.Fatalf("runtime did not build the plan: digest=%q project=%q", draft.Plan.Graph.Digest, draft.Plan.ProjectID)
	}
	run, err := s.StartPlanningFromDraft(t.Context(), "run", draft.Plan.Goal.GoalID, draft, marshal.Budget{})
	if err != nil || run.State != marshal.Drafting || len(run.Tasks) != 2 {
		t.Fatalf("planning refused the chat draft: %+v %v", run, err)
	}
}

func TestMarshalChatProposalRefusesWhatTheBriefingDidNotAskFor(t *testing.T) {
	fakeWorkerOnPath(t, "codex")
	s, _ := marshalFixture(t, 1)
	for name, proposal := range map[string]string{
		"old full-plan format": `{"plan":{"id":"PLAN-x"},"tasks":[]}`,
		"unlisted worker":      `{"tasks":[{"id":"a","title":"t","criteria":["c"],"paths":["a.txt"],"depends_on":[],"worker":"someone","checks":[{"command":"true","criteria":["c"]}]}]}`,
		"task without checks":  `{"tasks":[{"id":"a","title":"t","criteria":["c"],"paths":["a.txt"],"depends_on":[],"worker":"codex","checks":[]}]}`,
		"Marshal's own family": `{"tasks":[{"id":"a","title":"t","criteria":["c"],"paths":["a.txt"],"depends_on":[],"worker":"claude","checks":[{"command":"true","criteria":["c"]}]}]}`,
		"not JSON":             `tasks: a`,
	} {
		if _, err := s.DraftFromProposal([]byte(proposal), "claude"); err == nil {
			t.Errorf("%s: accepted", name)
		} else if name == "old full-plan format" && !strings.Contains(err.Error(), "invalid Marshal draft JSON") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
