package architecturereview

import (
	"context"
	"encoding/json"
	"testing"
)

func localizedFixture(t *testing.T, language string, architecture json.RawMessage) json.RawMessage {
	t.Helper()
	a, err := decodeObject(architecture)
	if err != nil {
		t.Fatal(err)
	}
	strings := []any{}
	for key, text := range EnglishUIStrings() {
		strings = append(strings, map[string]any{"key": key, "text": "Display: " + text})
	}
	components := []any{}
	for _, value := range a["components"].([]any) {
		c := value.(map[string]any)
		components = append(components, map[string]any{"id": c["id"], "name": "Display: " + c["name"].(string), "responsibility": "Display: " + c["responsibility"].(string)})
	}
	connections := []any{}
	for _, value := range a["connections"].([]any) {
		e := value.(map[string]any)
		connections = append(connections, map[string]any{"id": e["id"], "label": "Display: " + e["label"].(string), "caption": "Display: " + e["label"].(string)})
	}
	raw, err := canonicalJSON(map[string]any{"language": language, "strings": strings, "components": components, "connections": connections})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestReviewDefaultsToEnglishAndSendsExactVisibleLanguage(t *testing.T) {
	envelopes := make(chan json.RawMessage, 1)
	r := newFixture(t, func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
		envelopes <- e
		return resultFixture(e, "answered", false), nil
	})
	s := stateFixture(t, r)
	if s["language"] != "English" || s["ui"].(map[string]any)["strings"].(map[string]any)["select_area"] != "Select area" {
		t.Fatal("default UI language is not English")
	}
	if err := applyFixture(t, r, requestFixture(t, r, "submit")); err != nil {
		t.Fatal(err)
	}
	waitFixture(t, r, "answered")
	var envelope map[string]any
	_ = json.Unmarshal(<-envelopes, &envelope)
	if envelope["language"] != "English" || envelope["display"] == nil {
		t.Fatal("Marshal did not receive the exact approved view language and labels")
	}
}

func TestReviewLocalizationDoesNotChangeCanonicalArchitecture(t *testing.T) {
	r := newFixture(t, nil)
	before := stateFixture(t, r)
	if err := applyFixture(t, r, requestFixture(t, r, "approve")); err != nil {
		t.Fatal(err)
	}
	config := r.config
	config.Language = "Owner approved test language"
	config.UI = localizedFixture(t, config.Language, proposalFixture())
	localized, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	after := stateFixture(t, localized)
	if after["digest"] != before["digest"] || !equalJSON(after["architecture"], before["architecture"]) {
		t.Fatal("display translation changed architecture or stable IDs")
	}
	if after["ui_digest"] == before["ui_digest"] {
		t.Fatal("language overlay did not change its independently bound UI digest")
	}
	if after["approved"] != false {
		t.Fatal("language changed while old browser acceptance stayed valid")
	}
	if after["ui"].(map[string]any)["components"].(map[string]any)["capture"].(map[string]any)["name"] != "Display: Capture" {
		t.Fatal("localized label overlay missing")
	}
	config.UI = nil
	cached, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if stateFixture(t, cached)["ui_digest"] != after["ui_digest"] {
		t.Fatal("localized overlay cache was not bound to architecture/language")
	}
}

func TestReviewLocalizationRejectsUnboundDisplayObjects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"wrong language", func(ui map[string]any) { ui["language"] = "unapproved" }},
		{"missing string", func(ui map[string]any) { ui["strings"] = ui["strings"].([]any)[1:] }},
		{"unknown string", func(ui map[string]any) { ui["strings"].([]any)[0].(map[string]any)["key"] = "invented" }},
		{"missing component", func(ui map[string]any) { ui["components"] = ui["components"].([]any)[1:] }},
		{"unknown component", func(ui map[string]any) { ui["components"].([]any)[0].(map[string]any)["id"] = "invented" }},
		{"topology supplied", func(ui map[string]any) { ui["connections"].([]any)[0].(map[string]any)["target"] = "invented" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newFixture(t, nil)
			config := r.config
			config.Language = "Owner approved test language"
			ui, _ := decodeObject(localizedFixture(t, config.Language, proposalFixture()))
			tc.mutate(ui)
			config.UI, _ = canonicalJSON(ui)
			if _, err := New(config); err == nil {
				t.Fatal("unbound UI overlay accepted")
			}
		})
	}
}

func TestReviewRefreshesLocalizedLabelsAfterCanonicalChange(t *testing.T) {
	r := newFixture(t, func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
		return resultFixture(e, "proposed_change", true), nil
	})
	r.config.Localize = func(ctx context.Context, language string, architecture json.RawMessage) (json.RawMessage, error) {
		return localizedFixture(t, language, architecture), nil
	}
	q := requestFixture(t, r, "submit")
	q["request_type"] = "change"
	q["comment"] = "Rename selected Capture to Packet observer only"
	if err := applyFixture(t, r, q); err != nil {
		t.Fatal(err)
	}
	s := waitFixture(t, r, "proposed_change")
	if s["ui"].(map[string]any)["components"].(map[string]any)["capture"].(map[string]any)["name"] != "Display: Packet observer" {
		t.Fatal("browser displayed stale label after model revision")
	}
}

func TestReviewRejectsStaleDisplayBindingBeforeModel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing UI digest", func(q map[string]any) { delete(q, "ui_digest") }},
		{"wrong UI digest", func(q map[string]any) { q["ui_digest"] = "old" }},
		{"missing language", func(q map[string]any) { delete(q, "language") }},
		{"unapproved language", func(q map[string]any) { q["language"] = "Unapproved" }},
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
				t.Fatal("stale visible label/language binding accepted")
			}
			select {
			case <-called:
				t.Fatal("stale display reached model")
			default:
			}
		})
	}
}
