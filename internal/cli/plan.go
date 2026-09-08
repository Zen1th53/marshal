package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

const planUsage = "Usage: marshal plan create SESSION-ID --file INPUT.json | show PROJECT-ID | approve PROJECT-ID | cancel PROJECT-ID | handoff SESSION-ID PROJECT-ID\n"

func (c command) plan(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: %s", model.ErrInvalid, planUsage[:len(planUsage)-1])
	}
	runtime, err := app.Open(ctx, c.root)
	if err != nil {
		return err
	}
	defer runtime.Close()
	service := runtime.Plans()

	switch args[0] {
	case "create":
		return c.createPlan(ctx, service, args[1:])
	case "show":
		if len(args) != 2 {
			return fmt.Errorf("%w: %s", model.ErrInvalid, planUsage[:len(planUsage)-1])
		}
		p, err := service.Current(ctx, projectid.ID(args[1]))
		if err != nil {
			return err
		}
		return c.print(p, fmt.Sprintf("plan=%s version=%d state=%s", p.ID, p.Version, p.State))
	case "approve":
		if len(args) != 2 {
			return fmt.Errorf("%w: %s", model.ErrInvalid, planUsage[:len(planUsage)-1])
		}
		p, err := service.Approve(ctx, projectid.ID(args[1]))
		if err != nil {
			return err
		}
		return c.print(p, fmt.Sprintf("plan=%s version=%d state=%s", p.ID, p.Version, p.State))
	case "cancel":
		if len(args) != 2 {
			return fmt.Errorf("%w: %s", model.ErrInvalid, planUsage[:len(planUsage)-1])
		}
		p, err := service.Cancel(ctx, projectid.ID(args[1]))
		if err != nil {
			return err
		}
		return c.print(p, fmt.Sprintf("plan=%s version=%d state=%s", p.ID, p.Version, p.State))
	case "handoff":
		if len(args) != 3 {
			return fmt.Errorf("%w: %s", model.ErrInvalid, planUsage[:len(planUsage)-1])
		}
		handoff, err := service.Handoff(ctx, args[1], projectid.ID(args[2]))
		if err != nil {
			return err
		}
		return c.print(handoff, fmt.Sprintf("plan=%s version=%d handed off", handoff.PlanID, handoff.PlanVersion))
	default:
		return fmt.Errorf("%w: %s", model.ErrInvalid, planUsage[:len(planUsage)-1])
	}
}

func (c command) createPlan(ctx context.Context, service *app.PlanService, args []string) error {
	if len(args) != 3 || args[1] != "--file" {
		return fmt.Errorf("%w: %s", model.ErrInvalid, planUsage[:len(planUsage)-1])
	}
	body, err := os.ReadFile(args[2])
	if err != nil {
		return err
	}
	var input app.CreatePlanRequest
	if err := json.Unmarshal(body, &input); err != nil {
		return fmt.Errorf("decode plan input: %w", err)
	}
	input.SessionID = args[0]
	p, err := service.Create(ctx, input)
	if err != nil {
		return err
	}
	return c.print(p, fmt.Sprintf("plan=%s version=%d state=%s", p.ID, p.Version, p.State))
}
