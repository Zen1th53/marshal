package policytest

// RunState is the closed lifecycle vocabulary for one persisted policy-test
// run. ResultStatus remains a separate observation outcome.
type RunState string

const (
	StateLoaded    RunState = "loaded"
	StateValidated RunState = "validated"
	StateExecuted  RunState = "executed"
	StatePassed    RunState = "passed"
	StateFailed    RunState = "failed"
)

func (s RunState) Valid() bool {
	switch s {
	case StateLoaded, StateValidated, StateExecuted, StatePassed, StateFailed:
		return true
	default:
		return false
	}
}

func ValidateState(s RunState) error {
	if !s.Valid() {
		return ErrStateInvalid
	}
	return nil
}

// ValidateTransition is the single canonical lifecycle matrix.
func ValidateTransition(source, target RunState) error {
	if !source.Valid() || !target.Valid() {
		return ErrStateInvalid
	}
	switch {
	case source == StateLoaded && target == StateValidated:
		return nil
	case source == StateValidated && target == StateExecuted:
		return nil
	case source == StateExecuted && target == StatePassed:
		return nil
	case source == StateExecuted && target == StateFailed:
		return nil
	default:
		return ErrIllegalTransition
	}
}
