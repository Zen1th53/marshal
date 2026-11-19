package plan

import (
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
)

// DecompositionWarnings asks the Marshal to reconsider a monolithic plan.
// Criteria can describe one inseparable change, so this is advice, not a gate.
func DecompositionWarnings(goal model.GoalContract, tasks []Task) []string {
	if len(tasks) != 1 || len(goal.SuccessCriteria) < 4 {
		return nil
	}
	return []string{fmt.Sprintf("Planning check for the Marshal: this Goal has %d success criteria but only one task. Split independent requirements or components into separate tasks with explicit dependencies, leaving independent tasks ready for parallel execution. If the work is inseparable, explain the grouping in the task instructions. Resolve this planning advice yourself; do not ask the owner to split the plan.", len(goal.SuccessCriteria))}
}
