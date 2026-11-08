package architecturereview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoredArchitectureContextNeverAcceptsForgedApproval(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"project mismatch", func(s map[string]any) { s["project"] = "other" }},
		{"protocol mismatch", func(s map[string]any) { s["protocol_version"] = "other" }},
		{"bool approval", func(s map[string]any) { s["approved"] = 1 }},
		{"bool revision", func(s map[string]any) { s["revision"] = true }},
		{"digest mismatch", func(s map[string]any) { s["digest"] = "old" }},
		{"approval mismatch", func(s map[string]any) { s["approval"].(map[string]any)["digest"] = "old" }},
		{"missing drawing binding", func(s map[string]any) {
			delete(s, "layout_digest")
			delete(s["approval"].(map[string]any), "layout_digest")
		}},
		{"wrong view version", func(s map[string]any) { s["approval"].(map[string]any)["view_version"] = "invented" }},
		{"unresolved comment", func(s map[string]any) { s["comments"] = []any{map[string]any{"resolved": false}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newFixture(t, nil)
			if err := applyFixture(t, r, requestFixture(t, r, "approve")); err != nil {
				t.Fatal(err)
			}
			s := stateFixture(t, r)
			tc.mutate(s)
			raw, _ := json.Marshal(s)
			if _, err := ValidateStoredState(raw, "fixture"); err == nil {
				t.Fatal("malformed acceptance was credited")
			}
		})
	}
	r := newFixture(t, nil)
	packet, err := ValidateStoredState([]byte(mustState(r)), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	var draft map[string]any
	_ = json.Unmarshal(packet, &draft)
	if draft["status"] != "draft" {
		t.Fatal("unapproved architecture was credited")
	}
	if _, exists := draft["comments"]; exists {
		t.Fatal("private feedback leaked into KB context")
	}
}

func TestReviewRejectsHostileModelResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unsupported status", func(r map[string]any) { r["status"] = "approved" }},
		{"missing receipt", func(r map[string]any) { delete(r, "session_id") }},
		{"fake provider", func(r map[string]any) { r["provider"] = "invented" }},
		{"blank reply", func(r map[string]any) { r["reply"] = " " }},
		{"blank question", func(r map[string]any) { r["status"] = "needs_clarification"; r["questions"] = []any{" "} }},
		{"questions on change", func(r map[string]any) { r["questions"] = []any{"Guess what?"} }},
		{"no actual change", func(r map[string]any) {
			r["architecture"].(map[string]any)["components"].([]any)[0].(map[string]any)["name"] = "Capture"
		}},
		{"unselected object", func(r map[string]any) {
			r["architecture"].(map[string]any)["components"].([]any)[1].(map[string]any)["technology"] = "Invented"
		}},
		{"new assumption", func(r map[string]any) { r["architecture"].(map[string]any)["assumptions"] = []any{"An invented fact"} }},
		{"object reordering", func(r map[string]any) {
			a := r["architecture"].(map[string]any)["components"].([]any)
			a[0], a[1] = a[1], a[0]
		}},
		{"new project field", func(r map[string]any) { r["architecture"].(map[string]any)["approved"] = true }},
		{"endpoint rebound", func(r map[string]any) {
			r["architecture"].(map[string]any)["connections"].([]any)[0].(map[string]any)["target"] = "capture"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newFixture(t, func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
				result, _ := decodeObject(resultFixture(e, "proposed_change", true))
				tc.mutate(result)
				return canonicalJSON(result)
			})
			before := stateFixture(t, r)["digest"]
			q := requestFixture(t, r, "submit")
			q["request_type"] = "change"
			q["comment"] = "Rename selected node only"
			if err := applyFixture(t, r, q); err != nil {
				t.Fatal(err)
			}
			s := waitFixture(t, r, "failed")
			if s["digest"] != before || s["revision"] != float64(1) {
				t.Fatal("hostile model output changed architecture")
			}
		})
	}
}

