package planner

import (
	"context"
	"regexp"
	"strings"
	"time"
)

type QueryIntent string

const (
	IntentExact    QueryIntent = "exact"
	IntentLexical  QueryIntent = "lexical"
	IntentSemantic QueryIntent = "semantic"
	IntentGraph    QueryIntent = "graph"
	IntentTemporal QueryIntent = "temporal"
)

type QueryPlan struct {
	RawQuery        string      `json:"raw_query"`
	PrimaryIntent   QueryIntent `json:"primary_intent"`
	ExactSymbols    []string    `json:"exact_symbols,omitempty"`
	FilePaths       []string    `json:"file_paths,omitempty"`
	MemoryIDs       []string    `json:"memory_ids,omitempty"`
	AllowedScopeIDs []string    `json:"allowed_scope_ids"`
	AsOf            *time.Time  `json:"as_of,omitempty"`
	KnownAt         *time.Time  `json:"known_at,omitempty"`
	PlanReasons     []string    `json:"plan_reasons"`
}

type Planner struct {
	pathRegex   *regexp.Regexp
	symbolRegex *regexp.Regexp
	idRegex     *regexp.Regexp
}

func NewPlanner() *Planner {
	return &Planner{
		pathRegex:   regexp.MustCompile(`([a-zA-Z0-9_\-\.]+\/[a-zA-Z0-9_\-\.\/]+\.[a-zA-Z0-9]+)`),
		symbolRegex: regexp.MustCompile(`\b([a-z]+[A-Z][a-zA-Z0-9]*|[A-Z][a-z0-9]+[A-Z][a-zA-Z0-9]*|[a-zA-Z0-9]+_[a-zA-Z0-9_]+)\b`),
		idRegex:     regexp.MustCompile(`\b(MEM-[A-Z0-9\-]+|TASK-[A-Z0-9\-]+|DEC-[A-Z0-9\-]+)\b`),
	}
}

// Plan analyzes query text and constructs an authorized, bounded retrieval plan with explainable reasons.
func (p *Planner) Plan(ctx context.Context, rawQuery string, allowedScopeIDs []string, now time.Time) (QueryPlan, error) {
	var reasons []string

	// Enforce immutable caller authorization on scopes
	scoped := make([]string, len(allowedScopeIDs))
	copy(scoped, allowedScopeIDs)

	// 1. Extract memory/task/decision IDs
	var memoryIDs []string
	for _, match := range p.idRegex.FindAllString(rawQuery, -1) {
		memoryIDs = append(memoryIDs, match)
	}

	// 2. Extract file paths
	var filePaths []string
	for _, match := range p.pathRegex.FindAllString(rawQuery, -1) {
		filePaths = append(filePaths, match)
	}

	// 3. Extract code symbols (CamelCase, PascalCase)
	var symbols []string
	for _, match := range p.symbolRegex.FindAllString(rawQuery, -1) {
		if !strings.HasPrefix(match, "MEM-") && !strings.HasPrefix(match, "TASK-") && !strings.HasPrefix(match, "DEC-") {
			symbols = append(symbols, match)
		}
	}

	// 4. Determine intent
	var intent QueryIntent
	switch {
	case len(memoryIDs) > 0:
		intent = IntentExact
		reasons = append(reasons, "detected explicit record ID")
	case len(filePaths) > 0 || len(symbols) > 0:
		intent = IntentExact
		reasons = append(reasons, "detected file path or code symbol references")
	case strings.Contains(strings.ToLower(rawQuery), "history") || strings.Contains(strings.ToLower(rawQuery), "yesterday") || strings.Contains(strings.ToLower(rawQuery), "past"):
		intent = IntentTemporal
		reasons = append(reasons, "detected temporal inquiry keywords")
	case strings.Contains(strings.ToLower(rawQuery), "depends") || strings.Contains(strings.ToLower(rawQuery), "related to"):
		intent = IntentGraph
		reasons = append(reasons, "detected relationship/dependency inquiry keywords")
	default:
		intent = IntentSemantic
		reasons = append(reasons, "defaulted to conceptual semantic retrieval")
	}

	return QueryPlan{
		RawQuery:        rawQuery,
		PrimaryIntent:   intent,
		ExactSymbols:    symbols,
		FilePaths:       filePaths,
		MemoryIDs:       memoryIDs,
		AllowedScopeIDs: scoped,
		PlanReasons:     reasons,
	}, nil
}
