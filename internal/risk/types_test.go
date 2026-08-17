package risk

import (
	"errors"
	"testing"
)

func TestRiskContractDefinesClosedLevelsAndStructuredDescriptor(t *testing.T) {
	for _, level := range []Level{LevelLow, LevelMedium, LevelHigh, LevelCritical} {
		if !level.Valid() {
			t.Fatalf("level %q is not valid", level)
		}
	}
	if Level("unknown").Valid() {
		t.Fatal("unknown risk level accepted")
	}

	descriptor := ToolDescriptor{
		Tool:     "git",
		Action:   "push",
		Resource: "repo:marshal",
		Factors: Factors{
			ExternalWrite: true,
		},
	}
	if err := descriptor.Validate(); err != nil {
		t.Fatalf("valid descriptor rejected: %v", err)
	}
}

func TestRiskErrorsExposeStableSafeCodes(t *testing.T) {
	if !errors.Is(ErrDescriptorInvalid, NewError(CodeDescriptorInvalid, "secret-marker")) {
		t.Fatal("descriptor error does not preserve stable code")
	}
	if got := ErrUnknownMutation.Error(); got != "risk assessment rejected an unknown mutating action" {
		t.Fatalf("unsafe or unstable error message: %q", got)
	}
}

func TestAssessmentRejectsInvalidContractValues(t *testing.T) {
	assessment := Assessment{ID: "assessment-1", Level: Level("unknown"), Score: -1}
	if err := assessment.Validate(); !errors.Is(err, ErrDescriptorInvalid) {
		t.Fatalf("invalid assessment error = %v, want ErrDescriptorInvalid", err)
	}
}

func TestDescriptorRejectsControlAndUnknownClaimedLevel(t *testing.T) {
	base := ToolDescriptor{Tool: "shell", Action: "execute", Resource: "repo:marshal"}
	for name, descriptor := range map[string]ToolDescriptor{
		"control":       {Tool: "shell\n", Action: base.Action, Resource: base.Resource},
		"unknown-level": {Tool: base.Tool, Action: base.Action, Resource: base.Resource, ClaimedLevel: "critical-plus"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := descriptor.Validate(); !errors.Is(err, ErrDescriptorInvalid) {
				t.Fatalf("Validate() error = %v, want ErrDescriptorInvalid", err)
			}
		})
	}
}
