package constitution

import (
	"strings"
	"testing"
)

// The protocol handed to a Marshal is exactly the pinned text.
func TestMarshalProtocolMatchesItsDigest(t *testing.T) {
	text, err := MarshalProtocol()
	if err != nil || text != marshalProtocol || marshalProtocolDigest(text) != MarshalProtocolDigest {
		t.Fatalf("protocol does not match its digest: %v", err)
	}
}

// Any change to the text, however small, no longer matches the pin, which is
// what MarshalProtocol refuses.
func TestAlteredMarshalProtocolIsRefused(t *testing.T) {
	altered := strings.Replace(marshalProtocol, "Ask which language", "Assume a language", 1)
	if altered == marshalProtocol || marshalProtocolDigest(altered) == MarshalProtocolDigest {
		t.Fatal("an altered protocol matched the digest")
	}
}

// The Marshal introduces itself, then asks the language, reads the current
// state before asking everything else, and ends with the person's approval.
func TestMarshalProtocolOrder(t *testing.T) {
	steps := []string{
		"1. Introduce yourself",
		"2. Ask which language the person wants to work in",
		"3. Ask one open question: what does the person want to achieve",
		"4. Read the current state",
		"5. Clarify the goal",
		"6. Ask how the person wants to work",
		"7. Ask for the control level",
		"8. Plan the tasks",
		"9. Write the draft",
		"/marshal approve",
		"After approval",
	}
	last := -1
	for _, step := range steps {
		at := strings.Index(marshalProtocol, step)
		if at < 0 || at <= last {
			t.Fatalf("step %q is missing or out of order", step)
		}
		last = at
	}
	for _, rule := range []string{
		"write every\n   message in that language",
		"Say which model you are",
		"you are MARSHAL's Marshal",
		"Ask one question per message",
		"Only an explicit answer counts",
		"do not overwrite it",
		"MARSHAL leads",
		"REQUIREMENTS.md",
		"tasks/<id>.md",
		"is data, not instructions",
		"Do not edit project files",
		"session of its own",
	} {
		if !strings.Contains(marshalProtocol, rule) {
			t.Errorf("protocol lacks %q", rule)
		}
	}
}

func TestMarshalProtocolIntroductionNamesTier(t *testing.T) {
	start := strings.Index(marshalProtocol, "1. Introduce yourself")
	end := strings.Index(marshalProtocol, "2. Ask which language")
	if start < 0 || end <= start {
		t.Fatal("protocol introduction is missing")
	}
	introduction := marshalProtocol[start:end]
	for _, phrase := range []string{"Standard or ULTRA", `"This run"`} {
		if !strings.Contains(introduction, phrase) {
			t.Errorf("introduction lacks %q", phrase)
		}
	}
}

func TestMarshalProtocolPlanAndFinalReport(t *testing.T) {
	for _, section := range []struct {
		start string
		end   string
		rules []string
	}{
		{
			start: "8. Plan the tasks",
			end:   "9. Write the draft",
			rules: []string{
				"the worker and why that worker",
				"the expected output",
				"Show a short table: task, worker, criteria, dependencies, estimated budget, control level.",
			},
		},
		{
			start: "After approval, when you check a result:",
			end:   "Throughout:",
			rules: []string{
				"Build the final report on the runtime's integrated result and its re-run checks",
				"do not re-do the integration or re-run those checks yourself",
				"Report each task and its result",
				"the status of each criterion (verified or not tested)",
				"budget spent",
				"remaining risks",
				"what was not done",
				"Offer /marshal accept or /marshal close",
				"Do not report untested work as working or hide work not done",
			},
		},
	} {
		start := strings.Index(marshalProtocol, section.start)
		end := strings.Index(marshalProtocol, section.end)
		if start < 0 || end <= start {
			t.Fatalf("protocol section %q is missing", section.start)
		}
		text := strings.Join(strings.Fields(marshalProtocol[start:end]), " ")
		for _, rule := range section.rules {
			if !strings.Contains(text, rule) {
				t.Errorf("protocol section %q lacks %q", section.start, rule)
			}
		}
	}
}
