package architecturereview

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// An accepted architecture opened read-only refuses every write on every
// route and leaves the saved state and the acceptance callback untouched.
func TestAcceptedReviewRejectsAllWrites(t *testing.T) {
	r := newFixture(t, nil)
	r.config.ReadOnly = true
	accepted := 0
	r.config.OnAccepted = func(json.RawMessage) { accepted++ }
	before, err := os.ReadFile(filepath.Join(r.config.StateDir, "state.json"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	const token = "accepted-fixture-with-a-long-token"
	raw, _ := json.Marshal(requestFixture(t, r, "approve"))
	body := string(raw)
	for _, path := range []string{"/api/review", "/api/builders", "/api/design", "/api/history", "/api/conformance", "/api/codeview"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			q := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
			q.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.Handler(token).ServeHTTP(w, q)
			if w.Code < 400 {
				t.Fatalf("%s %s accepted: %d %s", method, path, w.Code, w.Body)
			}
		}
	}
	if err := applyFixture(t, r, requestFixture(t, r, "approve")); err == nil {
		t.Fatal("Apply accepted a read-only review")
	}
	r.Wait()
	after, _ := os.ReadFile(filepath.Join(r.config.StateDir, "state.json"))
	if string(before) != string(after) || accepted != 0 || stateFixture(t, r)["approved"] != false {
		t.Fatalf("a rejected write changed the accepted review (callbacks %d)", accepted)
	}
}
