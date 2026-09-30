package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/optimization"
)

// handleOptimizationCycle is intentionally evidence-only. The TUI may reveal
// candidates, vetoes and promotion decisions, but cannot create, promote or
// roll back a cycle outside the governed runtime boundary.
func (h *CommandHandler) handleOptimizationCycle(ctx context.Context, id string) (string, error) {
	if h.ws.store == nil {
		return "Canonical optimization store unavailable. Open the TUI in an initialized MARSHAL project (marshal init).", nil
	}
	cycle, err := h.ws.store.GetOptimizationCycle(ctx, id)
	if errors.Is(err, optimization.ErrNotFound) {
		return fmt.Sprintf("Optimization cycle %q not found. Use /optimization <cycle_id> with an existing canonical cycle ID.", id), nil
	}
	if err != nil {
		return "", fmt.Errorf("read optimization cycle: %w; reopen the TUI and check /store", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "OPTIMIZATION CYCLE %s v%d\n", cycle.ID, cycle.Version)
	fmt.Fprintf(&b, "  Process 07 binding: %s v%d\n", cycle.Binding.MemoryCommitID, cycle.Binding.MemoryVersion)
	fmt.Fprintf(&b, "  Source SHA: %s\n", cycle.Binding.SourceSHA)
	fmt.Fprintf(&b, "  Candidates: %d | Baselines: %d | Evidence refs: %d\n", len(cycle.Candidates), len(cycle.Baselines), len(cycle.ExperimentRefs))
	fmt.Fprintf(&b, "  Decisions: %d | Vetoes: %d\n", len(cycle.Decisions), len(cycle.Vetoes))
	for _, decision := range cycle.Decisions {
		fmt.Fprintf(&b, "    %s: %s\n", decision.CandidateID, decision.Decision)
	}
	for _, veto := range cycle.Vetoes {
		fmt.Fprintf(&b, "    veto %s: %s\n", veto.CandidateID, strings.Join(veto.Reasons, "; "))
	}
	for _, blocked := range cycle.BlockedOptimization {
		fmt.Fprintf(&b, "    blocked: %s\n", blocked)
	}
	fmt.Fprintf(&b, "  Digest: %s", cycle.Digest)
	return b.String(), nil
}
