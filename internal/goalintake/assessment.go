// Package goalintake turns a request into a canonical Goal.
//
// The shape of this package follows one observation from the pack: complexity
// and risk are different questions, and collapsing them loses the distinction
// that matters most. A large but wholly reversible refactor is complex and
// low-risk. A one-line change to production credentials is trivial and
// dangerous. A single score cannot say both, and a system that reports one
// number will eventually treat the second case like the first.
//
// So assessment produces ten independent dimensions and never reduces them to
// a score. Where they combine, they combine into a *decision* — whether
// confirmation is needed, whether clarification is warranted — which is a
// different thing from a number that implies precision nobody has.
//
// Process 03 is subordinate to Process 00. Nothing here authorizes anything:
// an assessment is evidence about a request, and the constitutional gate
// still decides what may be done with it.
package goalintake

import (
	"sort"
	"strings"
)

// Level is a coarse ordinal used by every dimension. Four values, because
// finer gradations would imply a precision that deterministic inspection of a
// request cannot support.
type Level string

const (
	LevelNone Level = "NONE"
	LevelLow  Level = "LOW"
	LevelMed  Level = "MEDIUM"
	LevelHigh Level = "HIGH"
	// LevelUnknown means the dimension could not be established. It is not a
	// synonym for NONE: not having assessed something is different from having
	// assessed it as harmless, and treating them alike is how a dangerous
	// request passes as routine.
	LevelUnknown Level = "UNKNOWN"
)

// rank orders levels for comparison. UNKNOWN ranks alongside HIGH, because a
// dimension nobody could establish must not be the reason a request is treated
// as safe.
func (l Level) rank() int {
	switch l {
	case LevelNone:
		return 0
	case LevelLow:
		return 1
	case LevelMed:
		return 2
	case LevelHigh, LevelUnknown:
		return 3
	default:
		return 3
	}
}

// AtLeast reports whether the level is at or above another.
func (l Level) AtLeast(other Level) bool { return l.rank() >= other.rank() }

// Established reports whether the dimension was actually assessed.
func (l Level) Established() bool { return l != LevelUnknown }

// Assessment is the independent evaluation of a request across every
// dimension the pack requires. There is deliberately no overall score field.
type Assessment struct {
	// Complexity is how much work this is. It says nothing about danger.
	Complexity Level `json:"complexity"`
	// Ambiguity is how much of the request is open to interpretation.
	Ambiguity Level `json:"ambiguity"`
	// BlastRadius is how much of the project the change reaches.
	BlastRadius Level `json:"blast_radius"`
	// Privilege is how much authority carrying it out would require.
	Privilege Level `json:"privilege"`
	// Reversibility is how hard it would be to undo. HIGH means hard to undo.
	Reversibility Level `json:"reversibility"`
	// ExternalEffects is how far the change reaches outside the project.
	ExternalEffects Level `json:"external_effects"`
	// DataSensitivity is how sensitive the data involved is.
	DataSensitivity Level `json:"data_sensitivity"`
	// DependencyDepth is how much the change depends on things not yet known.
	DependencyDepth Level `json:"dependency_depth"`
	// VerificationDifficulty is how hard it would be to prove the work correct.
	VerificationDifficulty Level `json:"verification_difficulty"`
	// OperationalCriticality is how much depends on the affected system
	// continuing to work.
	OperationalCriticality Level `json:"operational_criticality"`

	// Signals records the phrases that drove each finding, so an assessment
	// can be inspected rather than taken on faith.
	Signals []Signal `json:"signals,omitempty"`
}

// Signal is one observation that contributed to the assessment.
type Signal struct {
	Dimension string `json:"dimension"`
	// Phrase is the text in the request that triggered it.
	Phrase string `json:"phrase"`
	Level  Level  `json:"level"`
}

// Dimensions returns every dimension by name, in stable order, so surfaces can
// present them uniformly and tests can assert over all of them.
func (a Assessment) Dimensions() map[string]Level {
	return map[string]Level{
		"complexity":              a.Complexity,
		"ambiguity":               a.Ambiguity,
		"blast_radius":            a.BlastRadius,
		"privilege":               a.Privilege,
		"reversibility":           a.Reversibility,
		"external_effects":        a.ExternalEffects,
		"data_sensitivity":        a.DataSensitivity,
		"dependency_depth":        a.DependencyDepth,
		"verification_difficulty": a.VerificationDifficulty,
		"operational_criticality": a.OperationalCriticality,
	}
}

