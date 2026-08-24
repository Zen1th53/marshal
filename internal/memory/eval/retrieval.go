// Package eval provides deterministic, provider-neutral evaluation of ranked
// memory recall results against a versioned golden relevance corpus.
package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"time"
)

var DefaultCutoffs = []int{1, 3, 5, 10}

// Corpus is a maintainable JSON representation of canonical memory records
// and their expected relevance for realistic queries. Record fields not
// needed by the evaluator are intentionally carried as JSON-friendly values;
// the integration harness maps them onto the canonical runtime model.
type Corpus struct {
	Version     int             `json:"version"`
	Description string          `json:"description"`
	ProjectID   string          `json:"project_id"`
	Records     []RecordFixture `json:"records"`
	Queries     []QueryFixture  `json:"queries"`
}

type RecordFixture struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Body         string         `json:"body"`
	Kind         string         `json:"kind"`
	Lifecycle    string         `json:"lifecycle"`
	Confidence   string         `json:"confidence"`
	Authority    string         `json:"authority"`
	Scope        string         `json:"scope"`
	ScopeID      string         `json:"scope_id"`
	ACLScope     string         `json:"acl_scope,omitempty"`
	HeadCommit   string         `json:"head_commit,omitempty"`
	Branch       string         `json:"branch,omitempty"`
	WorktreeID   string         `json:"worktree_id,omitempty"`
	SupersededBy []string       `json:"superseded_by,omitempty"`
	Supersedes   []string       `json:"supersedes,omitempty"`
	ConflictIDs  []string       `json:"conflict_ids,omitempty"`
	EvidenceIDs  []string       `json:"evidence_ids,omitempty"`
	ExtMeta      map[string]any `json:"ext_meta,omitempty"`
	Tags         []string       `json:"tags,omitempty"`
}

type QueryFixture struct {
	ID                string              `json:"id"`
	Category          string              `json:"category"`
	Text              string              `json:"text"`
	PrincipalID       string              `json:"principal_id"`
	AllowedScopeIDs   []string            `json:"allowed_scope_ids,omitempty"`
	CurrentHead       string              `json:"current_head,omitempty"`
	CanonicalHead     string              `json:"canonical_head,omitempty"`
	CurrentBranch     string              `json:"current_branch,omitempty"`
	CurrentWorktreeID string              `json:"current_worktree_id,omitempty"`
	ModifiedFiles     []string            `json:"modified_files,omitempty"`
	DeletedFiles      []string            `json:"deleted_files,omitempty"`
	RenamedFiles      map[string]string   `json:"renamed_files,omitempty"`
	CurrentFileHashes map[string]string   `json:"current_file_hashes,omitempty"`
	ExistingSymbols   map[string]bool     `json:"existing_symbols,omitempty"`
	InvalidatedTests  []string            `json:"invalidated_tests,omitempty"`
	Required          []string            `json:"required"`
	Acceptable        []string            `json:"acceptable,omitempty"`
	Irrelevant        []string            `json:"irrelevant,omitempty"`
	Forbidden         map[string][]string `json:"forbidden,omitempty"`
}

func LoadCorpus(r io.Reader) (Corpus, error) {
	var corpus Corpus
	dec := json.NewDecoder(r)
	if err := dec.Decode(&corpus); err != nil {
		return Corpus{}, fmt.Errorf("decode golden relevance corpus: %w", err)
	}
	if err := corpus.Validate(); err != nil {
		return Corpus{}, err
	}
	return corpus, nil
}

