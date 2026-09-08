package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

const execUsage = "Usage: marshal exec start --session SESSION-ID --project PROJECT-ID | run RUN-ID | status RUN-ID | approve APPROVAL-ID [--decider USER] [--reason TEXT] | rollback CHECKPOINT-ID | handoff RUN-ID\n"

func (c command) exec(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: %s", model.ErrInvalid, strings.TrimSuffix(execUsage, "\n"))
	}

	switch args[0] {
	case "start", "run", "status", "approve", "rollback", "handoff":
		// valid subcommand, proceed to open runtime
	default:
		return fmt.Errorf("%w: %s", model.ErrInvalid, strings.TrimSuffix(execUsage, "\n"))
	}

	runtime, err := app.Open(ctx, c.root)
	if err != nil {
		return err
	}
	defer runtime.Close()

	service := runtime.Execution()
	if service == nil {
		return fmt.Errorf("%w: execution service unavailable", model.ErrUnavailable)
	}

	switch args[0] {
	case "start":
		return c.execStart(ctx, service, args[1:])
	case "run":
		return c.execRun(ctx, service, args[1:])
	case "status":
		return c.execStatus(ctx, service, args[1:])
	case "approve":
		return c.execApprove(ctx, service, args[1:])
	case "rollback":
		return c.execRollback(ctx, service, args[1:])
	case "handoff":
		return c.execHandoff(ctx, service, args[1:])
	default:
		return fmt.Errorf("%w: %s", model.ErrInvalid, strings.TrimSuffix(execUsage, "\n"))
	}
}

func (c command) execStart(ctx context.Context, service *app.ExecutionService, args []string) error {
	var sessionID string
	var projectID string

	fs := flag.NewFlagSet("exec start", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	fs.StringVar(&sessionID, "session", "", "Session ID")
	fs.StringVar(&projectID, "project", "", "Project ID")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Positional fallback if flags not used
	remaining := fs.Args()
	if sessionID == "" && len(remaining) > 0 {
		sessionID = remaining[0]
		remaining = remaining[1:]
	}
	if projectID == "" && len(remaining) > 0 {
		projectID = remaining[0]
	}

	if sessionID == "" || projectID == "" {
		return fmt.Errorf("%w: --session and --project are required", model.ErrInvalid)
	}

	run, err := service.StartRun(ctx, sessionID, projectid.ID(projectID))
	if err != nil {
		return err
	}

	return c.print(run, fmt.Sprintf("run=%s state=%s tasks=%d", run.RunID, run.State, len(run.Tasks)))
}

func (c command) execRun(ctx context.Context, service *app.ExecutionService, args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("%w: run ID is required", model.ErrInvalid)
	}
	runID := args[0]

	run, err := service.ExecuteRun(ctx, runID)
	if err != nil {
		if run != nil {
			_ = c.print(run, fmt.Sprintf("run=%s state=%s phase=%s", run.RunID, run.State, run.CurrentPhase))
		}
		return err
	}

	return c.print(run, fmt.Sprintf("run=%s state=%s phase=%s", run.RunID, run.State, run.CurrentPhase))
}

func (c command) execStatus(ctx context.Context, service *app.ExecutionService, args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("%w: run ID is required", model.ErrInvalid)
	}
	runID := args[0]

	run, err := service.GetRun(ctx, runID)
	if err != nil {
		return err
	}

	return c.print(run, fmt.Sprintf("run=%s state=%s phase=%s tasks=%d", run.RunID, run.State, run.CurrentPhase, len(run.Tasks)))
}

func (c command) execApprove(ctx context.Context, service *app.ExecutionService, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: approval ID is required", model.ErrInvalid)
	}

	approvalID := args[0]
	decider := "operator"
	reason := "Approved via MARSHAL CLI"

	fs := flag.NewFlagSet("exec approve", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	fs.StringVar(&decider, "decider", decider, "Decider identity")
	fs.StringVar(&reason, "reason", reason, "Approval rationale")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if err := service.Approve(ctx, approvalID, decider, reason); err != nil {
		return err
	}

	res := map[string]string{
		"approval_id": approvalID,
		"status":      "APPROVED",
		"decider":     decider,
		"reason":      reason,
	}
	return c.print(res, fmt.Sprintf("approval=%s status=APPROVED decider=%s", approvalID, decider))
}

func (c command) execRollback(ctx context.Context, service *app.ExecutionService, args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("%w: checkpoint ID is required", model.ErrInvalid)
	}
	checkpointID := args[0]

	cp, err := service.Rollback(ctx, checkpointID)
	if err != nil {
		return err
	}

	return c.print(cp, fmt.Sprintf("checkpoint=%s restored=true task=%s", cp.CheckpointID, cp.TaskID))
}

func (c command) execHandoff(ctx context.Context, service *app.ExecutionService, args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("%w: run ID is required", model.ErrInvalid)
	}
	runID := args[0]

	bundle, err := service.AssembleHandoffBundle(ctx, runID)
	if err != nil {
		return err
	}

	return c.print(bundle, fmt.Sprintf("bundle=%s run=%s plan=%s state=%s tasks=%d", bundle.RunID, bundle.RunID, bundle.PlanID, bundle.FinalState, len(bundle.Tasks)))
}
