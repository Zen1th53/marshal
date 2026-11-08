package evidence

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Cost-driven delivery policy (recipe locator/1, docs/COST-MODEL.md).
//
// Pushing t tokens at turn 0 of an agent run with N model calls costs
// about
//
//	c(t) = t · (w_w + w_r·(N−1) + w_o·β_o)
//
// in input-token equivalents: the tokens are written to the prompt cache
// once (w_w), read back on every later call (w_r), and the agent's own
// output grows by β_o tokens per pushed token (fitted on the pilot). An
// item that locates a file the agent needs saves part of the agent's
// localization turns; its expected value is p · V, where p is the
// calibrated probability that the item names a needed file not already
// listed and V the localization spend saved per needed file located.
// The recipe takes an item iff p·V > c(bytes/bytes-per-token), in rank
// order, and pushes nothing (no entries) when the sum of the margins does
// not pay for the header. Everything here is a deterministic function of
// the rank order, the file paths and the rendered byte counts.

// CostProfile holds the price weights and behaviour constants of one
// agent family. Weights are relative to the provider's base input price.
type CostProfile struct {
	Name string `json:"name"`
	// Price weights relative to base input.
	Write float64 `json:"w_write"` // first time a token enters the context (cache write, or fresh input)
	Read  float64 `json:"w_read"`  // cached re-read on each later call
	Out   float64 `json:"w_out"`   // output token
	// Turns is the expected number of model calls N.
	Turns float64 `json:"turns"`
	// OutPerToken (β_o) is the agent's extra output per pushed token.
	OutPerToken float64 `json:"out_per_pushed_token"`
	// BytesPerToken converts rendered bytes to tokens.
	BytesPerToken float64 `json:"bytes_per_token"`
	// FileValue (V) is the localization spend saved per needed file the
	// push locates, in input-token equivalents.
	FileValue float64 `json:"file_value"`
	// Need is the calibrated p(item names a new needed file).
	Need NeedModel `json:"need"`
	// MaxLines caps locator lines (a safety bound, not the optimum).
	MaxLines int `json:"max_lines"`
}

// NeedModel is a logistic model of p(the candidate at rank r names a
// needed file that no higher-ranked candidate named):
//
//	logit p = Intercept + LogRank·ln r + NewFile·[file not yet listed] + TestFile·[_test.go]
type NeedModel struct {
	Intercept float64 `json:"intercept"`
	LogRank   float64 `json:"log_rank"`
	NewFile   float64 `json:"new_file"`
	TestFile  float64 `json:"test_file"`
}

// P returns the modelled probability.
func (m NeedModel) P(rank int, newFile, testFile bool) float64 {
	z := m.Intercept + m.LogRank*math.Log(float64(max(rank, 1)))
	if newFile {
		z += m.NewFile
	}
	if testFile {
		z += m.TestFile
	}
	return 1 / (1 + math.Exp(-z))
}

// TokenCost is c(t) for bytes rendered bytes.
func (p CostProfile) TokenCost(bytes int) float64 {
	t := float64(bytes) / p.BytesPerToken
	return t * (p.Write + p.Read*math.Max(p.Turns-1, 0) + p.Out*p.OutPerToken)
}

// Profiles are the fitted profiles (bench/costmodel on the pilot ledgers,
// bench/results/agent-pilot; see docs/COST-MODEL.md §4 for the fit and
// its uncertainty). They are versioned with the recipe: changing a
// number changes dossiers, so it changes RecipeLocator.
var Profiles = map[string]CostProfile{
	// Claude Opus 5.5 under Claude Code: 1-hour cache writes 2×, cache
	// reads 0.05×, output 5× base input (Anthropic price list
	// 2026-11-06; Claude Code's cost_usd matches these weights exactly).
	"opus": {
		Name: "opus", Write: 2, Read: 0.05, Out: 5, Turns: 7, OutPerToken: 0.21, BytesPerToken: 2.0,
		FileValue: 2600, MaxLines: 12,
		Need: NeedModel{Intercept: -1.382, LogRank: -1.388, NewFile: 2.326, TestFile: -0.362},
	},
	// gpt-6.1-sol under Codex CLI: uncached input 1×, cached input
	// 0.05×, output 5× (OpenAI price list 2026-11-06); Codex reports no
	// cache writes and one aggregated turn, so N is the tool-call count.
	// Fitted on the complete Codex pilot (24 + 24 runs); localization =
	// search/read calls anywhere in the run (selected by leave-one-task-
	// out error, docs/COST-MODEL.md §5).
	"codex": {
		Name: "codex", Write: 1, Read: 0.05, Out: 5, Turns: 13, OutPerToken: 0, BytesPerToken: 1.87,
		FileValue: 2800, MaxLines: 12,
		Need: NeedModel{Intercept: -1.510, LogRank: -1.306, NewFile: 2.951, TestFile: 0.683},
	},
}

// DefaultProfile is used when a request names none.
const DefaultProfile = "opus"

// LookupProfile returns the named profile ("" = DefaultProfile).
func LookupProfile(name string) (CostProfile, error) {
	if name == "" {
		name = DefaultProfile
	}
	p, ok := Profiles[name]
	if !ok {
		names := make([]string, 0, len(Profiles))
		for n := range Profiles {
			names = append(names, n)
		}
		sort.Strings(names)
		return CostProfile{}, fmt.Errorf("evidence: unknown cost profile %q (have %s)", name, strings.Join(names, ", "))
	}
	return p, nil
}

// PlanItem is one candidate line offered to the planner.
type PlanItem struct {
	Rank      int    // 1-based candidate rank
	Path      string // file path
	Bytes     int    // rendered bytes of the line
	FileBytes int    // bytes of the file header line, paid by the first line taken in a file
	TestFile  bool
}

// PlanDecision is the planner's verdict on one item.
type PlanDecision struct {
	Take   bool
	P      float64 // modelled need probability
	Value  float64 // p·V
	Cost   float64 // c(bytes)
	Margin float64 // Value − Cost
}

// PlanLocator applies the break-even rule in rank order, taking at most
// maxLines lines (0 = the profile's MaxLines) within budget bytes. newFile
// is evaluated against the files already taken. It returns the decisions
// and the expected net saving (Σ margins − header cost); when that is
// not positive, nothing is taken.
func (p CostProfile) PlanLocator(items []PlanItem, headerBytes, budget, maxLines int) ([]PlanDecision, float64) {
	if maxLines <= 0 || maxLines > p.MaxLines {
		maxLines = p.MaxLines
	}
	out := make([]PlanDecision, len(items))
	listed := map[string]bool{}
	used := headerBytes
	net := -p.TokenCost(headerBytes)
	lines := 0
	for i, it := range items {
		newFile := !listed[it.Path]
		pr := p.Need.P(it.Rank, newFile, it.TestFile)
		size := it.Bytes
		if newFile {
			size += it.FileBytes
		}
		d := PlanDecision{P: pr, Value: pr * p.FileValue, Cost: p.TokenCost(size)}
		if !newFile {
			// The need model values locating a file; a second line in a
			// listed file has no measured value.
			d.Value = 0
		}
		d.Margin = d.Value - d.Cost
		if d.Margin > 0 && used+size <= budget && lines < maxLines {
			d.Take = true
			used += size
			net += d.Margin
			listed[it.Path] = true
			lines++
		}
		out[i] = d
	}
	if net <= 0 {
		for i := range out {
			out[i].Take = false
		}
		return out, 0
	}
	return out, net
}
