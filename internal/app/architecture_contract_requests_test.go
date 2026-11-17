package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func contractSeedForTest(t *testing.T, revision int64, digest, responsibility string) *contractReviewSeed {
	t.Helper()
	c := projectcontext.Contract{ID: "shop", Revision: revision, Purpose: "Sell things", Components: []projectcontext.Component{
		{ID: "api", Responsibility: "Routes requests", Paths: []string{"api"}, Dependencies: []string{"orders"}},
		{ID: "orders", Responsibility: responsibility, Paths: []string{"orders"}},
	}}
	seed, err := contractReviewSeedFor(c, digest, "en")
	if err != nil {
		t.Fatal(err)
	}
	return seed
}

func requestEnvelopeForTest(t *testing.T, seed *contractReviewSeed, kind string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"request_id": "req-1", "request_type": kind, "request": "Orders must also refund", "revision": json.Number("1"),
		"architecture": json.RawMessage(seed.Architecture),
		"selection":    map[string]any{"anchor": map[string]any{"kind": "component", "id": "orders"}, "components": []any{map[string]any{"id": "orders"}}, "connections": []any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestContractFeedbackForwardsTheExactSelection(t *testing.T) {
	seed := contractSeedForTest(t, 1, "d1", "Checkout")
	var got ArchitectureRequest
	forward := func(_ context.Context, req ArchitectureRequest) (ArchitectureAnswer, error) {
		got = req
		return ArchitectureAnswer{Status: "answered", Reply: "It already refunds.", Provider: "claude", Session: "s"}, nil
	}
	out, err := contractFeedback(forward, func(context.Context) (*contractReviewSeed, error) { return seed, nil })(context.Background(), requestEnvelopeForTest(t, seed, "comment"))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "req-1" || got.Type != "comment" || got.Text != "Orders must also refund" || string(got.Components) != `[{"id":"orders"}]` {
		t.Fatalf("forwarded request = %+v", got)
	}
	var source contractReviewSource
	if err := json.Unmarshal(got.Source, &source); err != nil || source.Digest != "d1" {
		t.Fatalf("source contract = %s", got.Source)
	}
	var result map[string]json.RawMessage
	_ = json.Unmarshal(out, &result)
	if string(result["status"]) != `"answered"` || string(result["questions"]) != `[]` {
		t.Fatalf("result = %s", out)
	}
	var before, after any
	_ = json.Unmarshal(seed.Architecture, &before)
	_ = json.Unmarshal(result["architecture"], &after)
	b1, _ := json.Marshal(before)
	b2, _ := json.Marshal(after)
	if string(b1) != string(b2) {
		t.Fatal("an answer must keep the architecture unchanged")
	}
}

func TestContractFeedbackDrawsTheRevisedContractForAChange(t *testing.T) {
	seed := contractSeedForTest(t, 1, "d1", "Checkout")
	revised := contractSeedForTest(t, 2, "d2", "Checkout and refunds")
	forward := func(context.Context, ArchitectureRequest) (ArchitectureAnswer, error) {
		return ArchitectureAnswer{Status: "proposed_change", Reply: "Orders now refunds.", Provider: "claude", Session: "s"}, nil
	}
	out, err := contractFeedback(forward, func(context.Context) (*contractReviewSeed, error) { return revised, nil })(context.Background(), requestEnvelopeForTest(t, seed, "change"))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Architecture struct {
			Source contractReviewSource `json:"source_contract"`
		} `json:"architecture"`
	}
	if err := json.Unmarshal(out, &result); err != nil || result.Architecture.Source.Digest != "d2" {
		t.Fatalf("the change must be the revised pending contract: %s", out)
	}
	_, err = contractFeedback(forward, func(context.Context) (*contractReviewSeed, error) { return seed, nil })(context.Background(), requestEnvelopeForTest(t, seed, "change"))
	if !errors.Is(err, errNoNewerContract) {
		t.Fatalf("a change without a revised contract must be refused, got %v", err)
	}
}

func TestContractUIIsDeterministic(t *testing.T) {
	seed := contractSeedForTest(t, 1, "d1", "")
	ui, err := contractUIFor(seed.Architecture, "en")
	if err != nil {
		t.Fatal(err)
	}
	if string(ui) != string(seed.UI) {
		t.Fatal("the overlay of a redrawn contract must equal the seed's")
	}
}