func (c Corpus) Validate() error {
	if c.Version <= 0 || c.ProjectID == "" || len(c.Records) == 0 || len(c.Queries) == 0 {
		return errors.New("golden relevance corpus requires version, project_id, records, and queries")
	}
	records := make(map[string]struct{}, len(c.Records))
	for _, rec := range c.Records {
		if rec.ID == "" || rec.Title == "" || rec.Body == "" {
			return errors.New("golden relevance record requires id, title, and body")
		}
		if _, duplicate := records[rec.ID]; duplicate {
			return fmt.Errorf("duplicate golden relevance record %q", rec.ID)
		}
		records[rec.ID] = struct{}{}
	}
	queries := make(map[string]struct{}, len(c.Queries))
	for _, query := range c.Queries {
		if query.ID == "" || query.Text == "" || query.PrincipalID == "" || len(query.Required) == 0 {
			return fmt.Errorf("golden query %q requires id, text, principal_id, and required results", query.ID)
		}
		if _, duplicate := queries[query.ID]; duplicate {
			return fmt.Errorf("duplicate golden query %q", query.ID)
		}
		queries[query.ID] = struct{}{}
		seen := make(map[string]string)
		groups := map[string][]string{"required": query.Required, "acceptable": query.Acceptable, "irrelevant": query.Irrelevant}
		for reason, ids := range query.Forbidden {
			groups["forbidden:"+reason] = ids
		}
		for group, ids := range groups {
			for _, id := range ids {
				if _, exists := records[id]; !exists {
					return fmt.Errorf("golden query %q references unknown record %q", query.ID, id)
				}
				if previous, duplicate := seen[id]; duplicate {
					return fmt.Errorf("golden query %q classifies %q as both %s and %s", query.ID, id, previous, group)
				}
				seen[id] = group
			}
		}
	}
	return nil
}

type QueryOutcome struct {
	QueryID        string
	RankedIDs      []string
	ContextBytes   int
	RecallDuration time.Duration
}

type Metrics struct {
	QueryCount                  int
	RecallAtK                   map[int]float64
	PrecisionAtK                map[int]float64
	MRR                         float64
	NDCGAtK                     map[int]float64
	FalsePositiveRecallRate     float64
	ForbiddenExposureRate       map[string]float64
	ContextBytesPerUsefulResult float64
	ContextTokensPerUseful      float64
	// MeanTimeToFirstUseful is an upper bound: the synchronous runtime exposes
	// completed recall latency, not a per-result streaming timestamp.
	MeanTimeToFirstUseful time.Duration
}

