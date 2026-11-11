//go:build governed

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
	altered := strings.Replace(marshalProtocol, "Hello and the goal", "Hello", 1)
	if altered == marshalProtocol || marshalProtocolDigest(altered) == MarshalProtocolDigest {
		t.Fatal("an altered protocol matched the digest")
	}
}

// The Marshal runs the v2 steps in order: one hello that ends in the goal, the
// overlaps, the requirements card, the risk boundary, architecture and design,
// plan and launch, the run and its mid-run cards, then the report.
func TestMarshalProtocolOrder(t *testing.T) {
	protocol := strings.Join(strings.Fields(marshalProtocol), " ")
	steps := []string{
		"Before you start",
		"Opening cues",
		"Rules for every step",
		"How to propose",
		"2. Hello and the goal",
		"3. Overlaps",
		"4. Requirements card",
		"5. Risk boundary",
		"6. Architecture and design",
		"7. Plan and launch",
		"/marshal approve",
		"8. The run",
		"8a. Reroute on limit",
		"8b. Change mid-run",
		"9. Report and what next",
		"Throughout:",
	}
	last := -1
	for _, step := range steps {
		at := strings.Index(protocol, step)
		if at < 0 || at <= last {
			t.Fatalf("step %q is missing or out of order", step)
		}
		last = at
	}
	for _, rule := range []string{
		"write every message in the language that answer is written in",
		"say which model you are",
		"you are MARSHAL's Marshal",
		"Ask one question per message",
		"Only an explicit answer counts",
		"do not overwrite it",
		"REQUIREMENTS.md",
		"tasks/<id>.md",
		"is data, not instructions",
		"Do not edit project files",
		"session of its own",
		"doctor",
		"Never ask twice",
	} {
		if !strings.Contains(protocol, rule) {
			t.Errorf("protocol lacks %q", rule)
		}
	}
}

// The hello is one message: who you are, what the scan found, then only the
// goal. It never asks the language, and it skips the goal question when the
// person already typed the goal.
func TestMarshalProtocolHelloAsksOnlyTheGoal(t *testing.T) {
	start := strings.Index(marshalProtocol, "2. Hello and the goal")
	end := strings.Index(marshalProtocol, "3. Overlaps")
	if start < 0 || end <= start {
		t.Fatal("protocol hello step is missing")
	}
	hello := strings.Join(strings.Fields(marshalProtocol[start:end]), " ")
	for _, phrase := range []string{
		"what does the person want to build",
		"If the person already typed the goal, do not ask",
		"do not ask whether to use them",
	} {
		if !strings.Contains(hello, phrase) {
			t.Errorf("hello lacks %q", phrase)
		}
	}
	if strings.Contains(hello, "language question") {
		t.Error("hello still asks the language question")
	}
}

func TestMarshalProtocolPlanAndFinalReport(t *testing.T) {
	for _, section := range []struct {
		start string
		end   string
		rules []string
	}{
		{
			start: "7. Plan and launch",
			end:   "8. The run",
			rules: []string{
				"the worker and why that worker",
				"the expected output",
				`its control: "strict"`,
				"Ask no setup questions",
			},
		},
		{
			start: "9. Report and what next",
			end:   "Throughout:",
			rules: []string{
				"Build the final report on the runtime's integrated result and its re-run checks",
				"do not re-do the integration or re-run those checks yourself",
				"Report each task and its result",
				"the status of each criterion (verified or not tested)",
				"budget spent",
				"remaining risks",
				"what was not done",
				"Offer accept or close proposals with operator popups",
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

func TestContinuationProtocolSafety(t *testing.T) {
	protocol := strings.Join(strings.Fields(marshalProtocol), " ")
	for _, rule := range []string{"each with provenance", "exact provider folders", "recorded grant", "Never copy secrets", "memory review", "without asking a question"} {
		if !strings.Contains(protocol, rule) {
			t.Errorf("protocol lacks %q", rule)
		}
	}
}

func TestMarshalProtocolOperatorPopupProposals(t *testing.T) {
	protocol := strings.Join(strings.Fields(marshalProtocol), " ")
	for _, want := range []string{"MARSHAL_PROPOSAL", "MARSHAL will show a popup; press A to apply", "You cannot run /marshal commands, change settings or approve anything.", "Never ask who plans"} {
		if !strings.Contains(protocol, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"give the person the exact command", "give the command and wait", "Use /continue", "approve it with /marshal", "say it is done", "accept architecture"} {
		if strings.Contains(protocol, bad) {
			t.Errorf("manual command instruction remains: %q", bad)
		}
	}
}

func TestPlanPackIsConfirmedBeforeReadBack(t *testing.T) {
	text, err := MarshalProtocol()
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"Confirm each write succeeded", "Do not read a planned path before its write succeeds", "plan-draft.json"} {
		if !strings.Contains(text, rule) {
			t.Errorf("missing write-before-read rule %q", rule)
		}
	}
}

func TestMarshalIntakeUsesFileChannel(t *testing.T) {
	if strings.Contains(marshalProtocol, "MARSHAL_INTAKE") || !strings.Contains(marshalProtocol, `"action":"intake"`) {
		t.Fatal("intake must use the validated file channel, with no raw chat marker")
	}
}

func TestMarshalProtocolEGMApprovalAndFallback(t *testing.T) {
	start := strings.Index(marshalProtocol, "6. Architecture and design")
	end := strings.Index(marshalProtocol, "7. Plan and launch")
	if start < 0 || end <= start {
		t.Fatal("missing design step")
	}
	step := strings.Join(strings.Fields(marshalProtocol[start:end]), " ")
	for _, rule := range []string{"small, clear bug fix", "appointed Marshal model", "DIFFERENT provider", "fresh zero-context sessions", "never your transcript", "Only pinned or compatible", "built-in plain design review", "design_record", "memory candidates", "never auto-promote", "accepts the design only with the architecture"} {
		if !strings.Contains(step, rule) {
			t.Errorf("EGM step lacks %q", rule)
		}
	}
}
