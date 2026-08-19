package router

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
)

type Router struct {
	mu       sync.RWMutex
	profiles []ModelProfile
}

func DefaultProfiles() []ModelProfile {
	return []ModelProfile{
		{Provider: "codex", Model: "gpt-4o", Capabilities: []string{"code", "refactor"}, MaxContext: 128000, CostClass: "MEDIUM", LatencyClass: "LOW", Available: true},
		{Provider: "claude", Model: "claude-3-5-sonnet", Capabilities: []string{"code", "reasoning", "refactor"}, MaxContext: 200000, CostClass: "HIGH", LatencyClass: "LOW", Available: true},
		{Provider: "gemini", Model: "gemini-1.5-pro", Capabilities: []string{"code", "long-context", "reasoning"}, MaxContext: 1000000, CostClass: "MEDIUM", LatencyClass: "MEDIUM", Available: true},
		{Provider: "opencode", Model: "qwen2.5-coder", Capabilities: []string{"code", "local", "refactor"}, MaxContext: 32000, CostClass: "FREE", LatencyClass: "FAST", Available: true, LocalOnly: true},
	}
}

func NewRouter() *Router {
	return &Router{
		profiles: DefaultProfiles(),
	}
}

func NewRouterWithProfiles(profiles []ModelProfile) *Router {
	p := make([]ModelProfile, len(profiles))
	copy(p, profiles)
	return &Router{
		profiles: p,
	}
}

func (r *Router) SetProfiles(profiles []ModelProfile) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.profiles = make([]ModelProfile, len(profiles))
	copy(r.profiles, profiles)
}

func (r *Router) Profiles() []ModelProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p := make([]ModelProfile, len(r.profiles))
	copy(p, r.profiles)
	return p
}

type scoredProfile struct {
	profile ModelProfile
	score   float64
	reasons []string
}

func (r *Router) Route(ctx context.Context, requiredCaps []string, minContext int) (*RouteDecision, error) {
	return r.RouteAdvanced(ctx, RouteRequest{
		RequiredCapabilities: requiredCaps,
		MinContext:           minContext,
	})
}

func (r *Router) RouteAdvanced(ctx context.Context, req RouteRequest) (*RouteDecision, error) {
	r.mu.RLock()
	profiles := r.profiles
	r.mu.RUnlock()

	if len(profiles) == 0 {
		return nil, ErrNoProvider
	}

	disabledMap := make(map[string]bool)
	for _, d := range req.DisabledProviders {
		disabledMap[strings.ToLower(strings.TrimSpace(d))] = true
	}

	var hasContextMatch bool
	var hasCapabilityMatch bool
	var eligible []scoredProfile

	for _, p := range profiles {
		if !p.Available {
			continue
		}
		if disabledMap[strings.ToLower(p.Provider)] {
			continue
		}
		if req.PinProvider != "" && !strings.EqualFold(req.PinProvider, p.Provider) {
			continue
		}
		if req.PinModel != "" && !strings.EqualFold(req.PinModel, p.Model) {
			continue
		}
		if p.MaxContext < req.MinContext {
			continue
		}
		hasContextMatch = true

		if !hasAllCapabilities(p.Capabilities, req.RequiredCapabilities) {
			continue
		}
		hasCapabilityMatch = true

		score, reasons := scoreProfile(p, req)
		eligible = append(eligible, scoredProfile{
			profile: p,
			score:   score,
			reasons: reasons,
		})
	}

	if len(eligible) == 0 {
		if req.PinProvider != "" && disabledMap[strings.ToLower(req.PinProvider)] {
			return nil, ErrProviderDisabled
		}
		if !hasContextMatch && req.MinContext > 0 {
			return nil, ErrContextTooSmall
		}
		if !hasCapabilityMatch && len(req.RequiredCapabilities) > 0 {
			return nil, ErrCapabilityMismatch
		}
		return nil, ErrNoProvider
	}

	// Deterministic sorting: highest score first; tie-breakers: Provider asc, Model asc
	sort.Slice(eligible, func(i, j int) bool {
		if math.Abs(eligible[i].score-eligible[j].score) > 1e-6 {
			return eligible[i].score > eligible[j].score
		}
		if eligible[i].profile.Provider != eligible[j].profile.Provider {
			return eligible[i].profile.Provider < eligible[j].profile.Provider
		}
		return eligible[i].profile.Model < eligible[j].profile.Model
	})

	best := eligible[0]
	return &RouteDecision{
		Provider: best.profile.Provider,
		Model:    best.profile.Model,
		Score:    math.Round(best.score*1000) / 1000,
		Reasons:  best.reasons,
	}, nil
}

func hasAllCapabilities(candidateCaps, requiredCaps []string) bool {
	if len(requiredCaps) == 0 {
		return true
	}
	capMap := make(map[string]bool, len(candidateCaps))
	for _, cap := range candidateCaps {
		capMap[strings.ToLower(strings.TrimSpace(cap))] = true
	}
	for _, req := range requiredCaps {
		if !capMap[strings.ToLower(strings.TrimSpace(req))] {
			return false
		}
	}
	return true
}

func scoreProfile(p ModelProfile, req RouteRequest) (float64, []string) {
	// 1. Context headroom factor (weight: 0.30)
	headroom := 1.0
	if req.MinContext > 0 && p.MaxContext > 0 {
		headroom = float64(p.MaxContext-req.MinContext) / float64(p.MaxContext)
		if headroom < 0 {
			headroom = 0
		}
	}
	headroomFactor := headroom * 0.30

	// 2. Latency class factor (weight: 0.30)
	latencyFactor := 0.15
	switch strings.ToUpper(p.LatencyClass) {
	case "FAST":
		latencyFactor = 0.30
	case "LOW":
		latencyFactor = 0.25
	case "MEDIUM":
		latencyFactor = 0.15
	case "HIGH":
		latencyFactor = 0.05
	}

	// 3. Cost class factor (weight: 0.25)
	costFactor := 0.10
	switch strings.ToUpper(p.CostClass) {
	case "FREE":
		costFactor = 0.25
	case "LOW":
		costFactor = 0.20
	case "MEDIUM":
		costFactor = 0.15
	case "HIGH":
		costFactor = 0.05
	}

	// 4. Locality preference bonus (weight: 0.15)
	localityFactor := 0.0
	if req.PreferLocal && p.LocalOnly {
		localityFactor = 0.15
	} else if !req.PreferLocal && !p.LocalOnly {
		localityFactor = 0.10
	} else if p.LocalOnly {
		localityFactor = 0.05
	}

	totalScore := headroomFactor + latencyFactor + costFactor + localityFactor

	reasons := []string{
		fmt.Sprintf("context_headroom=%.2f (contrib=%.3f)", headroom, headroomFactor),
		fmt.Sprintf("latency=%s (contrib=%.3f)", p.LatencyClass, latencyFactor),
		fmt.Sprintf("cost=%s (contrib=%.3f)", p.CostClass, costFactor),
		fmt.Sprintf("locality_bonus=%.3f", localityFactor),
	}

	return totalScore, reasons
}
