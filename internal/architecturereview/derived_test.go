package architecturereview

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// derivedFixture is a review whose part "svc" has no sub-parts, so its inside
// is derived from the code in codeRepo.
func derivedFixture(t *testing.T, feedback func(context.Context, json.RawMessage) (json.RawMessage, error)) (*Review, string) {
	t.Helper()
	root := codeRepo(t)
	raw := json.RawMessage(`{"title":"T","purpose":"P","components":[{"id":"svc","name":"svc","responsibility":"r","technology":"","files":["svc"]},{"id":"other","name":"other","responsibility":"r","technology":"","files":[]}],"connections":[{"id":"c","source":"svc","target":"other","label":"l","protocol":""}]}`)
	state := filepath.Join(t.TempDir(), "state")
	r, err := New(Config{Context: t.Context(), Project: "fixture", ArchitectureID: "architecture-fixture", StateDir: state, Proposal: raw, CodeRoot: root, Feedback: feedback})
	if err != nil {
		t.Fatal(err)
	}
	return r, state
}

func derivedRequest(t *testing.T, r *Review, part string, derived map[string]any) map[string]any {
	t.Helper()
	s := stateFixture(t, r)
	request := map[string]any{"protocol_version": Version, "action": "submit", "revision": s["revision"], "digest": s["digest"], "ui_digest": s["ui_digest"], "language": s["language"], "request_type": "comment", "comment": "What does this do?", "derived": derived}
	for id, value := range s["layouts"].(map[string]any) {
		l := value.(map[string]any)
		for _, n := range l["nodes"].([]any) {
			if n.(map[string]any)["id"] == part {
				request["target"] = map[string]any{"kind": "component", "id": part, "diagram_id": id, "layout_version": l["version"]}
			}
		}
	}
	return request
}

func answering(envelopes chan<- json.RawMessage, change bool) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(_ context.Context, e json.RawMessage) (json.RawMessage, error) {
		envelopes <- e
		if change {
			return resultFixture(e, "proposed_change", true), nil
		}
		return resultFixture(e, "answered", false), nil
	}
}

// A request about a derived declaration carries its exact path, kind and
// breadcrumb to the Marshal, and leaves the reviewed contract and Accept alone.
func TestDerivedRequestCarriesPreciseTargetAndKeepsAccept(t *testing.T) {
	envelopes := make(chan json.RawMessage, 4)
	r, state := derivedFixture(t, answering(envelopes, false))
	if err := applyFixture(t, r, requestFixtureFor(t, r, "approve")); err != nil {
		t.Fatal(err)
	}
	before := stateFixture(t, r)
	request := derivedRequest(t, r, "svc", map[string]any{"part": "svc", "dir": "svc/api", "items": []any{map[string]any{"path": "svc/api/api.go"}}, "name": "Serve"})
	if err := applyFixture(t, r, request); err != nil {
		t.Fatal(err)
	}
	after := waitFixture(t, r, "answered")
	if after["approved"] != true || after["approval"] == nil || after["digest"] != before["digest"] || !equalJSON(after["architecture"], before["architecture"]) {
		t.Fatal("a derived request changed the contract or the acceptance")
	}
	var envelope map[string]any
	_ = json.Unmarshal(<-envelopes, &envelope)
	derived := envelope["derived"].(map[string]any)
	element := derived["elements"].([]any)[0].(map[string]any)
	if element["kind"] != "declaration" || element["path"] != "svc/api/api.go" || element["name"] != "Serve" || derived["note"] != derivedNote {
		t.Fatalf("derived target = %v", derived)
	}
	if got := strings.Join(toStrings(derived["breadcrumb"]), " › "); got != "svc › svc/api › api.go › Serve" {
		t.Fatalf("breadcrumb = %q", got)
	}
	// The Marshal-facing request record names the path and kind.
	entry := after["comments"].([]any)[0].(map[string]any)
	stored, err := os.ReadFile(filepath.Join(state, "requests", entry["id"].(string)+".json"))
	if err != nil || !strings.Contains(string(stored), `"path":"svc/api/api.go"`) || !strings.Contains(string(stored), `"kind":"declaration"`) {
		t.Fatalf("request record lacks the derived target: %v %s", err, stored)
	}
	if entry["derived"] == nil {
		t.Fatal("the feedback entry must keep its derived target")
	}
	if _, err := ValidateStoredState([]byte(mustState(r)), "fixture"); err != nil {
		t.Fatalf("accepted state with a derived request is invalid: %v", err)
	}
}

// Area selection on a file level names several files and the link between them.
func TestDerivedAreaSelectionAndPackageLevel(t *testing.T) {
	envelopes := make(chan json.RawMessage, 4)
	r, _ := derivedFixture(t, answering(envelopes, false))
	items := []any{map[string]any{"path": "svc/api/api.go"}, map[string]any{"path": "svc/api/item.go"}, map[string]any{"path": "svc/api/api.go", "to": "svc/api/item.go"}}
	if err := applyFixture(t, r, derivedRequest(t, r, "svc", map[string]any{"part": "svc", "dir": "svc/api", "items": items})); err != nil {
		t.Fatal(err)
	}
	waitFixture(t, r, "answered")
	var envelope map[string]any
	_ = json.Unmarshal(<-envelopes, &envelope)
	kinds := []string{}
	for _, e := range envelope["derived"].(map[string]any)["elements"].([]any) {
		kinds = append(kinds, e.(map[string]any)["kind"].(string))
	}
	if strings.Join(kinds, ",") != "file,file,connection" {
		t.Fatalf("kinds = %v", kinds)
	}
	if err := applyFixture(t, r, derivedRequest(t, r, "svc", map[string]any{"part": "svc", "items": []any{map[string]any{"path": "svc/store"}}})); err != nil {
		t.Fatal(err)
	}
	waitFixture(t, r, "answered")
	_ = json.Unmarshal(<-envelopes, &envelope)
	if e := envelope["derived"].(map[string]any)["elements"].([]any)[0].(map[string]any); e["kind"] != "package" || e["path"] != "svc/store" {
		t.Fatalf("package element = %v", e)
	}
}

