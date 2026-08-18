package decision

import "testing"

func TestDecisionStatus(t *testing.T) {
	if !StatusAccepted.Valid() {
		t.Fatal("accepted status invalid")
	}
	if Status("INVALID").Valid() {
		t.Fatal("invalid status accepted")
	}
}
