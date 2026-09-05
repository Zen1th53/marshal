package interpretation

import "github.com/Zen1th53/marshal/internal/model"

type Interpretation = model.Interpretation
type InterpretationRequirement = model.InterpretationRequirement
type InterpretationResolution = model.InterpretationResolution
type DivergenceKind = model.DivergenceKind
type Divergence = model.Divergence

const (
	DivergenceScope       = model.DivergenceScope
	DivergenceOutcome     = model.DivergenceOutcome
	DivergenceConstraint  = model.DivergenceConstraint
	DivergenceDestructive = model.DivergenceDestructive
	DivergenceAssumptions = model.DivergenceAssumptions
)
