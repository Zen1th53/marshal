package marshal

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Zen1th53/marshal/internal/router"
)

func TestRecommendDeterministicRanking(t *testing.T) {
	inventory := Inventory{Profiles: []router.ModelProfile{
		{Provider: "z", Model: "one", Available: true, MaxContext: 100, CostClass: "LOW", LatencyClass: "FAST"},
		{Provider: "a", Model: "two", Available: true, MaxContext: 100, CostClass: "LOW", LatencyClass: "FAST"},
	}}
	first, err := Recommend(GoalAssessment{}, inventory, router.RouteRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		next, err := Recommend(GoalAssessment{}, inventory, router.RouteRequest{})
		if err != nil || !reflect.DeepEqual(first, next) {
			t.Fatalf("ranking changed: %v %v", next, err)
		}
	}
	if len(first.Candidates) != 2 || first.Candidates[0].Provider != "a" || first.Candidates[0].Reason == "" {
		t.Fatalf("unexpected ranking: %+v", first)
	}
}

func TestRecommendEmptyInventoryNamesMissingSources(t *testing.T) {
	got, err := Recommend(GoalAssessment{}, Inventory{MissingSources: []string{"codex cache", "ollama tags"}}, router.RouteRequest{})
	if !errors.Is(err, ErrNoCandidate) || len(got.Candidates) != 0 || !reflect.DeepEqual(got.MissingSources, []string{"codex cache", "ollama tags"}) {
		t.Fatalf("got %+v, %v", got, err)
	}
}
