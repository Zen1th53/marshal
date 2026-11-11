package architecturereview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func proposalFixture() json.RawMessage {
	return json.RawMessage(`{"title":"Traffic monitor","purpose":"Measure metadata","summary":"A draft","components":[{"id":"capture","name":"Capture","responsibility":"Observe","technology":"Go","files":[],"evidence":[]},{"id":"store","name":"Store","responsibility":"Persist","technology":"SQLite","files":[],"evidence":[]}],"connections":[{"id":"counts","source":"capture","target":"store","label":"Counters","protocol":"Unix socket"}],"constraints":["No payload"],"assumptions":[],"workers":[]}`)
}

func newFixture(t *testing.T, feedback func(context.Context, json.RawMessage) (json.RawMessage, error)) *Review {
	t.Helper()
	view, err := GenericLayouts(proposalFixture())
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Config{Context: t.Context(), Project: "fixture", ArchitectureID: "architecture-fixture", StateDir: filepath.Join(t.TempDir(), "state"), Proposal: proposalFixture(), Layouts: view, Feedback: feedback})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func stateFixture(t *testing.T, r *Review) map[string]any {
	t.Helper()
	raw, err := r.State()
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func requestFixture(t *testing.T, r *Review, action string) map[string]any {
	t.Helper()
	state := stateFixture(t, r)
	layouts := state["layouts"].(map[string]any)
	var diagram string
	for id := range layouts {
		diagram = id
		break
	}
	return map[string]any{"protocol_version": Version, "action": action, "revision": state["revision"], "digest": state["digest"], "ui_digest": state["ui_digest"], "language": state["language"], "builders_digest": state["builders_digest"], "target": map[string]any{"kind": "component", "id": "capture", "diagram_id": diagram, "layout_version": layouts[diagram].(map[string]any)["version"]}, "request_type": "comment", "comment": "Explain this capture component"}
}

func applyFixture(t *testing.T, r *Review, request map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Apply(t.Context(), raw)
	return err
}

func resultFixture(envelope json.RawMessage, status string, change bool) json.RawMessage {
	var e map[string]any
	_ = json.Unmarshal(envelope, &e)
	architecture := e["architecture"].(map[string]any)
	if change {
		architecture["components"].([]any)[0].(map[string]any)["name"] = "Packet observer"
	}
	questions := []string{}
	if status == "needs_clarification" {
		questions = []string{"Which interface?"}
	}
	raw, _ := json.Marshal(map[string]any{"status": status, "reply": "An exact answer", "questions": questions, "architecture": architecture, "provider": "codex", "session_id": "fixture-test-only"})
	return raw
}

func waitFixture(t *testing.T, r *Review, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := stateFixture(t, r)
		comments := s["comments"].([]any)
		if len(comments) > 0 && comments[len(comments)-1].(map[string]any)["status"] == want {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("status %q never reached: %s", want, mustState(r))
	return nil
}
func mustState(r *Review) string { raw, _ := r.State(); return string(raw) }

func TestReviewExactSelectionAndApproval(t *testing.T) {
	envelopes := make(chan json.RawMessage, 1)
	r := newFixture(t, func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
		envelopes <- e
		return resultFixture(e, "answered", false), nil
	})
	if err := applyFixture(t, r, requestFixture(t, r, "submit")); err != nil {
		t.Fatal(err)
	}
	s := waitFixture(t, r, "answered")
	var envelope map[string]any
	_ = json.Unmarshal(<-envelopes, &envelope)
	selection := envelope["selection"].(map[string]any)
	if len(selection["components"].([]any)) != 1 || selection["components"].([]any)[0].(map[string]any)["id"] != "capture" {
		t.Fatal("selection lost exact component identity")
	}
	if s["approved"] != false {
		t.Fatal("model reply approved architecture")
	}
	if err := applyFixture(t, r, requestFixture(t, r, "approve")); err != nil {
		t.Fatal(err)
	}
	if stateFixture(t, r)["approved"] != true {
		t.Fatal("explicit browser approval was not persisted")
	}
}

func TestReviewRejectsMalformedInputBeforeModel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"wrong protocol", func(q map[string]any) { q["protocol_version"] = "other" }},
		{"stale digest", func(q map[string]any) { q["digest"] = "old" }},
		{"bool revision", func(q map[string]any) { q["revision"] = true }},
		{"empty text", func(q map[string]any) { q["comment"] = " " }},
		{"unknown target", func(q map[string]any) { q["target"].(map[string]any)["id"] = "other" }},
		{"direct edit", func(q map[string]any) { q["action"] = "edit" }},
		{"server field", func(q map[string]any) { q["approved"] = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := make(chan bool, 1)
			r := newFixture(t, func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
				called <- true
				return resultFixture(e, "answered", false), nil
			})
			q := requestFixture(t, r, "submit")
			tc.mutate(q)
			if applyFixture(t, r, q) == nil {
				t.Fatal("malformed request accepted")
			}
			select {
			case <-called:
				t.Fatal("bad request invoked model")
			default:
			}
		})
	}
	r := newFixture(t, nil)
	if _, err := r.Apply(t.Context(), json.RawMessage(`{"protocol_version":"architecture-review.v1","protocol_version":"other"}`)); err == nil {
		t.Fatal("duplicate JSON keys accepted")
	}
}

