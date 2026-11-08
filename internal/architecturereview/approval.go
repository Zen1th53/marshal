package architecturereview

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
)

// AcceptByMarshal is a native runtime operation, never an HTTP action. The
// runtime must durably check the recorded operator instruction in authorize.
func (r *Review) AcceptByMarshal(ctx context.Context, project, id string, revision int64, digest, instructionRef, receiptID string, authorize func(json.RawMessage) error) error {
	if !architecturecodec.ModelArchitectureApprovalAllowed() || authorize == nil || instructionRef == "" || receiptID == "" {
		return errors.New("architecture acceptance requires the person's browser click")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, _ := integer(r.state["revision"])
	if r.state["project"] != project || r.state["architecture_id"] != id || current != revision || r.state["digest"] != digest {
		return errors.New("architecture acceptance binding is stale or foreign")
	}
	for _, value := range r.state["comments"].([]any) {
		if value.(map[string]any)["resolved"] != true {
			return errors.New("unresolved feedback prevents approval")
		}
	}
	next, err := cloneObject(r.state)
	if err != nil {
		return err
	}
	next["approved"] = true
	next["approval"] = map[string]any{"source": "marshal_operator_command", "model_self_approval": true, "operator_instruction_ref": instructionRef, "receipt_id": receiptID, "architecture_id": id, "revision": revision, "digest": digest, "view_version": "kiss-v1", "layout_digest": r.layoutDigest, "language": next["language"], "ui_digest": next["ui_digest"]}
	raw, err := canonicalJSON(next)
	if err != nil {
		return err
	}
	if err = authorize(raw); err != nil {
		return err
	}
	return r.commit(next)
}
