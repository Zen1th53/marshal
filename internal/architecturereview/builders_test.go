package architecturereview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func allAvailableBuilders(context.Context) []BuilderOption {
	return []BuilderOption{
		{ID: "claude", Glyph: "✳", Available: true},
		{ID: "codex", Glyph: "⬡", Available: true},
		{ID: "agy", Glyph: "◆", Available: true},
		{ID: "opencode", Glyph: "●", Available: true},
	}
}

func builderReview(t *testing.T, stateDir string, builders func(context.Context) []BuilderOption, feedback func(context.Context, json.RawMessage) (json.RawMessage, error)) *Review {
	t.Helper()
	view, err := GenericLayouts(proposalFixture())
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Config{Context: t.Context(), Project: "fixture", ArchitectureID: "architecture-fixture", StateDir: stateDir, Proposal: proposalFixture(), Layouts: view, Feedback: feedback, Builders: builders})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func builderRequest(t *testing.T, r *Review, component, builder string) map[string]any {
	t.Helper()
	s := stateFixture(t, r)
	return map[string]any{"protocol_version": Version, "action": "builder", "revision": s["revision"], "digest": s["digest"], "ui_digest": s["ui_digest"], "language": s["language"], "builders_digest": s["builders_digest"], "component_id": component, "builder": builder}
}

func approveRequest(t *testing.T, r *Review) map[string]any {
	t.Helper()
	s := stateFixture(t, r)
	return map[string]any{"protocol_version": Version, "action": "approve", "revision": s["revision"], "digest": s["digest"], "ui_digest": s["ui_digest"], "language": s["language"], "builders_digest": s["builders_digest"]}
}

func TestReviewBuilderChoicePersistsAndDoesNotChangeDigest(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	r := builderReview(t, stateDir, allAvailableBuilders, nil)
	before := stateFixture(t, r)
	if err := applyFixture(t, r, builderRequest(t, r, "capture", "codex")); err != nil {
		t.Fatal(err)
	}
	after := stateFixture(t, r)
	if after["digest"] != before["digest"] {
		t.Fatal("choosing a builder changed the architecture digest")
	}
	if after["builders_digest"] == before["builders_digest"] {
		t.Fatal("builders digest did not change after a builder choice")
	}
	if after["builders"].(map[string]any)["capture"] != "codex" {
		t.Fatalf("builder not persisted: %v", after["builders"])
	}
	reloaded := builderReview(t, stateDir, allAvailableBuilders, nil)
	if stateFixture(t, reloaded)["builders"].(map[string]any)["capture"] != "codex" {
		t.Fatal("builder choice did not survive reload from disk")
	}
}

func TestReviewBuilderChoiceSurvivesRevision(t *testing.T) {
	r := builderReview(t, filepath.Join(t.TempDir(), "state"), allAvailableBuilders, func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
		return resultFixture(e, "proposed_change", true), nil
	})
	if err := applyFixture(t, r, builderRequest(t, r, "capture", "codex")); err != nil {
		t.Fatal(err)
	}
	q := requestFixture(t, r, "submit")
	q["request_type"] = "change"
	q["comment"] = "Rename selected Capture to Packet observer only"
	if err := applyFixture(t, r, q); err != nil {
		t.Fatal(err)
	}
	s := waitFixture(t, r, "proposed_change")
	if s["revision"].(float64) != 2 {
		t.Fatalf("expected revision 2, got %v", s["revision"])
	}
	if s["builders"].(map[string]any)["capture"] != "codex" {
		t.Fatalf("builder lost across a revision: %v", s["builders"])
	}
}

