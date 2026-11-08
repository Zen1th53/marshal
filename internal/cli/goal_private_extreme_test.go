//go:build !governed

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestExtremeGoalCLIUsesCanonicalCommandsAndExactConfirmation(t *testing.T) {
	dir := gitDirectory(t)
	if _, err := app.Bootstrap(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "goal-input.json")
	if err := os.WriteFile(file, []byte(`{"request":"Fix the README typo. Do not change other files.","criteria":["README typo fixed"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runCLI(t, dir, "--json", "goal", "create", "SESSION-real-goal", "--file", file)
	if code != 0 {
		t.Fatalf("create: %d %s", code, stderr)
	}
	var goal model.GoalContract
	if err := json.Unmarshal([]byte(stdout), &goal); err != nil {
		t.Fatal(err)
	}
	if goal.ProjectID == "" || goal.RequestDigest == "" || goal.Confirmation != model.ConfirmationPending || goal.Revision != 2 || len(goal.Constraints) == 0 {
		t.Fatalf("source lost canonical formation: %+v", goal)
	}
	_, _, code = runCLI(t, dir, "goal", "approve", "SESSION-real-goal", "--revision", "1")
	if code == 0 {
		t.Fatal("stale review approved")
	}
	stdout, stderr, code = runCLI(t, dir, "--json", "goal", "approve", "SESSION-real-goal", "--revision", "2")
	if code != 0 {
		t.Fatalf("approve: %d %s", code, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &goal); err != nil {
		t.Fatal(err)
	}
	if goal.Confirmation != model.ConfirmationApproved || goal.Revision != 3 {
		t.Fatalf("canonical confirmation missing: %+v", goal)
	}
}

func TestExtremeModelPlanCLIRefusesUnconfirmedGoalBeforeProvider(t *testing.T) {
	dir := gitDirectory(t)
	if _, err := app.Bootstrap(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "goal-input.json")
	if err := os.WriteFile(file, []byte(`{"request":"Read README.md","criteria":["README reviewed"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runCLI(t, dir, "goal", "create", "SESSION-unconfirmed", "--file", file)
	if code != 0 {
		t.Fatal(stderr)
	}
	_, _, code = runCLI(t, dir, "plan", "draft", "SESSION-unconfirmed", "--provider", "provider-must-not-start")
	if code == 0 {
		t.Fatal("unconfirmed goal reached model")
	}
}
