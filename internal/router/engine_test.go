package router

import (
	"context"
	"errors"
	"testing"
)

func TestRouterRouteWithDefaultProfiles(t *testing.T) {
	rt := NewRouter()
	ctx := context.Background()

	dec, err := rt.Route(ctx, []string{"code"}, 32000)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if dec.Provider == "" || dec.Model == "" {
		t.Fatalf("expected valid route decision, got %+v", dec)
	}
	if dec.Score <= 0 || dec.Score > 1.0 {
		t.Fatalf("unexpected score: %f", dec.Score)
	}
	if len(dec.Reasons) == 0 {
		t.Fatal("expected structured reasons")
	}
}

func TestRouterContextRequirementFilter(t *testing.T) {
	rt := NewRouter()
	ctx := context.Background()

	// Require 500,000 tokens context -> Only Gemini (1,000,000) is eligible
	dec, err := rt.RouteAdvanced(ctx, RouteRequest{
		RequiredCapabilities: []string{"code"},
		MinContext:           500000,
	})
	if err != nil {
		t.Fatalf("expected gemini route, got error: %v", err)
	}
	if dec.Provider != "gemini" {
		t.Fatalf("expected gemini to win for 500k context, got %s", dec.Provider)
	}

	// Require 2,000,000 tokens context -> ErrContextTooSmall
	_, err = rt.RouteAdvanced(ctx, RouteRequest{
		MinContext: 2000000,
	})
	if !errors.Is(err, ErrContextTooSmall) {
		t.Fatalf("expected ErrContextTooSmall, got %v", err)
	}
}

func TestRouterCapabilityFilter(t *testing.T) {
	rt := NewRouter()
	ctx := context.Background()

	// Require "long-context" capability -> Only Gemini matches
	dec, err := rt.RouteAdvanced(ctx, RouteRequest{
		RequiredCapabilities: []string{"long-context"},
		MinContext:           10000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Provider != "gemini" {
		t.Fatalf("expected gemini, got %s", dec.Provider)
	}

	// Require non-existent capability -> ErrCapabilityMismatch
	_, err = rt.RouteAdvanced(ctx, RouteRequest{
		RequiredCapabilities: []string{"non-existent-capability-xyz"},
	})
	if !errors.Is(err, ErrCapabilityMismatch) {
		t.Fatalf("expected ErrCapabilityMismatch, got %v", err)
	}
}

func TestRouterLocalityPreference(t *testing.T) {
	rt := NewRouter()
	ctx := context.Background()

	dec, err := rt.RouteAdvanced(ctx, RouteRequest{
		RequiredCapabilities: []string{"code"},
		MinContext:           16000,
		PreferLocal:          true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Provider != "opencode" {
		t.Fatalf("expected opencode to win with PreferLocal: true, got %s", dec.Provider)
	}
}

func TestRouterPinningAndDisabling(t *testing.T) {
	rt := NewRouter()
	ctx := context.Background()

	// 1. Pin provider to codex
	dec, err := rt.RouteAdvanced(ctx, RouteRequest{
		PinProvider: "codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Provider != "codex" {
		t.Fatalf("expected pinned provider codex, got %s", dec.Provider)
	}

	// 2. Disable codex and claude -> Next best wins
	dec, err = rt.RouteAdvanced(ctx, RouteRequest{
		DisabledProviders: []string{"codex", "claude"},
		MinContext:        16000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Provider == "codex" || dec.Provider == "claude" {
		t.Fatalf("disabled provider was selected: %s", dec.Provider)
	}
}

func TestRouterDynamicProfileUpdate(t *testing.T) {
	rt := NewRouterWithProfiles([]ModelProfile{
		{Provider: "custom-ollama", Model: "deepseek-coder", Capabilities: []string{"code"}, MaxContext: 64000, CostClass: "FREE", LatencyClass: "FAST", Available: true},
	})
	ctx := context.Background()

	dec, err := rt.Route(ctx, []string{"code"}, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Provider != "custom-ollama" || dec.Model != "deepseek-coder" {
		t.Fatalf("expected custom profile to be chosen, got: %+v", dec)
	}
}
