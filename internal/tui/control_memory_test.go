package tui

import (
	"testing"
)

func TestMemoryActionFormsUseCanonicalServices(t *testing.T) {
	source := &ControlSource{}
	bindings := memoryActionBindings(source)
	for _, id := range []ActionID{"CTUI-0438", "CTUI-0450", "CTUI-0484", "CTUI-0485", "CTUI-0486", "CTUI-0489", "CTUI-0491", "CTUI-0071"} {
		b, ok := bindings[id]
		if !ok {
			t.Fatalf("missing form %s", id)
		}
		if b.Bound() || b.Requires == "" {
			t.Fatalf("missing extension authority did not fail closed: %s", id)
		}
	}
}

func TestMemoryFormsRequireTypedSelection(t *testing.T) {
	b := memoryPromotionBinding(&ControlSource{})
	form := newActionForm(b, ActionRequest{Action: b.Action})
	if _, err := form.build(); err == nil {
		t.Fatal("empty memory selection accepted")
	}
	if b.RequiresApproval {
		t.Fatal("operator promotion incorrectly requires unrelated runtime approval")
	}
}

func TestVerificationFormsFailClosedWithoutAuthority(t *testing.T) {
	for id, b := range verificationActionBindings(&ControlSource{}) {
		if b.Bound() || b.Requires == "" {
			t.Fatalf("missing optional verification authority executable: %s", id)
		}
	}
}
