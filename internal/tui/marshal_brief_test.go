package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func briefTask() marshal.Task {
	return marshal.Task{PlanTaskID: "T1", Title: "Cache responses", Files: []string{"internal/api/cache.go"}, Criteria: []string{"responses are cached"},
		Checks: []marshal.Check{{Command: "go test ./internal/api"}}, Instructions: "Wrap the handler; leave the store untouched.", ExpectedOutput: "a cached handler"}
}

func TestTaskBriefCarriesApprovedInstructions(t *testing.T) {
	brief := marshalTaskBrief(briefTask(), app.BriefContext{Control: marshal.ControlFree})
	for _, want := range []string{"Cache responses", "Expected output: a cached handler", "Instructions:\nWrap the handler; leave the store untouched.", "internal/api/cache.go", "A change to any other file gets your work returned", "go test ./internal/api", "Choose how to do the task yourself"} {
		if !strings.Contains(brief, want) {
			t.Errorf("free brief lacks %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "Follow the instructions exactly") || strings.Contains(brief, "Earlier attempts") {
		t.Errorf("free first-attempt brief says too much:\n%s", brief)
	}
}

func TestTaskBriefUnderStrictControlHoldsWorkerToInstructions(t *testing.T) {
	brief := marshalTaskBrief(briefTask(), app.BriefContext{Control: marshal.ControlStrict})
	if !strings.Contains(brief, "Follow the instructions exactly") || strings.Contains(brief, "Choose how to do the task yourself") {
		t.Fatalf("strict brief:\n%s", brief)
	}
}

func TestTaskBriefNamesWhyEarlierAttemptsWereReturned(t *testing.T) {
	brief := marshalTaskBrief(briefTask(), app.BriefContext{Control: marshal.ControlFree, Returned: []string{"cache key ignores the query", "no test for eviction"}})
	for _, want := range []string{"Earlier attempts at this task were returned", "- cache key ignores the query", "- no test for eviction"} {
		if !strings.Contains(brief, want) {
			t.Errorf("rework brief lacks %q:\n%s", want, brief)
		}
	}
}

func TestMarshalBriefingFollowsControlLevel(t *testing.T) {
	strict := marshalRoleBriefing([]string{"codex"}, marshal.ControlStrict)
	free := marshalRoleBriefing([]string{"codex"}, marshal.ControlFree)
	if !strings.Contains(strict, `every task must add "instructions"`) || strings.Contains(free, "must add") || !strings.Contains(free, `may add "instructions"`) {
		t.Fatalf("briefings do not follow the control level:\nstrict: %s\nfree: %s", strict, free)
	}
}
