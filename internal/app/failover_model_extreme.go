//go:build !governed

package app

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// Per-model limits at dispatch. agy reports one limit per model family: its
// "Claude and GPT" family can be spent while its "Gemini" family has nearly
// all of its limit left. The provider's worst window (QuotaLeft) then says
// 0%, and treating that as the whole provider kept every agy task waiting,
// Gemini ones included. When the limits reader knows the limits of the
// model a task runs on (ModelQuotaLeft), those decide: the task's model out
// keeps that task off the provider; the provider stays usable for its other
// models. A model the reader cannot place keeps the provider-level rule.

// candidateModel is the model worker would run a task on when the plan names
// none for it: the one its current verified profile chooses; "" unknown.
func (s *MarshalService) candidateModel(ctx context.Context, worker string, now time.Time) string {
	if s.ModelQuotaLeft == nil || s.Store == nil {
		return ""
	}
	m, _ := flexibleWorkerModel(ctx, s.Store, worker, "", now)
	return m
}

// taskModel is the model task t runs on with its worker: the approved plan's
// route model when the route names that worker (what Dispatch launches),
// else candidateModel; "" when unknown.
func (s *MarshalService) taskModel(ctx context.Context, run marshal.Run, t marshal.Task, now time.Time) string {
	if s.ModelQuotaLeft == nil || s.Store == nil {
		return ""
	}
	if p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion); err == nil {
		if route, ok := p.Routes[t.PlanTaskID]; ok && route.Model != "" && marshalHarnessName(route.Provider) == marshalHarnessName(t.Worker) {
			return route.Model
		}
	}
	return s.candidateModel(ctx, t.Worker, now)
}

// markKnownEmptyFor is markKnownEmpty for the model a task runs on. When the
// limits reader knows that model's limits they decide: a model with none
// left is kept out of use until defaultCooldown (the provider's other
// models stay usable) and out is true; a model with limit left marks
// nothing. Otherwise the provider-level rule applies and out is false.
func (s *MarshalService) markKnownEmptyFor(provider, model string, now time.Time) (out bool) {
	if model != "" && s.ModelQuotaLeft != nil {
		if left, known := s.ModelQuotaLeft(provider, model); known {
			if left > 0 {
				return modelLimited(provider, model, now)
			}
			if !modelLimited(provider, model, now) {
				markModelLimited(provider, model, "no limit left for "+model, now.Add(defaultCooldown))
			}
			return true
		}
	}
	s.markKnownEmpty(provider, now)
	return false
}

// modelOutUntil is when a model kept out by markKnownEmptyFor or a limit
// is usable again; zero when it is not out.
func modelOutUntil(provider, model string) time.Time {
	until, _, _ := providerHealth.CooldownUntil(modelHealthKey(provider, model))
	return until
}
