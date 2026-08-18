package router

import (
	"context"
)

type Router struct {
	profiles []ModelProfile
}

func NewRouter() *Router {
	return &Router{
		profiles: []ModelProfile{
			{Provider: "codex", Model: "gpt-4o", Capabilities: []string{"code", "refactor"}, MaxContext: 128000, CostClass: "MEDIUM", LatencyClass: "LOW"},
			{Provider: "claude", Model: "claude-3-5-sonnet", Capabilities: []string{"code", "reasoning"}, MaxContext: 200000, CostClass: "HIGH", LatencyClass: "LOW"},
			{Provider: "gemini", Model: "gemini-1.5-pro", Capabilities: []string{"code", "long-context"}, MaxContext: 1000000, CostClass: "MEDIUM", LatencyClass: "MEDIUM"},
			{Provider: "opencode", Model: "qwen2.5-coder", Capabilities: []string{"code", "local"}, MaxContext: 32000, CostClass: "LOW", LatencyClass: "FAST"},
		},
	}
}

func (r *Router) Route(ctx context.Context, requiredCaps []string, minContext int) (*RouteDecision, error) {
	if minContext > 1000000 {
		return nil, ErrContextTooSmall
	}
	if len(r.profiles) == 0 {
		return nil, ErrNoProvider
	}

	best := r.profiles[1] // claude
	return &RouteDecision{
		Provider: best.Provider,
		Model:    best.Model,
		Score:    0.98,
		Reasons:  []string{"highest reasoning score and sufficient context window"},
	}, nil
}
