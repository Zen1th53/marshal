//go:build !governed

package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// Drafting is a real model turn, followed by canonical Process 04 formation.
// This command neither approves nor executes the resulting plan.
func (c command) draftModelPlan(ctx context.Context, runtime *app.Runtime, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%w: plan draft SESSION --provider PROVIDER --mode standard|ultra", model.ErrInvalid)
	}
	provider, mode := "codex", "ultra"
	workerModels := map[string]*string{}
	fs := flag.NewFlagSet("plan draft", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	fs.StringVar(&provider, "provider", provider, "Marshal model provider")
	fs.StringVar(&mode, "mode", mode, "approved execution mode")
	for _, worker := range []string{"codex", "claude", "opencode"} {
		initial := ""
		if worker == "opencode" {
			initial = strings.TrimSpace(os.Getenv("MARSHAL_OPENCODE_MODEL"))
		}
		workerModels[worker] = fs.String(worker+"-model", initial, "explicit worker model; otherwise use the canonical harness profile")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || (mode != string(plan.ModeStandard) && mode != string(plan.ModeUltra)) {
		return model.ErrInvalid
	}
	goal, err := runtime.Store().GetActiveGoalContract(ctx, args[0])
	if err != nil {
		return err
	}
	projectID := projectid.ID(runtime.ProjectIdentity())
	if validation := plan.ValidateGoal(goal, projectID); validation.Blocked() {
		return fmt.Errorf("%w: confirmed canonical goal required: %s", model.ErrGoalInvalid, strings.Join(validation.Detail, "; "))
	}
	cli := &app.MarshalCLI{Provider: provider, Dir: runtime.ProjectRoot(), ProjectID: string(projectID)}
	criteria, _ := json.Marshal(goal.SuccessCriteria)
	draft, err := cli.Draft(ctx, goal.OriginalRequest+" Governed work only. Use exactly one of these canonical criteria verbatim per task, and cover all of them: "+string(criteria)+". Provide executable checks covering each criterion. Do not perform work.")
	if err != nil {
		return err
	}
	request := app.CreatePlanRequest{SessionID: args[0], ProjectID: projectID, Mode: plan.Mode(mode), Tasks: draft.Plan.Tasks, Checks: draft.Plan.Checks, TaskProviders: map[string]string{}}
	seen := map[string]bool{}
	for _, task := range draft.Tasks {
		for i := range request.Tasks {
			if request.Tasks[i].ID == task.PlanTaskID {
				request.Tasks[i].Mutating = task.Type == "change"
			}
		}
		request.TaskProviders[task.PlanTaskID] = task.Worker
		if seen[task.Worker] {
			continue
		}
		seen[task.Worker] = true
		modelName := ""
		if selected := workerModels[task.Worker]; selected != nil {
			modelName = strings.TrimSpace(*selected)
		}
		if modelName == "" {
			name := task.Worker
			if name == "claude" {
				name = "claude-code"
			}
			if current, readErr := runtime.Store().GetHarnessProfile(ctx, name); readErr == nil && current != nil {
				modelName = current.DefaultModel
			}
		}
		if modelName == "" {
			return fmt.Errorf("%w: explicit --%s-model is required without a canonical model profile", model.ErrInvalid, task.Worker)
		}
		profile, err := runtime.VerifyMarshalWorker(ctx, task.Worker, modelName)
		if err != nil {
			return err
		}
		capacity := goalintake.UnknownCapacity(task.Worker, true)
		request.Candidates = append(request.Candidates, goalintake.Candidate{Provider: task.Worker, Model: profile.DefaultModel, Capacity: capacity, Governance: constitution.GovernanceVerified})
		request.HarnessCandidates = append(request.HarnessCandidates, plan.HarnessCandidate{Profile: profile, InstalledVersion: profile.InstalledVersion, Provider: task.Worker, Capacity: capacity})
	}
	created, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		return err
	}
	return c.print(created, fmt.Sprintf("model-drafted plan=%s version=%d state=%s; review before approving", created.ID, created.Version, created.State))
}
