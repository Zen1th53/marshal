package risk

import (
	"errors"
	"testing"
)

func TestDescriptorRejectsSecretMarkerAndTraversalResource(t *testing.T) {
	base := ToolDescriptor{Tool: "shell", Action: "execute", Resource: "repo:marshal"}
	for name, descriptor := range map[string]ToolDescriptor{
		"secret-marker": {Tool: base.Tool, Action: base.Action, Resource: "MARSHAL_TEST_SECRET_T24_A07_UNIQUE"},
		"traversal":     {Tool: base.Tool, Action: base.Action, Resource: "repo:../outside"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := descriptor.Validate(); !errors.Is(err, ErrDescriptorInvalid) {
				t.Fatalf("Validate() error = %v, want ErrDescriptorInvalid", err)
			}
		})
	}
}

func FuzzDescriptorValidationNeverPanics(f *testing.F) {
	f.Add("git", "push", "repo:marshal")
	f.Fuzz(func(t *testing.T, tool, action, resource string) {
		_ = (ToolDescriptor{Tool: tool, Action: action, Resource: resource}).Validate()
	})
}
