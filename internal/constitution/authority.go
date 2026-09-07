package constitution

import "sort"

// AuthorityLevel encodes Article II. Lower numeric values bind more strongly.
// The numbering is the contract: a claim at level N can never override a claim
// at any level below N, whatever the claim says about itself.
type AuthorityLevel int

const (
	AuthorityConstitution   AuthorityLevel = 1
	AuthorityHardPolicy     AuthorityLevel = 2
	AuthorityUserConstraint AuthorityLevel = 3
	AuthorityGoalContract   AuthorityLevel = 4
	AuthorityProjectPolicy  AuthorityLevel = 5
	AuthorityApprovedPlan   AuthorityLevel = 6
	AuthorityProcessState   AuthorityLevel = 7
	// AuthorityControlIntelligence is the highest level an AI may ever occupy.
	// It sits below every rule-bearing source above it, which is what makes
	// "any qualified AI may think for MARSHAL, no AI may redefine MARSHAL"
	// mechanical rather than aspirational.
	AuthorityControlIntelligence AuthorityLevel = 8
	AuthoritySpecializedAgent    AuthorityLevel = 9
	AuthorityProviderDefault     AuthorityLevel = 10
)

var authorityNames = map[AuthorityLevel]string{
	AuthorityConstitution:        "constitution",
	AuthorityHardPolicy:          "hard-policy",
	AuthorityUserConstraint:      "user-constraint",
	AuthorityGoalContract:        "goal-contract",
	AuthorityProjectPolicy:       "project-policy",
	AuthorityApprovedPlan:        "approved-plan",
	AuthorityProcessState:        "process-state",
	AuthorityControlIntelligence: "control-intelligence",
	AuthoritySpecializedAgent:    "specialized-agent",
	AuthorityProviderDefault:     "provider-default",
}

func (l AuthorityLevel) String() string {
	if name, ok := authorityNames[l]; ok {
		return name
	}
	return "unknown-authority"
}

// Valid reports whether the level is one of the ten defined ranks.
func (l AuthorityLevel) Valid() bool {
	_, ok := authorityNames[l]
	return ok
}

// IsAI reports whether the level is occupied by model reasoning. Claims from
// these levels are advisory: they inform a decision but never constitute one.
func (l AuthorityLevel) IsAI() bool {
	return l == AuthorityControlIntelligence || l == AuthoritySpecializedAgent
}

// Claim is an assertion made by one authority source about what should happen.
type Claim struct {
	// Level is the rank of the source making the claim.
	Level AuthorityLevel `json:"level"`
	// Source identifies the concrete origin (a policy ID, a goal revision, a
	// model identifier). It is recorded for provenance, never for ranking:
	// ranking depends only on Level, so a source cannot promote itself by
	// naming itself convincingly.
	Source string `json:"source"`
	// Directive is the position taken.
	Directive string `json:"directive"`
	// Permits reports whether this claim would allow the action.
	Permits bool `json:"permits"`
}

// Conflict records that a lower authority contradicted a higher one.
type Conflict struct {
	Winner AuthorityLevel `json:"winner"`
	Loser  AuthorityLevel `json:"loser"`
	// Overreach is true when the losing claim came from an AI level and
	// attempted to permit something a rule-bearing level forbade. This is the
	// signature of an attempted self-authorization.
	Overreach bool   `json:"overreach"`
	Detail    string `json:"detail"`
}

// Resolution is the outcome of applying Article II to a set of claims.
type Resolution struct {
	// Permitted is the position of the highest-ranking claim present.
	Permitted bool `json:"permitted"`
	// Governing is the level that decided the outcome.
	Governing AuthorityLevel `json:"governing"`
	// Conflicts lists every lower-authority claim that contradicted the
	// governing one. They are reported, never applied.
	Conflicts []Conflict `json:"conflicts,omitempty"`
}

// AIOverreach reports whether any AI-level claim tried to permit what a
// rule-bearing level denied.
func (r Resolution) AIOverreach() bool {
	for _, conflict := range r.Conflicts {
		if conflict.Overreach {
			return true
		}
	}
	return false
}

// Resolve applies the authority hierarchy. The claim from the strongest level
// present decides the outcome; every contradicting weaker claim is recorded as
// a conflict so the disagreement is visible rather than silently discarded.
//
// With no claims at all the result is a denial: absence of any authorising
// source is not permission (Article X, fail closed).
func Resolve(claims []Claim) Resolution {
	valid := make([]Claim, 0, len(claims))
	for _, claim := range claims {
		if claim.Level.Valid() {
			valid = append(valid, claim)
		}
	}
	if len(valid) == 0 {
		return Resolution{Permitted: false, Governing: AuthorityConstitution}
	}
	sort.SliceStable(valid, func(a, b int) bool { return valid[a].Level < valid[b].Level })

	governing := valid[0]
	resolution := Resolution{Permitted: governing.Permits, Governing: governing.Level}
	for _, claim := range valid[1:] {
		if claim.Permits == governing.Permits {
			continue
		}
		resolution.Conflicts = append(resolution.Conflicts, Conflict{
			Winner:    governing.Level,
			Loser:     claim.Level,
			Overreach: claim.Level.IsAI() && claim.Permits && !governing.Permits,
			Detail:    claim.Source + " proposed " + claim.Directive,
		})
	}
	return resolution
}
