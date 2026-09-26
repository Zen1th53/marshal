package marshal

import "time"

// Ceiling sets task and plan limits in the enclosing Budget field's unit:
// Tokens are token counts, WallTime is whole seconds, and Money is minor
// currency units (for example, cents). Zero means no limit.
type Ceiling struct {
	Task int64
	Plan int64
}

// Budget holds the approved ceilings for each measurable resource.
type Budget struct {
	Tokens   Ceiling
	WallTime Ceiling
	Money    Ceiling
}

// Amount distinguishes an unknown charge from a known zero charge.
type Amount struct {
	Value int64
	Known bool
}

// Charge records consumed tokens, elapsed wall time, and money.
type Charge struct {
	Tokens   Amount
	WallTime time.Duration
	Money    Amount
}

// BudgetResult reports exceeded ceilings and resources with unknown usage.
type BudgetResult struct {
	TaskExceeded []string
	PlanExceeded []string
	Unknown      []string
}

// CheckBudget enforces known charges while reporting unknown units separately.
func CheckBudget(b Budget, task, plan Charge) BudgetResult {
	var r BudgetResult
	check := func(name string, c Ceiling, t, p Amount) {
		if !t.Known || !p.Known {
			r.Unknown = append(r.Unknown, name)
		}
		if c.Task > 0 && t.Known && t.Value > c.Task {
			r.TaskExceeded = append(r.TaskExceeded, name)
		}
		if c.Plan > 0 && p.Known && p.Value > c.Plan {
			r.PlanExceeded = append(r.PlanExceeded, name)
		}
	}
	check("tokens", b.Tokens, task.Tokens, plan.Tokens)
	check("money", b.Money, task.Money, plan.Money)
	check("wall_time", b.WallTime, Amount{int64(task.WallTime / time.Second), true}, Amount{int64(plan.WallTime / time.Second), true})
	return r
}
