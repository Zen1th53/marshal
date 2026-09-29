package marshal

import (
	"context"
	"errors"
	"strings"

	"github.com/Zen1th53/marshal/internal/router"
)

var ErrNoCandidate = errors.New("no candidate")

type Inventory struct {
	Profiles       []router.ModelProfile
	MissingSources []string
}

type GoalAssessment struct {
	RequiredCapabilities []string
	MinContext           int
}

type Recommendation struct {
	Candidates     []Candidate
	MissingSources []string
}

type Candidate struct {
	Provider string
	Model    string
	Score    float64
	Reason   string
}

// Recommend ranks the supplied inventory without discovering providers.
func Recommend(goal GoalAssessment, inventory Inventory, prefs router.RouteRequest) (Recommendation, error) {
	result := Recommendation{MissingSources: append([]string(nil), inventory.MissingSources...)}
	prefs.RequiredCapabilities = append(append([]string(nil), prefs.RequiredCapabilities...), goal.RequiredCapabilities...)
	if goal.MinContext > prefs.MinContext {
		prefs.MinContext = goal.MinContext
	}
	ranked, err := router.NewRouterWithProfiles(inventory.Profiles).RankAdvanced(context.Background(), prefs)
	if err != nil {
		return result, ErrNoCandidate
	}
	for _, item := range ranked {
		reason := "eligible for the goal"
		if len(item.Reasons) > 0 {
			reason = strings.Join(item.Reasons, "; ")
		}
		result.Candidates = append(result.Candidates, Candidate{item.Provider, item.Model, item.Score, reason})
	}
	return result, nil
}
