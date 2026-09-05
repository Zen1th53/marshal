package interpretation

import (
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Comparator performs cross-interpretation analysis to catch intent divergence without anchoring.
type Comparator struct{}

func NewComparator() *Comparator {
	return &Comparator{}
}

// Compare audits collected interpretations against each other and against requirements.
// Returns a definitive resolution: READY or NEEDS_INPUT with concrete questions.
// Never exposes artificial understanding percentages.
func (c *Comparator) Compare(
	sessionID, goalID string,
	revision int64,
	req model.InterpretationRequirement,
	interps []model.Interpretation,
) model.InterpretationResolution {
	now := time.Now().UTC()
	resID := fmt.Sprintf("res-%s-%d-%d", goalID, revision, now.UnixNano())

	res := model.InterpretationResolution{
		ID:             resID,
		SessionID:      sessionID,
		GoalID:         goalID,
		GoalRevision:   revision,
		RequiredCount:  req.MinInterpreters,
		CollectedCount: len(interps),
		ResolvedAt:     now,
	}

	// If fewer than required interpretations collected, needs input or pending collection
	if len(interps) < req.MinInterpreters {
		res.State = model.GoalNeedsInput
		res.Message = fmt.Sprintf("Awaiting required independent interpretations: collected %d, need %d",
			len(interps), req.MinInterpreters)
		res.ConcreteQuestions = append(res.ConcreteQuestions, model.UnresolvedDecision{
			ID:           fmt.Sprintf("dec-missing-interp-%d", now.UnixNano()),
			Question:     "Additional independent interpretation required before execution can proceed safely",
			Impact:       req.Reason,
			RequiresUser: false,
		})
		return res
	}

	// Single interpretation case (low-risk / R0 / R1)
	if len(interps) == 1 {
		inp := interps[0]
		if inp.IsDestructive {
			res.State = model.GoalNeedsInput
			res.Message = "Task was flagged as potentially destructive; requires user confirmation or secondary interpretation"
			res.Divergences = append(res.Divergences, Divergence{
				Kind:        DivergenceDestructive,
				Field:       "is_destructive",
				Description: "Single interpreter identified potentially destructive operation",
				Question:    "Confirm destructive action or clarify non-destructive intent",
				Impact:      "High risk of irreversible data or state loss",
				Options:     []string{"Proceed with destructive action", "Abort", "Modify task scope to non-destructive"},
			})
			res.ConcreteQuestions = append(res.ConcreteQuestions, model.UnresolvedDecision{
				ID:           fmt.Sprintf("dec-destr-%d", now.UnixNano()),
				Question:     "Single interpreter identified potentially destructive operation; please confirm intent",
				Impact:       "Potential irreversible state modification",
				Options:      []string{"Confirm destructive action", "Cancel task", "Restrict scope"},
				RequiresUser: true,
			})
			return res
		}

		if len(inp.Ambiguities) > 0 {
			res.State = model.GoalNeedsInput
			res.Message = "Interpreter detected material ambiguity requiring user clarification"
			for idx, amb := range inp.Ambiguities {
				res.ConcreteQuestions = append(res.ConcreteQuestions, model.UnresolvedDecision{
					ID:           fmt.Sprintf("dec-amb-%d-%d", now.UnixNano(), idx),
					Question:     amb,
					Impact:       "Prevents misaligned implementation",
					RequiresUser: true,
				})
			}
			return res
		}

		// Single low-risk interpretation is READY
		res.State = model.GoalReady
		res.ConsensusConfirmed = true
		res.Message = "Task intent validated (low risk, single interpreter sufficient)"
		return res
	}

	// Multi-interpretation comparison (>= 2)
	divergences := c.findDivergences(interps)
	if len(divergences) > 0 {
		res.State = model.GoalNeedsInput
		res.Divergences = divergences
		res.Message = fmt.Sprintf("Material divergence detected across %d independent interpretations", len(interps))

		for idx, div := range divergences {
			res.ConcreteQuestions = append(res.ConcreteQuestions, model.UnresolvedDecision{
				ID:           fmt.Sprintf("dec-div-%d-%d", now.UnixNano(), idx),
				Question:     div.Question,
				Impact:       div.Impact,
				Options:      div.Options,
				RequiresUser: true,
			})
		}
		return res
	}

	// Check if any interpreter raised explicit ambiguities
	var explicitAmbiguities []string
	for _, inp := range interps {
		explicitAmbiguities = append(explicitAmbiguities, inp.Ambiguities...)
	}
	if len(explicitAmbiguities) > 0 {
		res.State = model.GoalNeedsInput
		res.Message = "Interpreters identified unresolved ambiguities requiring operator clarification"
		for idx, amb := range explicitAmbiguities {
			res.ConcreteQuestions = append(res.ConcreteQuestions, model.UnresolvedDecision{
				ID:           fmt.Sprintf("dec-amb-%d-%d", now.UnixNano(), idx),
				Question:     amb,
				Impact:       "Clarifies intent before autonomous execution",
				RequiresUser: true,
			})
		}
		return res
	}

	// Agreement reached across all independent interpreters
	// Note: Consensus alone does not verify factual claims; MARSHAL evidence still required.
	res.State = model.GoalReady
	res.ConsensusConfirmed = true
	res.Message = "Consensus confirmed across independent interpretations without anchoring"
	return res
}

func (c *Comparator) findDivergences(interps []model.Interpretation) []Divergence {
	var divs []Divergence

	// 1. Destructive action disagreement
	hasDestructive := false
	hasNonDestructive := false
	for _, inp := range interps {
		if inp.IsDestructive {
			hasDestructive = true
		} else {
			hasNonDestructive = true
		}
	}
	if hasDestructive && hasNonDestructive {
		divs = append(divs, Divergence{
			Kind:        DivergenceDestructive,
			Field:       "is_destructive",
			Description: "Interpreters disagree on whether this task involves destructive actions",
			Question:    "Does this task intend to perform destructive operations (e.g. deleting files, dropping schemas)?",
			Impact:      "Risk of unintended data loss or broken baseline",
			Options:     []string{"Yes, destructive actions permitted", "No, non-destructive only"},
		})
	}

	// 2. Scope mismatch (disjoint or substantially divergent target scopes)
	allScopes := make(map[string]int)
	for _, inp := range interps {
		seenInThis := make(map[string]bool)
		for _, s := range inp.Scope {
			norm := strings.TrimSpace(strings.ToLower(s))
			if !seenInThis[norm] {
				seenInThis[norm] = true
				allScopes[norm]++
			}
		}
	}
	for s, count := range allScopes {
		// If a scope is only included by 1 of multiple interpreters, check if it's sensitive
		if count < len(interps) {
			if strings.Contains(s, "delete") || strings.Contains(s, "database") ||
				strings.Contains(s, "auth") || strings.Contains(s, "public-api") || strings.Contains(s, "secret") {
				divs = append(divs, Divergence{
					Kind:        DivergenceScope,
					Field:       "scope",
					Description: fmt.Sprintf("Interpreters disagree on sensitive scope %q", s),
					Question:    fmt.Sprintf("Should sensitive scope %q be modified as part of this task?", s),
					Impact:      "Unintended modification of sensitive subsystems",
					Options:     []string{fmt.Sprintf("Include %s in scope", s), fmt.Sprintf("Exclude %s from scope", s)},
				})
			}
		}
	}

	// 3. DesiredOutcome / ExpectedArtifact material mismatch
	// If one interpreter expects a complete rewrite and another expects an in-place bug fix
	for i := 0; i < len(interps)-1; i++ {
		for j := i + 1; j < len(interps); j++ {
			a := strings.ToLower(interps[i].DesiredOutcome)
			b := strings.ToLower(interps[j].DesiredOutcome)

			aRewrite := strings.Contains(a, "rewrite") || strings.Contains(a, "replace") || strings.Contains(a, "recreate")
			bRewrite := strings.Contains(b, "rewrite") || strings.Contains(b, "replace") || strings.Contains(b, "recreate")

			if aRewrite != bRewrite {
				divs = append(divs, Divergence{
					Kind:        DivergenceOutcome,
					Field:       "desired_outcome",
					Description: fmt.Sprintf("Interpreter %s plans a full rewrite/replacement while %s plans in-place maintenance", interps[i].Author.AgentID, interps[j].Author.AgentID),
					Question:    "Do you want a full replacement/rewrite or an in-place modification?",
					Impact:      "Rewrites risk discarding uncommitted or existing working architecture",
					Options:     []string{"In-place modification (preserve existing code)", "Full replacement/rewrite"},
				})
			}
		}
	}

	// 4. Contradictory assumptions
	for i := 0; i < len(interps)-1; i++ {
		for j := i + 1; j < len(interps); j++ {
			for _, asmI := range interps[i].Assumptions {
				for _, asmJ := range interps[j].Assumptions {
					normI := strings.ToLower(asmI.Text)
					normJ := strings.ToLower(asmJ.Text)
					// Look for direct negation patterns
					if (strings.Contains(normI, "allow") && strings.Contains(normJ, "disallow")) ||
						(strings.Contains(normI, "no auth") && strings.Contains(normJ, "require auth")) ||
						(strings.Contains(normI, "unauthenticated") && strings.Contains(normJ, "authenticated only")) {
						divs = append(divs, Divergence{
							Kind:        DivergenceAssumptions,
							Field:       "assumptions",
							Description: fmt.Sprintf("Contradictory assumptions between %s and %s: %q vs %q", interps[i].Author.AgentID, interps[j].Author.AgentID, asmI.Text, asmJ.Text),
							Question:    fmt.Sprintf("Clarify intent regarding: %s vs %s", asmI.Text, asmJ.Text),
							Impact:      "Security and behavioral inconsistency",
							Options:     []string{asmI.Text, asmJ.Text},
						})
					}
				}
			}
		}
	}

	return divs
}
