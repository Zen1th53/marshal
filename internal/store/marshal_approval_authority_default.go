//go:build governed

package store

import (
	"context"
	"database/sql"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
)

func (*Store) validateMarshalApprovalAuthorityTx(context.Context, *sql.Tx, string, plan.ExecutionPlan, marshal.Run, int64) error {
	return nil
}