func TestReviewBuilderRefusals(t *testing.T) {
	t.Run("not supported", func(t *testing.T) {
		r := builderReview(t, filepath.Join(t.TempDir(), "state"), nil, nil)
		if err := applyFixture(t, r, builderRequest(t, r, "capture", "codex")); err == nil || err.Error() != "Choosing a builder is not available in this edition." {
			t.Fatalf("want edition refusal, got %v", err)
		}
	})
	t.Run("approved", func(t *testing.T) {
		r := builderReview(t, filepath.Join(t.TempDir(), "state"), allAvailableBuilders, nil)
		if err := applyFixture(t, r, approveRequest(t, r)); err != nil {
			t.Fatal(err)
		}
		if err := applyFixture(t, r, builderRequest(t, r, "capture", "codex")); err == nil || err.Error() != "The architecture is accepted; builders are fixed for this run." {
			t.Fatalf("want accepted refusal, got %v", err)
		}
	})
	t.Run("unknown component", func(t *testing.T) {
		r := builderReview(t, filepath.Join(t.TempDir(), "state"), allAvailableBuilders, nil)
		if err := applyFixture(t, r, builderRequest(t, r, "ghost", "codex")); err == nil || err.Error() != "That part is no longer in the architecture. Select it again." {
			t.Fatalf("want unknown-part refusal, got %v", err)
		}
	})
	t.Run("unknown builder", func(t *testing.T) {
		r := builderReview(t, filepath.Join(t.TempDir(), "state"), allAvailableBuilders, nil)
		if err := applyFixture(t, r, builderRequest(t, r, "capture", "gpt")); err == nil || err.Error() != "Unknown builder." {
			t.Fatalf("want unknown-builder refusal, got %v", err)
		}
	})
	t.Run("provider unavailable", func(t *testing.T) {
		builders := func(context.Context) []BuilderOption {
			return []BuilderOption{{ID: "opencode", Glyph: "●", Available: false, Reason: "not installed"}}
		}
		r := builderReview(t, filepath.Join(t.TempDir(), "state"), builders, nil)
		if err := applyFixture(t, r, builderRequest(t, r, "capture", "opencode")); err == nil || err.Error() != "opencode is unavailable: not installed" {
			t.Fatalf("want unavailable refusal, got %v", err)
		}
	})
}

func TestReviewApproveBindsBuilders(t *testing.T) {
	r := builderReview(t, filepath.Join(t.TempDir(), "state"), allAvailableBuilders, nil)
	stale := stateFixture(t, r)["builders_digest"]
	if err := applyFixture(t, r, builderRequest(t, r, "capture", "codex")); err != nil {
		t.Fatal(err)
	}
	request := approveRequest(t, r)
	request["builders_digest"] = stale
	if err := applyFixture(t, r, request); err == nil || err.Error() != "Builders changed. Check them and press Accept again." {
		t.Fatalf("stale builders_digest not refused: %v", err)
	}
	if err := applyFixture(t, r, approveRequest(t, r)); err != nil {
		t.Fatal(err)
	}
	s := stateFixture(t, r)
	if s["approved"] != true {
		t.Fatal("approval with current builders_digest was refused")
	}
	approval := s["approval"].(map[string]any)
	if approval["builders_digest"] != s["builders_digest"] {
		t.Fatalf("approval builders_digest %v != state %v", approval["builders_digest"], s["builders_digest"])
	}
}