// Paths outside the project, absolute, oversize or with control characters,
// elements not on the level and undeclared names are refused before the model.
func TestDerivedRequestRefusesHostileTargets(t *testing.T) {
	called := false
	r, _ := derivedFixture(t, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		called = true
		return nil, os.ErrInvalid
	})
	if err := os.Symlink("/etc", filepath.Join(r.config.CodeRoot, "svc", "api", "link")); err != nil {
		t.Fatal(err)
	}
	before := mustState(r)
	file := func(p string) []any { return []any{map[string]any{"path": p}} }
	cases := map[string]map[string]any{
		"escape":         {"part": "svc", "dir": "svc/api", "items": file("../../etc/passwd")},
		"dot escape":     {"part": "svc", "dir": "svc/api", "items": file("svc/api/../../../etc/passwd")},
		"absolute":       {"part": "svc", "dir": "svc/api", "items": file("/etc/passwd")},
		"control":        {"part": "svc", "dir": "svc/api", "items": file("svc/api/api.go\n")},
		"newline inside": {"part": "svc", "dir": "svc/api", "items": file("svc/api/a\npi.go")},
		"oversize":       {"part": "svc", "dir": "svc/api", "items": file(strings.Repeat("a/", 300) + "x.go")},
		"not on level":   {"part": "svc", "dir": "svc/api", "items": file("svc/store/store.go")},
		"symlink":        {"part": "svc", "dir": "svc/api", "items": file("svc/api/link")},
		"hidden":         {"part": "svc", "dir": "svc/api", "items": file("svc/.hidden/x.go")},
		"dir escape":     {"part": "svc", "dir": "../svc", "items": file("svc/api/api.go")},
		"foreign dir":    {"part": "svc", "dir": "docs", "items": file("docs/readme.md")},
		"undeclared":     {"part": "svc", "dir": "svc/api", "items": file("svc/api/api.go"), "name": "Nope"},
		"name control":   {"part": "svc", "dir": "svc/api", "items": file("svc/api/api.go"), "name": "Serve\u0007"},
		"name two files": {"part": "svc", "dir": "svc/api", "items": []any{map[string]any{"path": "svc/api/api.go"}, map[string]any{"path": "svc/api/item.go"}}, "name": "Serve"},
		"fake edge":      {"part": "svc", "dir": "svc/api", "items": []any{map[string]any{"path": "svc/api/item.go", "to": "svc/api/api.go"}}},
		"unknown field":  {"part": "svc", "dir": "svc/api", "items": file("svc/api/api.go"), "kind": "part"},
		"no items":       {"part": "svc", "dir": "svc/api", "items": []any{}},
		"reviewed part":  {"part": "other", "items": file("svc/api/api.go")},
		"too many":       {"part": "svc", "dir": "svc/api", "items": manyItems(51)},
	}
	for name, derived := range cases {
		request := derivedRequest(t, r, "svc", derived)
		if err := applyFixture(t, r, request); err == nil {
			t.Errorf("%s: accepted %v", name, derived)
		}
	}
	// The derived target must bind to its own enclosing part.
	mismatch := derivedRequest(t, r, "other", map[string]any{"part": "svc", "dir": "svc/api", "items": file("svc/api/api.go")})
	if err := applyFixture(t, r, mismatch); err == nil {
		t.Error("a derived request bound to another part was accepted")
	}
	approve := requestFixtureFor(t, r, "approve")
	approve["derived"] = map[string]any{"part": "svc", "items": file("svc/store")}
	if err := applyFixture(t, r, approve); err == nil {
		t.Error("derived fields on Accept were accepted")
	}
	if called || mustState(r) != before {
		t.Fatal("a refused derived request reached the model or changed the state")
	}
}

// Even a change request about derived code cannot change the contract: a
// Marshal answer that edits the architecture is refused.
func TestDerivedChangeRequestNeverChangesContract(t *testing.T) {
	envelopes := make(chan json.RawMessage, 1)
	r, _ := derivedFixture(t, answering(envelopes, true))
	before := stateFixture(t, r)
	request := derivedRequest(t, r, "svc", map[string]any{"part": "svc", "dir": "svc/api", "items": []any{map[string]any{"path": "svc/api/item.go"}}})
	request["request_type"] = "change"
	if err := applyFixture(t, r, request); err != nil {
		t.Fatal(err)
	}
	after := waitFixture(t, r, "failed")
	if !equalJSON(after["architecture"], before["architecture"]) || after["revision"] != before["revision"] {
		t.Fatal("a derived change request changed the contract")
	}
	if !strings.Contains(after["comments"].([]any)[0].(map[string]any)["error"].(string), "derived code") {
		t.Fatalf("unexpected refusal: %v", after["comments"])
	}
	// Its failure does not block Accept.
	if err := applyFixture(t, r, requestFixtureFor(t, r, "approve")); err != nil {
		t.Fatalf("an open derived request blocked Accept: %v", err)
	}
}

func manyItems(n int) []any {
	items := []any{}
	for i := 0; i < n; i++ {
		items = append(items, map[string]any{"path": "svc/api/api.go", "to": "svc/api/x" + strings.Repeat("y", i) + ".go"})
	}
	return items
}

func toStrings(value any) []string {
	out := []string{}
	for _, v := range value.([]any) {
		out = append(out, v.(string))
	}
	return out
}