func TestReviewAreaSelectionIsComputedByServer(t *testing.T) {
	r := newFixture(t, func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
		return resultFixture(e, "answered", false), nil
	})
	q := requestFixture(t, r, "submit")
	target := q["target"].(map[string]any)
	delete(target, "id")
	target["kind"] = "area"
	target["rect"] = map[string]any{"x": 70, "y": 70, "width": 200, "height": 110}
	target["component_ids"] = []string{"store"}
	if applyFixture(t, r, q) == nil {
		t.Fatal("forged area IDs accepted")
	}
	delete(target, "component_ids")
	if err := applyFixture(t, r, q); err != nil {
		t.Fatal(err)
	}
	s := waitFixture(t, r, "answered")
	entry := s["comments"].([]any)[0].(map[string]any)
	bound := entry["target"].(map[string]any)
	if bound["component_ids"].([]any)[0] != "capture" {
		t.Fatal("area selected wrong component")
	}
}

func TestReviewChangeRevokesApprovalAndPreservesScope(t *testing.T) {
	r := newFixture(t, func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
		return resultFixture(e, "proposed_change", true), nil
	})
	if err := applyFixture(t, r, requestFixture(t, r, "approve")); err != nil {
		t.Fatal(err)
	}
	q := requestFixture(t, r, "submit")
	q["request_type"] = "change"
	q["comment"] = "Rename selected Capture to Packet observer only"
	if err := applyFixture(t, r, q); err != nil {
		t.Fatal(err)
	}
	s := waitFixture(t, r, "proposed_change")
	if s["approved"] != false || s["revision"] != float64(2) {
		t.Fatalf("change did not invalidate exact approval: %s", mustState(r))
	}
	components := s["architecture"].(map[string]any)["components"].([]any)
	if components[0].(map[string]any)["name"] != "Packet observer" || components[1].(map[string]any)["name"] != "Store" {
		t.Fatal("scope changed")
	}
}

func TestReviewClarificationFollowupDoesNotOrphanRequests(t *testing.T) {
	count := 0
	r := newFixture(t, func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
		count++
		if count == 1 {
			return resultFixture(e, "needs_clarification", false), nil
		}
		return resultFixture(e, "answered", false), nil
	})
	q := requestFixture(t, r, "submit")
	if err := applyFixture(t, r, q); err != nil {
		t.Fatal(err)
	}
	s := waitFixture(t, r, "needs_clarification")
	prior := s["comments"].([]any)[0].(map[string]any)
	if err := applyFixture(t, r, requestFixture(t, r, "approve")); err == nil {
		t.Fatal("open clarification was approved")
	}
	q = requestFixture(t, r, "submit")
	q["followup_to"] = prior["id"]
	q["comment"] = "Interface eno1"
	if err := applyFixture(t, r, q); err != nil {
		t.Fatal(err)
	}
	s = waitFixture(t, r, "answered")
	for _, c := range s["comments"].([]any) {
		if c.(map[string]any)["resolved"] != true {
			t.Fatal("followup stranded an unresolved ancestor")
		}
	}
}

func TestReviewPersistsPrivateStateAndRecoversInterruptedRequest(t *testing.T) {
	r := newFixture(t, nil)
	state := stateFixture(t, r)
	state["comments"] = []any{map[string]any{"id": "000000000000000000000001", "status": "running", "resolved": false, "target": map[string]any{}}}
	delete(state, "layouts")
	body, _ := json.Marshal(state)
	file := filepath.Join(r.config.StateDir, "state.json")
	if err := os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(r.config)
	if err != nil {
		t.Fatal(err)
	}
	s := stateFixture(t, reopened)
	entry := s["comments"].([]any)[0].(map[string]any)
	if entry["status"] != "failed" || entry["resolved"] != false {
		t.Fatal("restart credited unfinished work")
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("state permissions are not private: %v %v", info, err)
	}
}

func TestReviewHTTPRequiresTokenAndSameOrigin(t *testing.T) {
	r := newFixture(t, nil)
	handler := r.Handler("test-token-that-is-only-used-in-fixtures")
	q := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/review", nil)
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, q)
	if out.Code != http.StatusForbidden {
		t.Fatal("unauthenticated state exposed")
	}
	q = httptest.NewRequest(http.MethodGet, "http://127.0.0.1/?token=test-token-that-is-only-used-in-fixtures", nil)
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, q)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "<svg") {
		t.Fatal("embedded schematic was unavailable")
	}
	body, _ := json.Marshal(requestFixture(t, r, "approve"))
	q = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/review", strings.NewReader(string(body)))
	q.Header.Set("Authorization", "Bearer test-token-that-is-only-used-in-fixtures")
	q.Header.Set("Origin", "https://other.example")
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, q)
	if out.Code != http.StatusForbidden {
		t.Fatal("foreign origin could mutate review")
	}
}

func TestGenericLayoutsNeverInventsEndpoints(t *testing.T) {
	view, err := GenericLayouts(proposalFixture())
	if err != nil {
		t.Fatal(err)
	}
	if len(view["architecture"].Nodes) != 2 || len(view["architecture"].Edges) != 1 {
		t.Fatal("actual topology was not preserved")
	}
	bad := strings.Replace(string(proposalFixture()), `"target":"store"`, `"target":"invented"`, 1)
	if _, err := GenericLayouts(json.RawMessage(bad)); err == nil {
		t.Fatal("unknown endpoint guessed instead of rejected")
	}
}
