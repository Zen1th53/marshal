package multitrack

import (
	"context"
	"strings"
)

type MemoryTrack string

const (
	TrackCodeSymbol         MemoryTrack = "CODE_SYMBOL"
	TrackProceduralWorkflow MemoryTrack = "PROCEDURAL_WORKFLOW"
	TrackEnvironmentGotcha  MemoryTrack = "ENVIRONMENT_GOTCHA"
	TrackFactualSemantic    MemoryTrack = "FACTUAL_SEMANTIC"
)

type TrackAllocation struct {
	PrimaryTrack MemoryTrack          `json:"primary_track"`
	TrackBudgets map[MemoryTrack]int  `json:"track_budgets"`
	TrackTopK    map[MemoryTrack]int  `json:"track_top_k"`
	Reason       string               `json:"reason"`
}

type Router struct{}

func NewRouter() *Router {
	return &Router{}
}

// AllocateTrackBudget analyzes query characteristics and dynamically allocates per-track retrieval and token budgets.
func (r *Router) AllocateTrackBudget(ctx context.Context, query string, totalTokenBudget int) TrackAllocation {
	lower := strings.ToLower(query)

	primary := TrackFactualSemantic
	reason := "general semantic architectural lookup"

	if strings.Contains(lower, "how to") || strings.Contains(lower, "steps") || strings.Contains(lower, "workflow") || strings.Contains(lower, "recipe") || strings.Contains(lower, "build and") {
		primary = TrackProceduralWorkflow
		reason = "matched workflow / procedural recipe pattern"
	} else if strings.HasPrefix(lower, "func ") || strings.Contains(lower, ".go") || strings.Contains(lower, "class ") || strings.HasPrefix(lower, "package ") {
		primary = TrackCodeSymbol
		reason = "matched code symbol / file path pattern"
	} else if strings.Contains(lower, "cgo") || strings.Contains(lower, "driver") || strings.Contains(lower, "error") || strings.Contains(lower, "failure") || strings.Contains(lower, "gotcha") {
		primary = TrackEnvironmentGotcha
		reason = "matched environment issue / known error pattern"
	}

	budgets := make(map[MemoryTrack]int)
	topK := make(map[MemoryTrack]int)

	tracks := []MemoryTrack{TrackCodeSymbol, TrackProceduralWorkflow, TrackEnvironmentGotcha, TrackFactualSemantic}
	primaryBudget := int(float64(totalTokenBudget) * 0.55)
	secondaryBudget := (totalTokenBudget - primaryBudget) / (len(tracks) - 1)

	for _, tr := range tracks {
		if tr == primary {
			budgets[tr] = primaryBudget
			topK[tr] = 8
		} else {
			budgets[tr] = secondaryBudget
			topK[tr] = 3
		}
	}

	return TrackAllocation{
		PrimaryTrack: primary,
		TrackBudgets: budgets,
		TrackTopK:    topK,
		Reason:       reason,
	}
}
