//go:build governed

package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// handleWorkerLimit escalates the task in default build when a provider limit occurs.
func (s *MarshalService) handleWorkerLimit(ctx context.Context, runID string, dispatch MarshalDispatch, handin marshal.HandIn, reason string, cooldown time.Duration) (marshal.HandIn, error) {
	run, rev, loadErr := s.load(context.WithoutCancel(ctx), runID)
	if loadErr != nil {
		return handin, loadErr
	}
	i := taskIndex(run, dispatch.TaskID)
	if i < 0 {
		return handin, model.ErrNotFound
	}
	t := &run.Tasks[i]
	t.State = marshal.Escalated
	t.EscalationReason = "provider limit: " + reason
	t.EscalationCode = escalationCode(marshal.CodeProviderLimit, t.EscalationReason)
	run.Operation = nil
	pauseMarshal(&run, marshal.Pause{Reason: "provider limit: " + reason, Resolutions: []string{"resolve the provider limit before retrying"}, Code: marshal.CodeProviderLimit, Action: marshal.PauseResume})
	if saveErr := s.save(context.WithoutCancel(ctx), runID, run, rev); saveErr != nil {
		return handin, saveErr
	}
	alertErr := s.record(context.WithoutCancel(ctx), runID, dispatch.TaskID, events.EventTypeMarshalEscalated, map[string]any{
		"reason":   reason,
		"provider": t.Worker,
		"cooldown": cooldown.String(),
	})
	return handin, errors.Join(fmt.Errorf("provider limit: %s", reason), alertErr)
}

// GetEffectiveFailoverOrder returns enabled default providers in governed build.
func GetEffectiveFailoverOrder(s marshal.Settings) []string {
	var out []string
	for _, p := range MarshalProviders() {
		if s.ProviderEnabled(p) {
			out = append(out, p)
		}
	}
	return out
}

func orderReassignCandidates(_ marshal.Settings, names []string) []string { return names }

func isProviderFailover(t marshal.Task) bool                    { return false }
func providerFailoverBrief(t marshal.Task, brief string) string { return brief }

func finishProviderFailoverDispatch(t *marshal.Task) {}

func (s *MarshalService) admitProviderDispatch(ctx context.Context, _ string, run *marshal.Run, rev int64, index int, _ string) (int64, error) {
	policy := s.projectSettingsSnapshot(ctx, run.Settings)
	if !policy.ProviderEnabled(run.Tasks[index].Worker) {
		return rev, fmt.Errorf("provider %s is disabled by project settings", run.Tasks[index].Worker)
	}
	return rev, nil
}

func providerFailoverPending(marshal.Task) bool { return false }
