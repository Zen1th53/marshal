package app

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// Runs started before the branch prefix was renamed keep their branches.
func TestMarshalTaskIdentityAcceptsLegacyPrefix(t *testing.T) {
	task := marshal.Task{PlanTaskID: "seams", Branch: marshalTaskBranch("RUN-1", "seams", 0)}
	if err := validateMarshalTaskIdentity("RUN-1", task); err != nil {
		t.Fatal(err)
	}
	if statepath.LegacyRefName == "" {
		return
	}
	for _, branch := range []string{statepath.LegacyRefName + "/RUN-1/seams", statepath.LegacyRefName + "/RUN-1/r2/seams"} {
		task.Branch = branch
		if err := validateMarshalTaskIdentity("RUN-1", task); err != nil {
			t.Fatalf("%s: %v", branch, err)
		}
	}
	task.Branch = statepath.LegacyRefName + "/RUN-2/seams"
	if err := validateMarshalTaskIdentity("RUN-1", task); err == nil {
		t.Fatal("a legacy branch of another run was accepted")
	}
}
