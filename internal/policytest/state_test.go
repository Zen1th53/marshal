package policytest

import (
	"errors"
	"testing"
)

func TestRunStateMatrix(t *testing.T) {
	states := []RunState{StateLoaded, StateValidated, StateExecuted, StatePassed, StateFailed}
	legal := map[[2]RunState]bool{
		{StateLoaded, StateValidated}:   true,
		{StateValidated, StateExecuted}: true,
		{StateExecuted, StatePassed}:    true,
		{StateExecuted, StateFailed}:    true,
	}
	for _, source := range states {
		for _, target := range states {
			err := ValidateTransition(source, target)
			wantLegal := legal[[2]RunState{source, target}]
			if wantLegal && err != nil {
				t.Errorf("%s -> %s: error = %v, want legal", source, target, err)
			}
			if !wantLegal && !errors.Is(err, ErrIllegalTransition) {
				t.Errorf("%s -> %s: error = %v, want illegal transition", source, target, err)
			}
		}
	}
	if err := ValidateTransition(RunState("unknown"), StateLoaded); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("unknown source error = %v", err)
	}
	if err := ValidateTransition(StateLoaded, RunState("unknown")); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("unknown target error = %v", err)
	}
}
