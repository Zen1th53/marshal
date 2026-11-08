//go:build governed

package store

import (
	"context"
	"database/sql"
	"github.com/Zen1th53/marshal/internal/plan"
)

func (*Store) validateNativePlanningFence(context.Context, *sql.Tx, plan.ExecutionPlan) error {
	return nil
}
