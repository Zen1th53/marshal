//go:build !governed

package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// The headless CLI planning turn already obtained complete mandatory context.
// Consume its opaque actual-turn capture and bind the resulting tasks to the
// same current Goal and accepted contract before the atomic plan/run write.
func (s *MarshalService) canonicalizeHeadlessDraft(ctx context.Context, requested string, draft MarshalDraft, budget marshal.Budget) (MarshalDraft, error) {
	cli, ok := s.Model.(*MarshalCLI)
	if !ok || s.runtime == nil || cli.runtime != s.runtime || cli.requiredGoalSession == "" {
		return MarshalDraft{}, projectcontext.ErrReviewPending
	}
	capture, err := cli.takeRequiredMarshalTurnCapture(s.runtime)
	if err != nil {
		return MarshalDraft{}, err
	}
	if capture.preparation.receipt.Outcome != "QUALIFIED" {
		return MarshalDraft{}, projectcontext.ErrReviewPending
	}
	goal, err := s.Store.GetActiveGoalContract(ctx, cli.requiredGoalSession)
	if err != nil {
		return MarshalDraft{}, err
	}
	if !goal.Confirmation.Settled() || strings.TrimSpace(requested) != strings.TrimSpace(goal.OriginalRequest) {
		return MarshalDraft{}, fmt.Errorf("%w: /marshal goal must match the current confirmed Goal; review and confirm the Goal first", model.ErrInvalid)
	}
	binding := capture.preparation.receipt.Binding
	if binding.GoalID != goal.ID || binding.GoalRevision != goal.Revision || binding.Session != goal.SessionID || binding.Project != s.runtime.ProjectContractBinding() {
		return MarshalDraft{}, projectcontext.Changed("marshal_headless_plan_extreme.go:40")
	}
	settings, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		return MarshalDraft{}, err
	}
	if budget != settings.Value.Budget {
		return MarshalDraft{}, fmt.Errorf("%w: Marshal budget changed during planning; review again", model.ErrConflict)
	}
	mode := plan.ModeStandard
	if marshal.TierPolicy(s.Gate, settings.Value).Tier == marshal.Ultra {
		mode = plan.ModeUltra
	}
	canonical, err := s.runtime.PrepareNativeMarshalDraft(ctx, goal.SessionID, draft, mode)
	if err != nil {
		return MarshalDraft{}, err
	}
	if !canonical.Plan.Goal.Matches(goal) || canonical.Plan.RequiredProjectContract == nil || *canonical.Plan.RequiredProjectContract != binding.Contract {
		return MarshalDraft{}, projectcontext.Changed("marshal_headless_plan_extreme.go:58")
	}
	return canonical, nil
}
