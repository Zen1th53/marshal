//go:build !governed

package store

import "testing"

// A real reviewer answer cites file lines ("calc.py:2") beside the offered
// references and leaves out an "obligation:<id>" self-reference. That is not
// a refusal: each assessment with one concrete offered reference keeps its
// status. An assessment with no offered reference still refuses.
func TestArchitectureOutcomeIgnoresUnofferedCitations(t *testing.T) {
	input := ProjectArchitectureReviewInput{
		Obligations:  []string{"component:calc", "goal:outcome"},
		EvidenceRefs: []string{"workspace:tree", "evidence:runtime-check"},
	}
	observation := func(refs ...[]string) ProjectArchitectureObservation {
		return ProjectArchitectureObservation{
			Invocation: ProjectArchitectureInvocation{ObservedModel: "claude-opus-5-5", SessionID: "session", SourceHead: "head"},
			Assessments: []ProjectArchitectureAssessment{
				{ObligationID: "component:calc", Status: "PASS", Reason: "calc.py:2 returns a + b", EvidenceRefs: refs[0]},
				{ObligationID: "goal:outcome", Status: "PASS", Reason: "the test passes", EvidenceRefs: refs[1]},
			},
		}
	}
	outcome, err := projectArchitectureOutcome(input, observation(
		[]string{"calc.py:1", "calc.py:2", "workspace:tree", "workspace:tree"},
		[]string{"calc.py:2", "evidence:runtime-check"},
	))
	if err != nil || outcome != "COMPATIBLE" {
		t.Fatalf("real reviewer answer refused: %q %v", outcome, err)
	}
	outcome, err = projectArchitectureOutcome(input, observation(
		[]string{"calc.py:2", "test_calc.py:5-7"},
		[]string{"evidence:runtime-check"},
	))
	if err != nil || outcome != "COMPATIBLE" {
		t.Fatalf("file-line citations into the offered workspace refused: %q %v", outcome, err)
	}
	for _, refs := range [][]string{{"obligation:component:calc"}, {"file:invented"}, {"../etc/passwd:1x"}} {
		if _, err = projectArchitectureOutcome(input, observation(refs, []string{"evidence:runtime-check"})); err == nil {
			t.Fatalf("an assessment citing only %v qualified", refs)
		}
	}
	noWorkspace := input
	noWorkspace.EvidenceRefs = []string{"evidence:runtime-check"}
	if _, err = projectArchitectureOutcome(noWorkspace, observation([]string{"calc.py:2"}, []string{"evidence:runtime-check"})); err == nil {
		t.Fatal("a file-line citation counted without an offered workspace")
	}
}
