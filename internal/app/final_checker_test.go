package app

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// Planning no longer keeps the final checker off worker tasks: the
// Marshal's plan gives tasks to Claude Code and Codex, both verified, and
// neither moves. The final check then picks Claude Code, which did tasks,
// to check them in a fresh session.
func TestPlanningKeepsTasksOnClaudeCode(t *testing.T) {
	r, _ := verifierProviderFixture(t, "claude", "codex")
	for _, p := range []string{"claude", "codex"} {
		providerHealth.Clear(p)
		t.Cleanup(func() { providerHealth.Clear(p) })
	}
	draft := MarshalDraft{Tasks: []marshal.Task{
		{PlanTaskID: "a", Worker: "claude", Mode: marshal.Governed},
		{PlanTaskID: "b", Worker: "codex", Mode: marshal.Governed},
	}}
	verified := func(p string) bool { return p == "claude" || p == "codex" }
	err := reassignDraftWorkers(&draft, verified, func(task, from, to string) error {
		t.Fatalf("task %s moved from %s to %s", task, from, to)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Tasks[0].Worker != "claude" || draft.Tasks[1].Worker != "codex" {
		t.Fatalf("tasks moved off Claude Code: %+v", draft.Tasks)
	}
	provider, worker, err := r.independentVerifierChoice(marshal.Run{Tasks: draft.Tasks})
	want := "claude"
	if requiredVerifierAttributionGap("codex") == "" {
		// Where Codex names its model it did tasks too; the first
		// eligible provider checks in a fresh session.
		want = MarshalProviders()[0]
	}
	if err != nil || provider != want || !worker {
		t.Fatalf("final checker after planning: %q worker=%v %v", provider, worker, err)
	}
}

// A provider that did none of the run's tasks is preferred over a worker's
// provider in a fresh session.
func TestFinalCheckPrefersProviderThatDidNoTasks(t *testing.T) {
	r, _ := verifierProviderFixture(t, "claude", "codex")
	for _, p := range []string{"claude", "codex"} {
		providerHealth.Clear(p)
		t.Cleanup(func() { providerHealth.Clear(p) })
	}
	provider, worker, err := r.independentVerifierChoice(marshal.Run{Tasks: []marshal.Task{{Worker: "codex"}}})
	if err != nil || provider != "claude" || worker {
		t.Fatalf("non-worker provider not preferred: %q worker=%v %v", provider, worker, err)
	}
}
