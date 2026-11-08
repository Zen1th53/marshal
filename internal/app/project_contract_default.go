//go:build governed

package app

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

func privateProjectContractKind(permission.Request) bool { return false }
func (r *Runtime) commitBrokerProjectContractPermission(context.Context, permission.Request, bool, model.Event) (bool, error) {
	return false, nil
}

// Community has no model acceptance: the owner's browser click is required.
func (r *Runtime) RecordProjectContractOperatorInstruction(context.Context, string, string, string, string, time.Time) (OperatorInstructionBinding, bool, error) {
	return OperatorInstructionBinding{}, false, model.ErrPolicyDenied
}