// Evaluate computes macro-averaged ranking metrics. Required results have
// relevance grade 2, acceptable results grade 1, and all other records grade
// 0. Recall is defined against required results; precision and NDCG count both
// required and acceptable results. Context tokens use the documented local
// estimate of four UTF-8 bytes per token rather than claiming provider usage.
func Evaluate(c Corpus, outcomes []QueryOutcome, cutoffs []int) (Metrics, error) {
	if err := c.Validate(); err != nil {
		return Metrics{}, err
	}
	if len(cutoffs) == 0 {
		cutoffs = DefaultCutoffs
	}
	cutoffs = append([]int(nil), cutoffs...)
	sort.Ints(cutoffs)
	queryByID := make(map[string]QueryFixture, len(c.Queries))
	for _, query := range c.Queries {
		queryByID[query.ID] = query
	}
	if len(outcomes) != len(c.Queries) {
		return Metrics{}, fmt.Errorf("got %d query outcomes for %d golden queries", len(outcomes), len(c.Queries))
	}

	metrics := Metrics{QueryCount: len(c.Queries), RecallAtK: map[int]float64{}, PrecisionAtK: map[int]float64{}, NDCGAtK: map[int]float64{}, ForbiddenExposureRate: map[string]float64{}}
	var reciprocalRanks, falsePositives, returned, useful, contextBytes float64
	var usefulLatency time.Duration
	var queriesWithUseful int
	forbiddenHits := map[string]int{}
	forbiddenOpportunities := map[string]int{}
	seenOutcomes := make(map[string]struct{}, len(outcomes))

	for _, outcome := range outcomes {
		query, ok := queryByID[outcome.QueryID]
		if !ok {
			return Metrics{}, fmt.Errorf("outcome references unknown golden query %q", outcome.QueryID)
		}
		if _, duplicate := seenOutcomes[outcome.QueryID]; duplicate {
			return Metrics{}, fmt.Errorf("duplicate outcome for golden query %q", outcome.QueryID)
		}
		seenOutcomes[outcome.QueryID] = struct{}{}
		required := set(query.Required)
		acceptable := set(query.Acceptable)
		forbidden := make(map[string]map[string]struct{}, len(query.Forbidden))
		for reason, ids := range query.Forbidden {
			forbidden[reason] = set(ids)
			forbiddenOpportunities[reason] += len(ids)
		}

		firstRelevantRank := 0
		maxK := cutoffs[len(cutoffs)-1]
		for rank, id := range prefix(outcome.RankedIDs, maxK) {
			_, isRequired := required[id]
			_, isAcceptable := acceptable[id]
			if (isRequired || isAcceptable) && firstRelevantRank == 0 {
				firstRelevantRank = rank + 1
			}
			if isRequired || isAcceptable {
				useful++
			} else {
				falsePositives++
			}
			returned++
			for reason, ids := range forbidden {
				if _, exposed := ids[id]; exposed {
					forbiddenHits[reason]++
				}
			}
		}
		if firstRelevantRank > 0 {
			reciprocalRanks += 1 / float64(firstRelevantRank)
			usefulLatency += outcome.RecallDuration
			queriesWithUseful++
		}
		contextBytes += float64(outcome.ContextBytes)

		for _, k := range cutoffs {
			top := prefix(outcome.RankedIDs, k)
			requiredHits, relevantHits := 0, 0
			gains := make([]float64, 0, len(top))
			for _, id := range top {
				grade := 0.0
				if _, ok := required[id]; ok {
					requiredHits++
					relevantHits++
					grade = 2
				} else if _, ok := acceptable[id]; ok {
					relevantHits++
					grade = 1
				}
				gains = append(gains, grade)
			}
			metrics.RecallAtK[k] += float64(requiredHits) / float64(len(required))
			metrics.PrecisionAtK[k] += float64(relevantHits) / float64(k)
			ideal := make([]float64, 0, len(required)+len(acceptable))
			for range query.Required {
				ideal = append(ideal, 2)
			}
			for range query.Acceptable {
				ideal = append(ideal, 1)
			}
			ideal = prefixFloat(ideal, k)
			if idealDCG := dcg(ideal); idealDCG > 0 {
				metrics.NDCGAtK[k] += dcg(gains) / idealDCG
			}
		}
	}

	queryCount := float64(len(c.Queries))
	for _, k := range cutoffs {
		metrics.RecallAtK[k] /= queryCount
		metrics.PrecisionAtK[k] /= queryCount
		metrics.NDCGAtK[k] /= queryCount
	}
	metrics.MRR = reciprocalRanks / queryCount
	if returned > 0 {
		metrics.FalsePositiveRecallRate = falsePositives / returned
	}
	for reason, opportunities := range forbiddenOpportunities {
		if opportunities > 0 {
			metrics.ForbiddenExposureRate[reason] = float64(forbiddenHits[reason]) / float64(opportunities)
		}
	}
	if useful > 0 {
		metrics.ContextBytesPerUsefulResult = contextBytes / useful
		metrics.ContextTokensPerUseful = math.Ceil(metrics.ContextBytesPerUsefulResult / 4)
	}
	if queriesWithUseful > 0 {
		metrics.MeanTimeToFirstUseful = usefulLatency / time.Duration(queriesWithUseful)
	}
	return metrics, nil
}

func set(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func prefix(values []string, limit int) []string {
	if len(values) < limit {
		return values
	}
	return values[:limit]
}

func prefixFloat(values []float64, limit int) []float64 {
	if len(values) < limit {
		return values
	}
	return values[:limit]
}

func dcg(grades []float64) float64 {
	var result float64
	for index, grade := range grades {
		result += (math.Pow(2, grade) - 1) / math.Log2(float64(index)+2)
	}
	return result
}
