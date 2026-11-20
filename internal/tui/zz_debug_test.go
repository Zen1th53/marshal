//go:build linux

package tui

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestZZDebugFrames(t *testing.T) {
	w := activeGoalBoardWorkspace(t, nil, "s1")
	fmt.Println(activeGoalFrameText(w, 120, 40))
	w.mu.Lock()
	w.state.Goal = model.GoalContract{ID: "G", Revision: 1, DesiredOutcome: "Build a terminal RSS reader", Confirmation: model.ConfirmationApproved, CreatedAt: time.Now().Add(-25 * time.Minute)}
	w.mu.Unlock()
	fmt.Println(activeGoalFrameText(w, 120, 40))
	g := emptyBoardWorkspace(t)
	g.gateContractSource = func(context.Context) (*gateContract, error) { return walkContract(), nil }
	g.refreshGates(t.Context())
	fmt.Println(frameText(g, 120, 40))
	for _, h := range g.boardHits.hits {
		fmt.Println(h)
	}
}
