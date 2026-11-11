package architecturereview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDesignNormalisationRejectsHostileInput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
	}{
		{"extra top-level key", `{"findings":[{"provider":"codex","text":"x"}],"extra":1}`},
		{"unknown provider", `{"findings":[{"provider":"gpt","text":"x"}]}`},
		{"non-string text", `{"findings":[{"provider":"codex","text":5}]}`},
		{"extra finding key", `{"findings":[{"provider":"codex","text":"x","note":"y"}]}`},
		{"brief not an object", `{"brief":"x","findings":[{"provider":"codex","text":"x"}]}`},
		{"findings not an array", `{"findings":{}}`},
		{"resolution carries a provider", `{"findings":[{"provider":"codex","text":"x"}],"resolutions":[{"provider":"codex","text":"y"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := normaliseDesign(json.RawMessage(tc.input)); err == nil {
				t.Fatal("hostile design input was accepted")
			}
		})
	}
}

func TestDesignTextIsShortened(t *testing.T) {
	long := strings.Repeat("a", 20001)
	output, digest, err := normaliseDesign(json.RawMessage(`{"findings":[{"provider":"codex","text":"` + long + `"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" {
		t.Fatal("a present design has an empty digest")
	}
	item := output["findings"].([]any)[0].(map[string]any)
	if item["shortened"] != true {
		t.Fatal("over-long text was not marked shortened")
	}
	if n := utf8.RuneCountInString(item["text"].(string)); n != 20000 {
		t.Fatalf("text cut to %d runes, want 20000", n)
	}
}

func newDesignFixture(t *testing.T, raw json.RawMessage) *Review {
	t.Helper()
	view, err := GenericLayouts(proposalFixture())
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Config{Context: t.Context(), Project: "fixture", ArchitectureID: "architecture-fixture", StateDir: filepath.Join(t.TempDir(), "state"), Proposal: proposalFixture(), Layouts: view,
		Design: func(context.Context) (json.RawMessage, error) { return raw, nil }})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestApproveBindsDesignDigest(t *testing.T) {
	r := newDesignFixture(t, json.RawMessage(`{"brief":{"provider":"codex","text":"Brief"},"findings":[{"provider":"codex","text":"Finding"}],"resolutions":[{"text":"Resolved"}]}`))
	_, digest, err := r.design(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" {
		t.Fatal("expected a design digest for a present design")
	}

	stale := requestFixture(t, r, "approve")
	stale["design_digest"] = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := applyFixture(t, r, stale); err == nil || !strings.Contains(err.Error(), "The design changed") {
		t.Fatalf("stale design digest was not refused: %v", err)
	}

	current := requestFixture(t, r, "approve")
	current["design_digest"] = digest
	if err := applyFixture(t, r, current); err != nil {
		t.Fatalf("approve with the current design digest was refused: %v", err)
	}
	approval := stateFixture(t, r)["approval"].(map[string]any)
	if approval["design_digest"] != digest {
		t.Fatalf("approval.design_digest = %v, want %v", approval["design_digest"], digest)
	}
}

func TestHTTPDesignEndpointNeedsToken(t *testing.T) {
	r := newFixture(t, nil)
	server := httptest.NewServer(r.Handler("0123456789abcdef0123456789abcdef"))
	defer server.Close()
	response, err := http.Get(server.URL + "/api/design")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /api/design without a token returned %d, want 403", response.StatusCode)
	}
}
