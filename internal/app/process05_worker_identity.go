package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
)

// process05WorkerEventType records that the runtime provisioned the worker
// identity a Process 05 assignment needed. It is audit evidence, not a grant.
const process05WorkerEventType = "PROCESS05_WORKER_PROVISIONED"

// process05WorkerName is the display name of the runtime-provisioned worker
// principal for one provider. One canonical name per provider keeps the
// identity stable across tasks, runs and restarts.
func process05WorkerName(provider string) string { return "marshal-worker-" + provider }

// resolveProcess05Worker binds a Process 05 assignment for provider to a
// registered local principal. The assignment's explicit agent, then a role
// match, then a unique provider principal are honoured exactly as before.
// When no principal of that provider exists, the runtime provisions one
// canonical worker identity, but only for a provider whose harness probe is
// recorded and unexpired: an installed, probed CLI is what the identity
// stands for. The probe evidence is written to the audit log with it.
// Multiple candidates remain a hard refusal; nothing is guessed. With
// exactAssigned, a missing explicitly assigned principal is refused as before.
func (r *Runtime) resolveProcess05Worker(ctx context.Context, task execution.TaskExecution, provider, label string, sameProvider func(string) bool, exactAssigned bool) (string, error) {
	if task.AssignedAgent != "" {
		agent, err := r.store.GetAgent(ctx, task.AssignedAgent)
		if err == nil {
			if agent.Status != model.AgentDisabled && sameProvider(agent.ModelProvider) {
				return task.AssignedAgent, nil
			}
			return "", fmt.Errorf("%w: assigned Process 05 agent %q is not an eligible local %s agent", model.ErrUnavailable, task.AssignedAgent, label)
		}
		// A provider that requires its exact assigned principal never falls
		// back to another one when that principal is missing.
		if exactAssigned || !errors.Is(err, model.ErrNotFound) {
			return "", err
		}
	}
	r.process05WorkerMu.Lock()
	defer r.process05WorkerMu.Unlock()
	id, found, err := r.matchProcess05Worker(ctx, task, label, sameProvider)
	if err != nil || found {
		return id, err
	}
	profile, err := r.store.GetHarnessProfile(ctx, marshalHarnessName(provider))
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return "", fmt.Errorf("read %s harness probe: %w", label, err)
	}
	now := time.Now().UTC()
	if profile == nil || profile.ProbeEvidenceID == "" || !now.Before(profile.ExpiresAt) {
		return "", fmt.Errorf("%w: no registered local %s agent matches Process 05 assignment %q, and no current %s harness probe exists to provision one; run /harness verify %s", model.ErrUnavailable, label, task.AssignedRole, label, provider)
	}
	modelName := task.AssignedModel
	if modelName == "" {
		modelName = profile.DefaultModel
	}
	agent, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: process05WorkerName(provider), Role: model.RoleDeveloper, ModelProvider: provider, ModelName: modelName})
	if err != nil {
		return "", fmt.Errorf("provision Process 05 %s worker: %w", label, err)
	}
	eventID, err := model.NewID("EVT-P05-WORKER-")
	if err != nil {
		return "", err
	}
	if err := r.store.AppendEvent(ctx, nil, model.Event{ID: eventID, Type: process05WorkerEventType, ProjectID: r.ProjectID(), TaskID: "", ActorAgentID: agent.ID, Timestamp: now, Data: map[string]any{
		"agent_id": agent.ID, "provider": provider, "model": modelName, "harness": profile.Harness,
		"installed_version": profile.InstalledVersion, "probe_evidence_id": profile.ProbeEvidenceID,
		"probe_expires_at": profile.ExpiresAt.UTC().Format(time.RFC3339Nano), "plan_task": task.TaskID,
		"assigned_role": task.AssignedRole, "reason": "Process 05 assignment had no registered local worker for an installed, probed provider",
	}}); err != nil {
		return "", fmt.Errorf("record Process 05 %s worker provisioning: %w", label, err)
	}
	return agent.ID, nil
}

// matchProcess05Worker applies the pre-existing resolution rules over the
// registered principals: a role match first, otherwise every principal of
// the provider; exactly one candidate binds, several refuse.
func (r *Runtime) matchProcess05Worker(ctx context.Context, task execution.TaskExecution, label string, sameProvider func(string) bool) (string, bool, error) {
	agents, err := r.Agents(ctx)
	if err != nil {
		return "", false, fmt.Errorf("list Process 05 agents: %w", err)
	}
	var eligible, all []model.Agent
	for _, agent := range agents {
		if agent.Status != model.AgentDisabled && sameProvider(agent.ModelProvider) {
			all = append(all, agent)
			if task.AssignedRole != "" && string(agent.Role) == task.AssignedRole {
				eligible = append(eligible, agent)
			}
		}
	}
	// Some Process 04 assignment roles are provider route labels ("codex")
	// rather than an Agent.Role enum. In that case only a *unique* eligible
	// principal of the provider may be used.
	if len(eligible) == 0 {
		eligible = all
	}
	switch len(eligible) {
	case 0:
		return "", false, nil
	case 1:
		return eligible[0].ID, true, nil
	default:
		return "", true, fmt.Errorf("%w: %d eligible local %s agents match Process 05 assignment %q; bind AssignedAgent explicitly", model.ErrInvalid, len(eligible), label, task.AssignedRole)
	}
}

// providerWorkerAgent returns the canonical worker principal of provider,
// registering it once. Callers outside Process 05 use it instead of
// registering a fresh principal per dispatch, which would leave several
// candidates and make later Process 05 resolution refuse.
func (r *Runtime) providerWorkerAgent(ctx context.Context, provider string) (string, error) {
	r.process05WorkerMu.Lock()
	defer r.process05WorkerMu.Unlock()
	agents, err := r.Agents(ctx)
	if err != nil {
		return "", err
	}
	for _, agent := range agents {
		if agent.Status != model.AgentDisabled && agent.ModelProvider == provider && agent.DisplayName == process05WorkerName(provider) {
			return agent.ID, nil
		}
	}
	agent, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: process05WorkerName(provider), Role: model.RoleDeveloper, ModelProvider: provider})
	if err != nil {
		return "", err
	}
	return agent.ID, nil
}
