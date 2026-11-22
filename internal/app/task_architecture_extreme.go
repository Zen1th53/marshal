//go:build !governed

package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/archconform"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func (s *MarshalService) taskArchitecture(ctx context.Context, t marshal.Task) (string, bool) {
	if s == nil || s.runtime == nil || s.runtime.store == nil {
		return "ARCHITECTURE FOR THIS TASK: no accepted contract available.\n", false
	}
	accepted, err := s.runtime.store.ReadAcceptedProjectContract(ctx, s.runtime.ProjectContractBinding())
	if err != nil {
		return "ARCHITECTURE FOR THIS TASK: no accepted contract available.\n", false
	}
	return archconform.TaskBrief(accepted.Contract, taskArchitectureParts(archconform.NewMap(accepted.Contract), t)), true
}