func TestStoredStateRejectsTamperedBuilders(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	r := builderReview(t, stateDir, allAvailableBuilders, nil)
	if err := applyFixture(t, r, builderRequest(t, r, "capture", "codex")); err != nil {
		t.Fatal(err)
	}
	if err := applyFixture(t, r, approveRequest(t, r)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(stateDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateStoredState(raw, "fixture"); err != nil {
		t.Fatalf("untampered accepted state rejected: %v", err)
	}
	t.Run("builder value", func(t *testing.T) {
		obj, _ := decodeObject(raw)
		obj["builders"].(map[string]any)["capture"] = "claude"
		tampered, _ := canonicalJSON(obj)
		if _, err := ValidateStoredState(tampered, "fixture"); err == nil {
			t.Fatal("tampered builder value accepted")
		}
	})
	t.Run("builders digest", func(t *testing.T) {
		obj, _ := decodeObject(raw)
		obj["builders_digest"] = "0000000000000000000000000000000000000000000000000000000000000000"
		tampered, _ := canonicalJSON(obj)
		if _, err := ValidateStoredState(tampered, "fixture"); err == nil {
			t.Fatal("tampered builders digest accepted")
		}
	})
}

func TestLegacyAcceptedStateStillValid(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	r := builderReview(t, stateDir, allAvailableBuilders, nil)
	if err := applyFixture(t, r, approveRequest(t, r)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(stateDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	obj, err := decodeObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	delete(obj, "builders")
	delete(obj, "builders_digest")
	approval := obj["approval"].(map[string]any)
	delete(approval, "builders_digest")
	ui := obj["ui"].(map[string]any)
	current := ui["strings"].(map[string]any)
	legacy := map[string]any{}
	for _, key := range legacyUIStringKeys {
		legacy[key] = current[key]
	}
	ui["strings"] = legacy
	uiDigest, err := digestValue(ui)
	if err != nil {
		t.Fatal(err)
	}
	obj["ui_digest"] = uiDigest
	approval["ui_digest"] = uiDigest
	rebuilt, err := canonicalJSON(obj)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := ValidateStoredState(rebuilt, "fixture")
	if err != nil {
		t.Fatalf("legacy accepted state rejected: %v", err)
	}
	decoded, _ := decodeObject(packet)
	if decoded["status"] != "accepted" {
		t.Fatalf("legacy state is not accepted: %v", decoded["status"])
	}
	if _, ok := decoded["builders"].(map[string]any); !ok {
		t.Fatalf("packet missing builders: %v", decoded["builders"])
	}
}

func TestHTTPBuildersEndpoint(t *testing.T) {
	token := "fixture-token-with-more-than-sixteen-characters"
	t.Run("shape and order", func(t *testing.T) {
		r := builderReview(t, filepath.Join(t.TempDir(), "state"), func(context.Context) []BuilderOption {
			return []BuilderOption{
				{ID: "claude", Glyph: "✳", Available: true, Limit: "5h 62% left · resets 14:05"},
				{ID: "codex", Glyph: "⬡", Available: true, Limit: "limit unavailable"},
				{ID: "agy", Glyph: "◆", Available: false, Reason: "not available in this build"},
				{ID: "opencode", Glyph: "●", Available: false, Reason: "not installed"},
			}
		}, nil)
		handler := r.Handler(token)
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/builders", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, request)
		if out.Code != http.StatusOK {
			t.Fatalf("status %d: %s", out.Code, out.Body.String())
		}
		var body struct {
			Supported bool `json:"supported"`
			Options   []struct {
				ID        string `json:"id"`
				Glyph     string `json:"glyph"`
				Available bool   `json:"available"`
				Limit     string `json:"limit"`
				Reason    string `json:"reason"`
			} `json:"options"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if !body.Supported {
			t.Fatal("supported should be true")
		}
		wantOrder := []string{"auto", "claude", "codex", "agy", "opencode"}
		if len(body.Options) != len(wantOrder) {
			t.Fatalf("want %d options, got %d", len(wantOrder), len(body.Options))
		}
		for i, id := range wantOrder {
			if body.Options[i].ID != id {
				t.Fatalf("option %d want %q got %q", i, id, body.Options[i].ID)
			}
		}
		if body.Options[0].Glyph != "" || !body.Options[0].Available {
			t.Fatalf("auto option wrong: %+v", body.Options[0])
		}
		if body.Options[1].Limit != "5h 62% left · resets 14:05" {
			t.Fatalf("claude limit wrong: %q", body.Options[1].Limit)
		}
		if body.Options[3].Reason != "not available in this build" {
			t.Fatalf("agy reason wrong: %q", body.Options[3].Reason)
		}
	})
	t.Run("token required", func(t *testing.T) {
		r := builderReview(t, filepath.Join(t.TempDir(), "state"), allAvailableBuilders, nil)
		handler := r.Handler(token)
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/builders", nil)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, request)
		if out.Code != http.StatusForbidden {
			t.Fatalf("missing token should be 403, got %d", out.Code)
		}
	})
	t.Run("unsupported in governed-like build", func(t *testing.T) {
		r := builderReview(t, filepath.Join(t.TempDir(), "state"), nil, nil)
		handler := r.Handler(token)
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/builders", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, request)
		if out.Code != http.StatusOK {
			t.Fatalf("status %d", out.Code)
		}
		var body struct {
			Supported bool  `json:"supported"`
			Options   []any `json:"options"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Supported || len(body.Options) != 0 {
			t.Fatalf("want supported:false options:[] got %+v", body)
		}
	})
}
