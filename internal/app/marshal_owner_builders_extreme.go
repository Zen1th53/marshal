//go:build !governed

package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// applyOwnerBuilders makes the owner's per-part builder, accepted in the
// browser, the worker of every task that builds that part. packet is the
// validated browser architecture packet (nil when no review exists).
// It returns the RevisionReason lines for the replacements it made.
func applyOwnerBuilders(draft *MarshalDraft, packet json.RawMessage) ([]string, error) {
	if len(packet) == 0 {
		return nil, nil
	}
	var context struct {
		Status       string `json:"status"`
		Architecture struct {
			Components []struct {
				ID string `json:"id"`
			} `json:"components"`
		} `json:"architecture"`
		Builders map[string]string `json:"builders"`
	}
	if err := json.Unmarshal(packet, &context); err != nil {
		return nil, err
	}
	if context.Status != "accepted" || len(context.Builders) == 0 {
		return nil, nil
	}
	parts := make(map[string]bool, len(context.Architecture.Components))
	for _, component := range context.Architecture.Components {
		parts[component.ID] = true
	}
	// Own the task slice so a refusal cannot mutate the caller's draft.
	draft.Tasks = append([]marshal.Task(nil), draft.Tasks...)
	var lines []string
	for i := range draft.Tasks {
		task := &draft.Tasks[i]
		if len(task.Components) == 0 {
			return nil, fmt.Errorf("%w: task %s must list the architecture parts it builds in \"components\" (the owner chose builders)", model.ErrInvalid, task.PlanTaskID)
		}
		for _, component := range task.Components {
			if !parts[component] {
				return nil, fmt.Errorf("%w: task %s names unknown architecture part %s", model.ErrInvalid, task.PlanTaskID, component)
			}
		}
		providers := make(map[string]bool)
		for _, component := range task.Components {
			if provider := context.Builders[component]; provider != "" {
				providers[provider] = true
			}
		}
		if len(providers) > 1 {
			choices := make([]string, 0, len(providers))
			for provider := range providers {
				choices = append(choices, provider)
			}
			sort.Strings(choices)
			return nil, fmt.Errorf("%w: task %s builds parts with different chosen builders (%s); split it so each task's parts share one builder", model.ErrInvalid, task.PlanTaskID, strings.Join(choices, ", "))
		}
		if len(providers) == 1 {
			var provider string
			for choice := range providers {
				provider = choice
			}
			task.Worker = provider
			for _, component := range task.Components {
				if context.Builders[component] == provider {
					lines = append(lines, fmt.Sprintf("owner builder: task %q uses %s (part %s)", task.PlanTaskID, provider, component))
					break
				}
			}
		}
	}
	return lines, nil
}