// SafetyDimensions are the dimensions that speak to danger rather than to
// effort. Complexity is deliberately absent: it is the dimension most likely
// to be mistaken for risk, and keeping it out of this list is what stops a
// large safe change being treated like a small dangerous one.
func (a Assessment) SafetyDimensions() map[string]Level {
	return map[string]Level{
		"blast_radius":            a.BlastRadius,
		"privilege":               a.Privilege,
		"reversibility":           a.Reversibility,
		"external_effects":        a.ExternalEffects,
		"data_sensitivity":        a.DataSensitivity,
		"operational_criticality": a.OperationalCriticality,
	}
}

// Elevated returns the safety dimensions at or above medium, in stable order.
// These are what a user should be shown when asked to confirm.
func (a Assessment) Elevated() []string {
	var elevated []string
	for name, level := range a.SafetyDimensions() {
		if level.AtLeast(LevelMed) {
			elevated = append(elevated, name)
		}
	}
	sort.Strings(elevated)
	return elevated
}

// Unestablished returns dimensions that could not be assessed.
func (a Assessment) Unestablished() []string {
	var unknown []string
	for name, level := range a.Dimensions() {
		if !level.Established() {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// RequiresConfirmation reports whether a human should approve before work
// begins.
//
// It reads only the safety dimensions. A complex request is not, by itself, a
// reason to interrupt someone: the work is large, not dangerous, and asking
// about it trains people to click through prompts. What warrants asking is
// reach, privilege, irreversibility, external effect, sensitive data or
// operational criticality.
func (a Assessment) RequiresConfirmation() bool {
	for _, level := range a.SafetyDimensions() {
		if level.AtLeast(LevelMed) {
			return true
		}
	}
	return false
}

// signalRule maps a phrase to the dimension it speaks to.
//
// This is deterministic keyword inspection, and its limits are the point.
// It cannot understand a request; it recognises the shapes that reliably
// indicate danger, and it errs toward noticing. Semantic judgement is the
// Control Intelligence's job, and its opinion arrives as advice that can raise
// an assessment but never lower one.
type signalRule struct {
	phrases   []string
	dimension string
	level     Level
}

var signalRules = []signalRule{
	// Reach.
	{[]string{"everything", "all files", "entire", "every ", "across the", "codebase-wide", "whole project"},
		"blast_radius", LevelHigh},
	// "refactor" is deliberately absent here. Refactoring describes effort,
	// not reach: refactoring one function and refactoring a whole service are
	// the same word for very different blast radii, and the reach shows up in
	// the scope wording instead. Counting it as reach made every large safe
	// change demand confirmation, which is how a confirmation prompt stops
	// meaning anything.
	{[]string{"rename across", "migrate the", "restructure the"}, "blast_radius", LevelMed},

	// Privilege.
	{[]string{"sudo", "root", "as administrator", "elevated", "chmod 777", "disable auth"},
		"privilege", LevelHigh},
	{[]string{"credential", "api key", "token", "secret", "password"}, "privilege", LevelMed},

	// Reversibility. HIGH means hard to undo.
	{[]string{"delete", "drop table", "rm -rf", "truncate", "wipe", "purge", "destroy", "force push"},
		"reversibility", LevelHigh},
	{[]string{"overwrite", "replace", "reset"}, "reversibility", LevelMed},

	// External effects.
	{[]string{"deploy", "publish", "release to", "push to production", "send email", "charge", "notify users"},
		"external_effects", LevelHigh},
	{[]string{"api call", "webhook", "upload", "third-party"}, "external_effects", LevelMed},

	// Data sensitivity.
	{[]string{"customer data", "personal data", "pii", "medical", "financial record", "private key"},
		"data_sensitivity", LevelHigh},
	{[]string{"user data", "database", "backup"}, "data_sensitivity", LevelMed},

	// Operational criticality.
	{[]string{"production", "prod ", "live system", "customer-facing"}, "operational_criticality", LevelHigh},
	{[]string{"staging", "ci", "pipeline", "build system"}, "operational_criticality", LevelMed},

	// Complexity — effort, deliberately not danger.
	{[]string{"rewrite", "redesign", "architecture", "from scratch"}, "complexity", LevelHigh},
	{[]string{"refactor", "multiple", "several"}, "complexity", LevelMed},

	// Ambiguity.
	{[]string{"somehow", "or something", "etc", "and so on", "maybe", "probably", "i think", "not sure"},
		"ambiguity", LevelHigh},
	{[]string{"better", "improve", "clean up", "optimize", "fix the issue", "make it nicer"},
		"ambiguity", LevelMed},

	// Dependency depth.
	{[]string{"depends on", "after we", "once the", "blocked by", "requires that"},
		"dependency_depth", LevelMed},

	// Verification difficulty.
	{[]string{"performance", "race condition", "flaky", "intermittent", "concurrency", "timing"},
		"verification_difficulty", LevelHigh},
}

// AssessRequest evaluates a request across every dimension.
//
// Assessment is deterministic and reads only the request text plus supplied
// project context. It does not call a model: the point of assessing before any
// AI is involved is that the baseline cannot be talked out of.
func AssessRequest(request string, context RequestContext) Assessment {
	lowered := " " + strings.ToLower(strings.Join(strings.Fields(request), " ")) + " "

	// Every dimension starts at NONE and is raised by evidence. Nothing here
	// lowers a dimension, so the order rules are evaluated in cannot change
	// the outcome.
	found := map[string]Level{}
	assessment := Assessment{}

	for _, rule := range signalRules {
		for _, phrase := range rule.phrases {
			if !strings.Contains(lowered, phrase) {
				continue
			}
			if existing, ok := found[rule.dimension]; !ok || rule.level.rank() > existing.rank() {
				found[rule.dimension] = rule.level
			}
			assessment.Signals = append(assessment.Signals, Signal{
				Dimension: rule.dimension, Phrase: strings.TrimSpace(phrase), Level: rule.level,
			})
		}
	}
	sort.SliceStable(assessment.Signals, func(a, b int) bool {
		if assessment.Signals[a].Dimension != assessment.Signals[b].Dimension {
			return assessment.Signals[a].Dimension < assessment.Signals[b].Dimension
		}
		return assessment.Signals[a].Phrase < assessment.Signals[b].Phrase
	})

	level := func(name string) Level {
		if value, ok := found[name]; ok {
			return value
		}
		return LevelNone
	}
	assessment.Complexity = level("complexity")
	assessment.Ambiguity = level("ambiguity")
	assessment.BlastRadius = level("blast_radius")
	assessment.Privilege = level("privilege")
	assessment.Reversibility = level("reversibility")
	assessment.ExternalEffects = level("external_effects")
	assessment.DataSensitivity = level("data_sensitivity")
	assessment.DependencyDepth = level("dependency_depth")
	assessment.VerificationDifficulty = level("verification_difficulty")
	assessment.OperationalCriticality = level("operational_criticality")

	// A request too short to say anything is ambiguous, not simple. Treating
	// an empty instruction as a low-ambiguity request would let it through
	// unclarified.
	if len(strings.Fields(request)) < 3 {
		assessment.Ambiguity = LevelHigh
	}

	// Project context raises dimensions that the request text cannot speak to.
	// It only ever raises: context is evidence about the environment, and a
	// calm-sounding request in a dangerous environment is not a calm request.
	if context.ProductionProject {
		assessment.OperationalCriticality = raise(assessment.OperationalCriticality, LevelHigh)
	}
	if !context.Recoverable {
		// Without a way back, everything is harder to undo than it looks.
		assessment.Reversibility = raise(assessment.Reversibility, LevelHigh)
	}
	if context.DirtyWorktree {
		assessment.Reversibility = raise(assessment.Reversibility, LevelMed)
	}
	if !context.ScopeKnown {
		assessment.BlastRadius = LevelUnknown
	}
	return assessment
}

// raise returns the higher of two levels.
func raise(current, candidate Level) Level {
	if candidate.rank() > current.rank() {
		return candidate
	}
	return current
}

// RequestContext is what MARSHAL knows about the project the request concerns.
// Every field is an observation from Process 02, not a claim from the request.
type RequestContext struct {
	// ProductionProject reports that this project affects a live system.
	ProductionProject bool
	// Recoverable reports that Git can undo what happens here.
	Recoverable bool
	// DirtyWorktree reports uncommitted work that a change could disturb.
	DirtyWorktree bool
	// ScopeKnown reports that the working scope was resolved.
	ScopeKnown bool
}
