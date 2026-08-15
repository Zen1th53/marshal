package events

import "testing"

func TestT43A03DeliveryStateMachineAllowsOnlyCanonicalTransitions(t *testing.T) {
	allowed := map[[2]DeliveryState]bool{
		{StateProduced, StateValidated}:        true,
		{StateValidated, StateDurablyAppended}: true,
		{StateDurablyAppended, StatePublished}: true,
		{StatePublished, StateConsumed}:        true,
	}
	states := []DeliveryState{StateProduced, StateValidated, StateDurablyAppended, StatePublished, StateConsumed}
	for _, from := range states {
		for _, to := range states {
			if got, want := CanTransitionDelivery(from, to), allowed[[2]DeliveryState{from, to}]; got != want {
				t.Fatalf("CanTransitionDelivery(%q,%q)=%v want=%v", from, to, got, want)
			}
		}
	}
	if CanTransitionDelivery(DeliveryState("unknown"), StateValidated) || CanTransitionDelivery(StateProduced, DeliveryState("unknown")) {
		t.Fatal("unknown delivery state was accepted")
	}
}
