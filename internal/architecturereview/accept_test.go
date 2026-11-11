package architecturereview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcceptBlockedReasonRefusesAndIsPublished(t *testing.T) {
	r := newFixture(t, nil)
	const reason = "The Marshal is still preparing the project summary."
	r.config.AcceptBlocked = func() string { return reason }
	if got := stateFixture(t, r)["accept_blocked"]; got != reason {
		t.Fatalf("published accept_blocked = %v", got)
	}
	err := applyFixture(t, r, requestFixture(t, r, "approve"))
	if err == nil || err.Error() != reason {
		t.Fatalf("blocked Accept: %v", err)
	}
	if stateFixture(t, r)["approved"] != false {
		t.Fatal("blocked Accept approved")
	}
	raw, err := os.ReadFile(filepath.Join(r.config.StateDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "accept_blocked") {
		t.Fatalf("accept_blocked persisted: %s", raw)
	}
	r.config.AcceptBlocked = func() string { return "" }
	if _, present := stateFixture(t, r)["accept_blocked"]; present {
		t.Fatal("empty reason published")
	}
	if err := applyFixture(t, r, requestFixture(t, r, "approve")); err != nil {
		t.Fatal(err)
	}
}

func TestOnAcceptedRunsAfterCommit(t *testing.T) {
	r := newFixture(t, nil)
	calls := make(chan map[string]any, 2)
	r.config.OnAccepted = func(raw json.RawMessage) {
		disk, err := os.ReadFile(filepath.Join(r.config.StateDir, "state.json"))
		var saved, got map[string]any
		if err != nil || json.Unmarshal(disk, &saved) != nil || json.Unmarshal(raw, &got) != nil {
			t.Error("accepted state unreadable")
			calls <- nil
			return
		}
		got["saved"] = saved["approved"]
		calls <- got
	}
	if err := applyFixture(t, r, requestFixture(t, r, "approve")); err != nil {
		t.Fatal(err)
	}
	r.Wait()
	select {
	case got := <-calls:
		if got["approved"] != true || got["saved"] != true {
			t.Fatalf("OnAccepted state: approved=%v saved=%v", got["approved"], got["saved"])
		}
	default:
		t.Fatal("Wait returned before OnAccepted ran")
	}
	if len(calls) != 0 {
		t.Fatal("OnAccepted ran more than once")
	}
}

func TestLegacyChatAcceptanceLoadsAsDraft(t *testing.T) {
	r := newFixture(t, nil)
	state := stateFixture(t, r)
	delete(state, "layouts")
	state["approved"] = true
	state["approval"] = map[string]any{"source": "marshal_operator_command", "model_self_approval": true, "operator_instruction_ref": "operator-ref", "receipt_id": "receipt-ref", "architecture_id": state["architecture_id"], "revision": state["revision"], "digest": state["digest"], "layout_digest": state["layout_digest"], "view_version": "kiss-v1", "language": state["language"], "ui_digest": state["ui_digest"]}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(r.config.StateDir, "state.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	packet, err := ValidateStoredState(raw, "fixture")
	if err != nil {
		t.Fatalf("legacy chat acceptance is an error: %v", err)
	}
	var validated struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(packet, &validated); err != nil || validated.Status != "draft" {
		t.Fatalf("legacy chat acceptance status = %q (%v)", validated.Status, err)
	}
	reloaded, err := New(r.config)
	if err != nil {
		t.Fatal(err)
	}
	if got := stateFixture(t, reloaded); got["approved"] != false || got["approval"] != nil {
		t.Fatalf("legacy chat acceptance loaded as accepted: %v %v", got["approved"], got["approval"])
	}
	if err := applyFixture(t, reloaded, requestFixture(t, reloaded, "approve")); err != nil {
		t.Fatalf("owner cannot Accept again: %v", err)
	}
}
