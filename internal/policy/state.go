package policy

// State is the explicit lifecycle state of a persisted policy version.
// Lifecycle state does not grant authorization or activate runtime behavior.
type State string

const (
	StateLoaded     State = "loaded"
	StateValidated  State = "validated"
	StateActive     State = "active"
	StateSuperseded State = "superseded"
)

func (s State) Valid() bool {
	switch s {
	case StateLoaded, StateValidated, StateActive, StateSuperseded:
		return true
	default:
		return false
	}
}

// CanTransition reports the complete authoritative transition matrix.
func CanTransition(from, to State) bool {
	return (from == StateLoaded && to == StateValidated) ||
		(from == StateValidated && to == StateActive) ||
		(from == StateActive && to == StateSuperseded)
}
