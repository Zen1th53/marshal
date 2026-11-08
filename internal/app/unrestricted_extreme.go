//go:build !governed

package app

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// ExtremeRuntimeDecider names the runtime acting on the Marshal's behalf when
// the private edition applies a decision itself, without a decision turn.
const ExtremeRuntimeDecider = "marshal-runtime"

const extremeReadReason = "Marshal: reads outside the project apply automatically"

// buildUndecidedRead lets MARSHAL's own readers (continuation, session
// inventory, native history capture and watchers) read a folder no operator
// decision covers. An explicit operator denial is still the most specific
// decision and wins. The first read of each folder in a runtime is recorded as
// an ordinary PERMISSION_DECIDED event naming the deciding actor.
func (r *Runtime) buildUndecidedRead(resolved string) bool {
	folder := resolved
	if info, err := os.Stat(resolved); err == nil && !info.IsDir() {
		folder = filepath.Dir(resolved)
	}
	if _, seen := r.automaticReads.LoadOrStore(folder, true); seen {
		return true
	}
	id, err := model.NewID("EVENT-PERMISSION-")
	if err == nil {
		err = r.store.AppendEvent(context.Background(), nil, model.Event{ID: id, Type: "PERMISSION_DECIDED", ProjectID: r.ProjectID(), Timestamp: time.Now().UTC(), Data: map[string]any{
			"kind": "read", "object": folder, "scope": "this runtime, read-only", "allow": true,
			"actor": ExtremeRuntimeDecider, "decided_by": ExtremeRuntimeDecider, "source": "marshal-extreme",
			"reason": extremeReadReason, "decision_reason": extremeReadReason, "runtime_instance": r.runtimeInstanceID,
		}})
	}
	if err != nil {
		// Evidence must precede the read. Retry recording on the next read.
		r.automaticReads.Delete(folder)
		return false
	}
	return true
}

// buildHighRiskNetworkAllowed: the private edition does not refuse a
// developer's network for an R2/R3 task; every destination is recorded by the
// run's egress evidence instead.
func buildHighRiskNetworkAllowed() bool { return true }

// buildMaxFreshSessionRetries: zero, no bound. Retrying an escalated task in a
// fresh session never stops at a count in the private edition.
func buildMaxFreshSessionRetries() int { return 0 }

// buildTaskLaunchTimeout: zero, no wall-clock cut of one governed task launch
// (one model turn). An owner-set run budget (wall time, tokens, money) is
// still charged and enforced after the turn, and the operator can cancel.
func buildTaskLaunchTimeout() time.Duration { return 0 }

// buildShellExecGrantTTL bounds the provider shell-exec grant of one launch.
// The launch has no wall-clock cut here, and the required project context
// receipt ends at the grant's expiry, so a 30-minute grant stopped every model
// turn at 30 minutes (REQUIRED_CONTEXT_CHANGED) and discarded its edits. The
// grant stays bound to one binary and task and is revoked when the launch ends.
func buildShellExecGrantTTL() time.Duration { return 12 * time.Hour }
