//go:build !governed

package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func (c *command) privateGoalCommand(ctx context.Context, args []string) (bool, error) {
	if len(args) == 0 || (args[0] != "create" && args[0] != "approve") {
		return false, nil
	}
	if len(args) < 2 {
		return true, fmt.Errorf("%w: goal create SESSION --file INPUT.json | approve SESSION --revision N", model.ErrInvalid)
	}
	runtime, err := app.Open(ctx, c.root)
	if err != nil {
		return true, err
	}
	defer runtime.Close()
	if args[0] == "approve" {
		var revision int64
		fs := flag.NewFlagSet("goal approve", flag.ContinueOnError)
		fs.SetOutput(c.stderr)
		fs.Int64Var(&revision, "revision", 0, "exact reviewed goal revision")
		if err := fs.Parse(args[2:]); err != nil {
			return true, err
		}
		if revision < 1 || fs.NArg() != 0 {
			return true, model.ErrInvalid
		}
		goal, err := runtime.ApproveGoal(ctx, args[1], revision)
		if err != nil {
			return true, err
		}
		return true, c.print(goal, fmt.Sprintf("goal=%s revision=%d confirmation=%s", goal.ID, goal.Revision, goal.Confirmation))
	}
	var inputFile string
	fs := flag.NewFlagSet("goal create", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	fs.StringVar(&inputFile, "file", "", "goal request and explicit acceptance criteria")
	if err := fs.Parse(args[2:]); err != nil {
		return true, err
	}
	if inputFile == "" || fs.NArg() != 0 {
		return true, model.ErrInvalid
	}
	body, err := os.ReadFile(inputFile)
	if err != nil {
		return true, err
	}
	var input struct {
		Request  string   `json:"request"`
		Criteria []string `json:"criteria"`
		DoNotDo  []string `json:"do_not_do"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		return true, err
	}
	local, err := runtime.OpenLocalControl(ctx)
	if err != nil {
		return true, err
	}
	ctx = local.Context(ctx)
	id, err := model.NewID("GOAL-")
	if err != nil {
		return true, err
	}
	key, err := model.NewID("goal-create-")
	if err != nil {
		return true, err
	}
	e := app.CommandEnvelope{ProjectID: runtime.ProjectIdentity(), SessionID: args[1], TargetID: id, IdempotencyKey: key}
	goal, err := runtime.CommandCreateGoal(ctx, e, input.Request)
	if err != nil {
		return true, err
	}
	if input.Criteria != nil || input.DoNotDo != nil {
		e.ExpectedVersion = goal.Revision
		e.IdempotencyKey += "-criteria"
		goal, err = runtime.CommandEditGoal(ctx, e, app.GoalEdit{SuccessCriteria: input.Criteria, DoNotDo: input.DoNotDo, Reason: "owner supplied explicit acceptance criteria"})
		if err != nil {
			return true, err
		}
	}
	return true, c.print(goal, fmt.Sprintf("goal=%s revision=%d confirmation=%s; review before approving", goal.ID, goal.Revision, goal.Confirmation))
}
