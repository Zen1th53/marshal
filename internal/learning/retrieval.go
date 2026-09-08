package learning

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Query bounds a memory retrieval. Retrieval is always project-aware and
// bounded: an unbounded query over all memory is not a supported operation,
// because unbounded recall is how irrelevant and stale knowledge leaks into a
// decision.
type Query struct {
	ProjectID string
	// IncludeGeneral admits GENERAL-scope items alongside project memory.
	IncludeGeneral bool
	Terms          []string
	TaskClass      string
	// IncludeStale surfaces stale and invalidated items for audit views. It
	// never makes them usable: Result.Usable stays false.
	IncludeStale bool
	Limit        int
}

// Result is one retrieved item with the uncertainty kept attached. Callers
// cannot receive a claim without also receiving its state, freshness and
// contradiction signal, so a decision cannot silently treat memory as fact.
type Result struct {
	Item Item `json:"item"`
	// Usable reports whether this item may inform a decision. A stale,
	// invalidated or unresolved-contested item is returned but not usable.
	Usable bool `json:"usable"`
	// Fresh reports freshness at the query time.
	Fresh bool `json:"fresh"`
	// Contradicted reports that the item has unresolved contradictions.
	Contradicted bool `json:"contradicted"`
	// Clusters is the number of independent evidence clusters supporting it.
	Clusters int `json:"independent_clusters"`
	// Provenance summarizes where the claim came from.
	Provenance string `json:"provenance_summary"`
	// Score ranks relevance. It is a term-overlap count, not a confidence:
	// Process 07 does not invent confidence percentages.
	Score int `json:"relevance_score"`
}

// Retrieve returns bounded, scope-aware, freshness-aware memory.
//
// Ranking never hides uncertainty: a contested or stale item that matches the
// query is still returned, marked unusable, rather than filtered away so the
// caller believes no relevant memory existed.
func Retrieve(items []Item, q Query, now time.Time) []Result {
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	terms := make([]string, 0, len(q.Terms))
	for _, t := range q.Terms {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" {
			terms = append(terms, t)
		}
	}

	out := make([]Result, 0, len(items))
	for _, it := range items {
		switch it.Scope {
		case ScopeProject:
			// Project memory never crosses projects. A fact learned in one
			// repository is not evidence about another.
			if q.ProjectID == "" || it.ProjectID != q.ProjectID {
				continue
			}
		case ScopeGeneral:
			if !q.IncludeGeneral {
				continue
			}
		default:
			continue
		}

		fresh := it.Fresh(now)
		if !fresh && !q.IncludeStale {
			continue
		}

		// A secret must never leave memory through retrieval, even if one
		// somehow reached storage.
		if CarriesSecret(it.Claim) {
			continue
		}

		score := 0
		claim := strings.ToLower(it.Claim)
		for _, t := range terms {
			if strings.Contains(claim, t) {
				score++
			}
		}
		if len(terms) > 0 && score == 0 {
			continue
		}

		contradicted := len(it.Contradicts) > 0 && it.State == model.ClaimStateContested
		usable := fresh &&
			it.State != model.ClaimStateUnsupported &&
			it.State != model.ClaimStateStale &&
			it.State != model.ClaimStateInvalidated &&
			!contradicted

		out = append(out, Result{
			Item:         it,
			Usable:       usable,
			Fresh:        fresh,
			Contradicted: contradicted,
			Clusters:     independentClusters(it.Evidence),
			Provenance:   it.Provenance,
			Score:        score,
		})
	}

	// Rank by relevance, then by evidence strength, then deterministically by
	// id. Verified beats supported, but neither outranks a fresh match: recency
	// of evidence is handled by freshness, not by inflating old verified items.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Usable != out[j].Usable {
			return out[i].Usable
		}
		if ri, rj := rank(out[i].Item.State), rank(out[j].Item.State); ri != rj {
			return ri > rj
		}
		if out[i].Clusters != out[j].Clusters {
			return out[i].Clusters > out[j].Clusters
		}
		return out[i].Item.ID < out[j].Item.ID
	})

	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// InjectionKind separates binding constraints from advisory memory in a
// re-injected context. The distinction is load-bearing: memory is advisory
// unless canonical policy or the Goal makes it binding.
type InjectionKind string

const (
	// InjectionConstraint is a canonical constraint. It takes precedence over
	// every advisory memory in the same context.
	InjectionConstraint InjectionKind = "CONSTRAINT"
	// InjectionAdvisory is learned memory offered as context only.
	InjectionAdvisory InjectionKind = "ADVISORY"
	// InjectionContradiction is a visible unresolved disagreement.
	InjectionContradiction InjectionKind = "CONTRADICTION"
)

// Injection is one entry in a context re-injected into a later process.
type Injection struct {
	Kind       InjectionKind `json:"kind"`
	Text       string        `json:"text"`
	ItemID     string        `json:"item_id,omitempty"`
	State      string        `json:"state,omitempty"`
	Scope      Scope         `json:"scope,omitempty"`
	Provenance string        `json:"provenance,omitempty"`
	// Binding marks an entry that constrains the consuming process rather than
	// merely informing it.
	Binding bool `json:"binding"`
}

// BuildContext turns retrieval results into a context for a later Process 03,
// 04 or 05 decision.
//
// Constraints are emitted first and marked binding. Advisory memory follows and
// is never marked binding, so a later process cannot mistake a learned habit
// for a rule. Unresolved contradictions are emitted explicitly rather than
// dropped, because hiding a disagreement is how memory quietly becomes wrong.
func BuildContext(constraints []string, results []Result) ([]Injection, error) {
	out := make([]Injection, 0, len(constraints)+len(results))
	for _, c := range constraints {
		if strings.TrimSpace(c) == "" {
			continue
		}
		if CarriesSecret(c) {
			return nil, ErrSecretMaterial
		}
		out = append(out, Injection{Kind: InjectionConstraint, Text: c, Binding: true})
	}
	for _, r := range results {
		if CarriesSecret(r.Item.Claim) {
			return nil, ErrSecretMaterial
		}
		if r.Contradicted {
			out = append(out, Injection{
				Kind:       InjectionContradiction,
				Text:       r.Item.Claim,
				ItemID:     r.Item.ID,
				State:      string(r.Item.State),
				Scope:      r.Item.Scope,
				Provenance: r.Provenance,
			})
			continue
		}
		// Stale memory is never injected as fact. It is dropped from the
		// advisory context entirely; a caller that wants it for an audit view
		// asks for it explicitly through Retrieve.
		if !r.Usable {
			continue
		}
		out = append(out, Injection{
			Kind:       InjectionAdvisory,
			Text:       r.Item.Claim,
			ItemID:     r.Item.ID,
			State:      string(r.Item.State),
			Scope:      r.Item.Scope,
			Provenance: r.Provenance,
		})
	}
	return out, nil
}

// ValidateQuery rejects an unbounded or cross-project query.
func ValidateQuery(q Query) error {
	if strings.TrimSpace(q.ProjectID) == "" && !q.IncludeGeneral {
		return fmt.Errorf("%w: query needs a project or general scope", ErrInvalid)
	}
	for _, t := range q.Terms {
		if CarriesSecret(t) {
			return ErrSecretMaterial
		}
	}
	return nil
}
