package marshal

import "testing"

func TestCriterionAggregationFailsMixedAndDuplicateResults(t *testing.T) {
	task := Task{Checks: []Check{{Command: "check", Criteria: []string{"c"}}}, Criteria: []string{"c"}}
	h := HandIn{ResultCommit: "result", CheckResults: []CheckResult{{Command: "check", Criteria: []string{"c"}, ResultCommit: "result", Passed: true}, {Command: "check", Criteria: []string{"c"}, ResultCommit: "result", Passed: false}}}
	if met, _, _ := CriteriaMet(task, h); met != 0 {
		t.Fatal("mixed check results accepted")
	}
}

func TestUnchangedResultDependsOnTaskType(t *testing.T) {
	task := Task{BaseCommit: "base", Checks: []Check{{Command: "true", Criteria: []string{"c"}}}, Criteria: []string{"c"}}
	h := HandIn{TreeDigest: "tree", BaseCommit: "base", ResultCommit: "base", CheckResults: []CheckResult{{Command: "true", Criteria: []string{"c"}, ResultCommit: "base", TreeDigest: "tree", Passed: true}}}
	if err := ValidateHandIn(task, h); err == nil {
		t.Fatal("unchanged change task accepted")
	}
	for _, kind := range []TaskType{TaskInspection, TaskVerification} {
		task.Type = kind
		if err := ValidateHandIn(task, h); err != nil {
			t.Fatal(err)
		}
		h.CheckResults[0].Passed = false
		if err := ValidateHandIn(task, h); err == nil {
			t.Fatal("unchanged inspection without proof accepted")
		}
		h.CheckResults[0].Passed = true
	}
}
