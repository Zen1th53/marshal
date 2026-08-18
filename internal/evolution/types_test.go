package evolution

import "testing"

func TestIndividualStruct(t *testing.T) {
	ind := Individual{ID: "ind-1", ChangeID: "ch-1", Fitness: 98.5}
	if ind.ID != "ind-1" {
		t.Fatalf("expected ind-1, got %s", ind.ID)
	}
}