func TestReviewWithdrawalPreservesHistoryAndUnblocksApproval(t *testing.T) {
	r := newFixture(t, func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("actual adapter failure")
	})
	if err := applyFixture(t, r, requestFixture(t, r, "submit")); err != nil {
		t.Fatal(err)
	}
	s := waitFixture(t, r, "failed")
	id := s["comments"].([]any)[0].(map[string]any)["id"]
	q := requestFixture(t, r, "withdraw")
	delete(q, "target")
	delete(q, "request_type")
	delete(q, "comment")
	q["request_id"] = id
	q["reason"] = "Operator no longer requests this change"
	if err := applyFixture(t, r, q); err != nil {
		t.Fatal(err)
	}
	s = stateFixture(t, r)
	entry := s["comments"].([]any)[0].(map[string]any)
	if entry["resolved"] != true || entry["withdrawn"] != true || entry["closed_by"] != "browser_operator" {
		t.Fatal("withdrawal discarded or falsified history")
	}
	if err := applyFixture(t, r, requestFixture(t, r, "approve")); err != nil {
		t.Fatal(err)
	}
}

func TestReviewDoesNotOrphanUnresolvedTarget(t *testing.T) {
	count := 0
	r := newFixture(t, func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
		count++
		if count == 1 {
			return resultFixture(e, "needs_clarification", false), nil
		}
		return resultFixture(e, "proposed_change", true), nil
	})
	if err := applyFixture(t, r, requestFixture(t, r, "submit")); err != nil {
		t.Fatal(err)
	}
	waitFixture(t, r, "needs_clarification")
	q := requestFixture(t, r, "submit")
	q["request_type"] = "change"
	q["comment"] = "Rename this node without answering previous clarification"
	if err := applyFixture(t, r, q); err != nil {
		t.Fatal(err)
	}
	s := waitFixture(t, r, "failed")
	if s["revision"] != float64(1) {
		t.Fatal("pending target silently changed")
	}
}

func TestReviewCommitsAreAtomicOnWriteFailure(t *testing.T) {
	for _, failureAt := range []int{2, 3} {
		t.Run(map[int]string{2: "running transition", 3: "proposal commit"}[failureAt], func(t *testing.T) {
			r := newFixture(t, func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
				return resultFixture(e, "proposed_change", true), nil
			})
			writes := 0
			r.writeFile = func(path string, value any) error {
				if filepath.Base(path) == "state.json" {
					writes++
					if writes == failureAt {
						return errors.New("injected state write failure")
					}
				}
				return writePrivate(path, value)
			}
			q := requestFixture(t, r, "submit")
			q["request_type"] = "change"
			q["comment"] = "Rename selected node"
			before := stateFixture(t, r)["digest"]
			if err := applyFixture(t, r, q); err != nil {
				t.Fatal(err)
			}
			s := waitFixture(t, r, "failed")
			if s["digest"] != before || s["revision"] != float64(1) {
				t.Fatal("failed durable write exposed changed architecture")
			}
			raw, err := os.ReadFile(filepath.Join(r.config.StateDir, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			var disk map[string]any
			_ = json.Unmarshal(raw, &disk)
			if disk["digest"] != before || disk["comments"].([]any)[0].(map[string]any)["status"] != "failed" {
				t.Fatal("durable failure history inconsistent")
			}
		})
	}
}

func TestReviewRequestContextDoesNotCancelSubmittedModel(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	r := newFixture(t, func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
		started <- ctx
		<-release
		return resultFixture(e, "answered", false), nil
	})
	request, cancel := context.WithCancel(t.Context())
	q := requestFixture(t, r, "submit")
	raw, _ := json.Marshal(q)
	if _, err := r.Apply(request, raw); err != nil {
		t.Fatal(err)
	}
	model := <-started
	cancel()
	if model.Err() != nil {
		t.Fatal("closed HTTP request canceled durable submitted work")
	}
	if err := applyFixture(t, r, requestFixture(t, r, "submit")); err == nil {
		t.Fatal("parallel request bypassed single-flight")
	}
	close(release)
	waitFixture(t, r, "answered")
}

