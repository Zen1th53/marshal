package architecturereview

import (
	"encoding/json"
	"testing"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
)

func TestArchitectureModelApprovalIsNativeAndEditionBound(t *testing.T) {
	r := newFixture(t, nil)
	state := stateFixture(t, r)
	revision := int64(state["revision"].(float64))
	called := false
	err := r.AcceptByMarshal(t.Context(), "fixture", "architecture-fixture", revision, state["digest"].(string), "operator-ref", "receipt-ref", func(json.RawMessage) error { called = true; return nil })
	if (err == nil) != architecturecodec.ModelArchitectureApprovalAllowed() || called != architecturecodec.ModelArchitectureApprovalAllowed() {
		t.Fatalf("model approval: err=%v authorize called=%v", err, called)
	}
	// Neither edition exposes the native action as a browser/feedback command.
	request := requestFixture(t, r, "accept-architecture")
	if err := applyFixture(t, r, request); err == nil {
		t.Fatal("native model acceptance exposed through browser Apply")
	}
	if !architecturecodec.ModelArchitectureApprovalAllowed() {
		state["approved"] = true
		state["approval"] = map[string]any{"source": "marshal_operator_command", "model_self_approval": true, "operator_instruction_ref": "operator-ref", "receipt_id": "receipt-ref", "architecture_id": state["architecture_id"], "revision": state["revision"], "digest": state["digest"], "layout_digest": state["layout_digest"], "view_version": "kiss-v1", "language": state["language"], "ui_digest": state["ui_digest"]}
		raw, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateStoredState(raw, "fixture"); err == nil {
			t.Fatal("Community credited model-originated approval JSON")
		}
	}
}
