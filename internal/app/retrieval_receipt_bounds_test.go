package app

import "testing"

func TestRetrievalReceiptDecisionsAreBoundedAndKeepMeaningfulReasons(t *testing.T) {
	decisions := make([]RetrievalDecision, 0, 1000)
	for i := 0; i < 999; i++ {
		decisions = append(decisions, RetrievalDecision{MemoryID: "irrelevant", Reason: "not_relevant"})
	}
	decisions = append(decisions, RetrievalDecision{MemoryID: "included", Included: true, Reason: "authorized_relevant_fresh"})
	bounded, omitted := boundRetrievalDecisions(decisions, 256)
	if len(bounded) != 256 || omitted != 744 {
		t.Fatalf("bounded=%d omitted=%d", len(bounded), omitted)
	}
	found := false
	for _, decision := range bounded {
		if decision.MemoryID == "included" && decision.Included {
			found = true
		}
	}
	if !found {
		t.Fatal("bounded receipt dropped an included decision")
	}
}
