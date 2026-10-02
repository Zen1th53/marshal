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

func TestTaskBriefCarriesTheApprovedPlanPack(t *testing.T) {
	brief := marshalTaskBrief(briefTask(), app.BriefContext{Control: marshal.ControlFree, Requirements: "keep the API stable", Index: "T1 then T2", Note: "the store is shared with T2"})
	for _, want := range []string{"Task note from the approved plan:\nthe store is shared with T2", "requirements the person approved", "keep the API stable", "T1 then T2"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief lacks %q:\n%s", want, brief)
		}
	}
	plain := marshalTaskBrief(briefTask(), app.BriefContext{Control: marshal.ControlFree})
	if strings.Contains(plain, "approved plan") || strings.Contains(plain, "requirements the person approved") {
		t.Errorf("a run without a pack speaks of one:\n%s", plain)
	}
}

func TestMarshalBriefingFollowsControlLevel(t *testing.T) {
	settings := marshal.DefaultSettings()
	free, err := marshalRoleBriefing([]string{"codex"}, settings, marshal.Standard)
	if err != nil {
		t.Fatal(err)
	}
	settings.Control = marshal.ControlStrict
	strict, err := marshalRoleBriefing([]string{"codex"}, settings, marshal.Standard)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strict, `every task must add "instructions"`) || strings.Contains(free, "must add") || !strings.Contains(free, `may add "instructions"`) {
		t.Fatalf("briefings do not follow the control level:\nstrict: %s\nfree: %s", strict, free)
	}
}

func TestMarshalBriefingStatesTier(t *testing.T) {
	for _, tt := range []struct {
		tier marshal.Tier
		want string
	}{
		{marshal.Standard, "- Tier: Standard."},
		{marshal.Ultra, "- Tier: ULTRA (independent cross-review and verification)."},
	} {
		t.Run(string(tt.tier), func(t *testing.T) {
			brief, err := marshalRoleBriefing([]string{"codex"}, marshal.DefaultSettings(), tt.tier)
			if err != nil {
				t.Fatal(err)
			}
			_, run, found := strings.Cut(brief, "\nThis run:\n")
			if !found || !strings.Contains(run, tt.want+"\n") || strings.Count(run, "- Tier:") != 1 {
				t.Fatalf("briefing does not state tier %s:\n%s", tt.tier, brief)
			}
		})
	}
}
