package app

import (
	"github.com/Zen1th53/marshal/internal/marshal"
	"strings"
	"testing"
)

func TestArchitectureReviewVerdict(t *testing.T) {
	r := marshal.Review{Verdict: marshal.VerdictAccept}
	if checkArchitectureReview(&r, true) == nil {
		t.Fatal("missing verdict accepted")
	}
	r.ArchitectureVerdict = "conforms"
	if err := checkArchitectureReview(&r, true); err != nil {
		t.Fatal(err)
	}
	r.ArchitectureMismatches = []string{"outside.go:no-part: not owned"}
	if err := checkArchitectureReview(&r, true); err != nil || r.Verdict != marshal.VerdictReturn {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestTaskArchitectureNoContract(t *testing.T) {
	s := &MarshalService{}
	text, required := s.taskArchitecture(t.Context(), marshal.Task{})
	if required || !strings.Contains(text, "ARCHITECTURE FOR THIS TASK") {
		t.Fatalf("%q %v", text, required)
	}
}