func TestCanonicalJSONRejectsAmbiguityAndPreservesTypes(t *testing.T) {
	for _, raw := range []string{`{"a":1,"a":2}`, `{"x":NaN}`, `{"a":1} trailing`, strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66)} {
		if _, err := CanonicalDigest([]byte(raw)); err == nil {
			t.Fatalf("ambiguous JSON accepted: %q", raw[:min(len(raw), 40)])
		}
	}
	first, err := CanonicalDigest([]byte(`{"b":"<x>","a":true}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalDigest([]byte(`{ "a":true,"b":"<x>"}`))
	if err != nil || first != second {
		t.Fatal("canonical object ordering is unstable")
	}
	number, _ := CanonicalDigest([]byte(`{"a":1,"b":"<x>"}`))
	if first == number {
		t.Fatal("bool equaled JSON number")
	}
}

func TestHTTPMutationValidation(t *testing.T) {
	r := newFixture(t, nil)
	handler := r.Handler("fixture-token-with-more-than-sixteen-characters")
	for _, tc := range []struct {
		name, method, path, body, host string
		want                           int
	}{
		{"oversized body", http.MethodPost, "/api/review", strings.Repeat("x", 1_000_001), "127.0.0.1", 400},
		{"malformed body", http.MethodPost, "/api/review", "{", "127.0.0.1", 409},
		{"unsupported method", http.MethodDelete, "/api/review", "", "127.0.0.1", 405},
		{"DNS rebinding host", http.MethodGet, "/api/review", "", "attacker.example", 403},
		{"wrong route", http.MethodGet, "/other", "", "127.0.0.1", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, strings.NewReader(tc.body))
			request.Header.Set("Authorization", "Bearer fixture-token-with-more-than-sixteen-characters")
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, request)
			if out.Code != tc.want {
				t.Fatalf("HTTP status %d; want %d", out.Code, tc.want)
			}
		})
	}
}

func TestReviewReloadsExactGeometryWithoutArguments(t *testing.T) {
	r := newFixture(t, nil)
	before := stateFixture(t, r)
	config := r.config
	config.Proposal = nil
	config.Layouts = nil
	reopened, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	after := stateFixture(t, reopened)
	if !equalJSON(before["layouts"], after["layouts"]) || before["layout_digest"] != after["layout_digest"] {
		t.Fatal("reopening rebound the same IDs to different geometry")
	}
	file := filepath.Join(config.StateDir, "layouts", before["layout_file"].(string))
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var layouts map[string]Layout
	if err = json.Unmarshal(data, &layouts); err != nil {
		t.Fatal(err)
	}
	view := layouts["architecture"]
	view.Nodes[0].X += 10
	layouts["architecture"] = view
	if err = writePrivate(file, layouts); err != nil {
		t.Fatal(err)
	}
	if _, err = New(config); err == nil {
		t.Fatal("modified stored geometry was silently reinterpreted")
	}
}

func TestReviewPrivatePersistenceAndProjectBinding(t *testing.T) {
	r := newFixture(t, nil)
	config := r.config
	config.Project = "unrelated"
	if _, err := New(config); err == nil {
		t.Fatal("review was reused for unrelated project")
	}
	config = r.config
	file := filepath.Join(config.StateDir, "state.json")
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(config); err == nil {
		t.Fatal("public review state accepted")
	}
	config.StateDir = filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(r.config.StateDir, config.StateDir); err != nil {
		t.Fatal(err)
	}
	if _, err := New(config); err == nil {
		t.Fatal("symlink state directory accepted")
	}
}

func TestReviewApprovalHookFailureDoesNotApprove(t *testing.T) {
	r := newFixture(t, nil)
	r.config.OnApprove = func(json.RawMessage) error { return errors.New("knowledge acceptance hook failed") }
	if err := applyFixture(t, r, requestFixture(t, r, "approve")); err == nil {
		t.Fatal("failed acceptance hook ignored")
	}
	if stateFixture(t, r)["approved"] != false {
		t.Fatal("failed hook granted human approval")
	}
}

func TestHTTPAuthenticatedStateAndApproval(t *testing.T) {
	r := newFixture(t, nil)
	token := "fixture-http-token-more-than-sixteen"
	handler := r.Handler(token)
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8123/api/review", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, request)
	if out.Code != 200 || !strings.Contains(out.Body.String(), Version) {
		t.Fatal("authenticated state unavailable")
	}
	q := requestFixture(t, r, "approve")
	raw, _ := json.Marshal(q)
	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8123/api/review", strings.NewReader(string(raw)))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Origin", "http://127.0.0.1:8123")
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, request)
	if out.Code != 200 || stateFixture(t, r)["approved"] != true {
		t.Fatalf("authenticated explicit browser approval failed: %d %s", out.Code, out.Body.String())
	}
}

func TestGenericGeometrySupportsSelfLoopAndEmptyEdges(t *testing.T) {
	for _, self := range []bool{false, true} {
		a, _ := decodeObject(proposalFixture())
		if self {
			a["connections"].([]any)[0].(map[string]any)["target"] = "capture"
		} else {
			a["connections"] = []any{}
		}
		raw, _ := canonicalJSON(a)
		views, err := GenericLayouts(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err = validateLayouts(a, views); err != nil {
			t.Fatal(err)
		}
	}
}
