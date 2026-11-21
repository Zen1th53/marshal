//go:build !governed

package app

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/usage"
)

// liveAgyQuota is agy's limits as the live workstation run read them: the
// Claude and GPT family spent for the week, the Gemini families untouched.
func liveAgyQuota() usage.Quota {
	reset := time.Now().Add(96 * time.Hour)
	return usage.Quota{Provider: "agy", Known: true, AsOf: time.Now(), Windows: []usage.Window{
		{Label: "Gemini wk", LeftPercent: 100, ResetsAt: reset},
		{Label: "Gemini 5h", LeftPercent: 100, ResetsAt: reset},
		{Label: "Claude and GPT wk", LeftPercent: 0, ResetsAt: reset},
		{Label: "Claude and GPT 5h", LeftPercent: 40, ResetsAt: reset},
		{Label: "Gemini 4 Argon wk", LeftPercent: 99, ResetsAt: reset},
	}}
}

// agyRouteFixture is a one-task run whose task runs on agy with model.
func agyRouteFixture(t *testing.T, model string) (*MarshalService, marshal.Run, int64) {
	t.Helper()
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	fx, _ := marshalFixture(t, 1)
	m := fx.Model.(marshalFakeModel)
	m.draft.Plan.Routes = map[string]plan.Route{"a": {Provider: "agy", Model: model}}
	fx.Model = m
	startIntegrityRun(t, fx, marshal.Budget{})
	run, rev, _ := fx.load(t.Context(), "run")
	run.Tasks[0].Worker = "agy"
	run.Tasks[0].Mode = marshal.Governed
	if err := fx.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	run, rev, _ = fx.load(t.Context(), "run")
	fx.GovernedDrivers = map[string]driver.Driver{
		"agy":   &extremeMockDriver{mode: marshal.Governed},
		"codex": &extremeMockDriver{mode: marshal.Governed},
	}
	fx.Drivers = fx.GovernedDrivers
	q := liveAgyQuota()
	fx.QuotaLeft = func(provider string) (int, bool) {
		if provider != "agy" {
			return 50, true
		}
		w, _ := q.Binding()
		return 100 - w.UsedShown(), true
	}
	fx.ModelQuotaLeft = func(provider, model string) (int, bool) {
		if provider != "agy" {
			return 50, true
		}
		w, ok := q.ModelBinding(model)
		if !ok {
			return 0, false
		}
		return 100 - w.UsedShown(), true
	}
	return fx, run, rev
}

// The live case: agy's Claude and GPT family is spent, its Gemini families
// are not. A task that runs on Gemini stays on agy; before, agy's worst
// window (0%) kept every agy task waiting or moved it.
func TestAdmissionKeepsGeminiTaskOnAgyWhenClaudeFamilyIsOut(t *testing.T) {
	fx, run, rev := agyRouteFixture(t, "gemini-4-argon-high")
	if got := fx.taskModel(t.Context(), run, run.Tasks[0], fx.clock()); got != "gemini-4-argon-high" {
		t.Fatalf("task model = %q; want the plan's route model", got)
	}
	if _, err := fx.admitProviderDispatch(t.Context(), "run", &run, rev, 0, "brief"); err != nil {
		t.Fatalf("admitProviderDispatch: %v", err)
	}
	if run.Tasks[0].Worker != "agy" || run.Tasks[0].State == marshal.Reassigned {
		t.Fatalf("a Gemini task left agy: worker %s state %s", run.Tasks[0].Worker, run.Tasks[0].State)
	}
	if isProviderUnavailable("agy", fx.clock()) {
		t.Fatal("agy is marked cooling although the Gemini family has limit left")
	}
}

// The provider-level rule is what the bug was: without the per-model hook,
// the same task is moved off agy.
func TestAdmissionWithoutModelLimitsKeepsProviderRule(t *testing.T) {
	fx, run, rev := agyRouteFixture(t, "gemini-4-argon-high")
	fx.ModelQuotaLeft = nil
	if _, err := fx.admitProviderDispatch(t.Context(), "run", &run, rev, 0, "brief"); err != nil {
		t.Fatalf("admitProviderDispatch: %v", err)
	}
	if run.Tasks[0].Worker != "codex" {
		t.Fatalf("worker = %s; the provider-level rule moves it to codex", run.Tasks[0].Worker)
	}
}

// A task whose model is in the spent family is still kept off agy: it moves
// to a provider with limit left, and agy stays usable for Gemini.
func TestAdmissionMovesClaudeTaskOffAgyWhenItsFamilyIsOut(t *testing.T) {
	fx, run, rev := agyRouteFixture(t, "claude-opus-5-5-high")
	if _, err := fx.admitProviderDispatch(t.Context(), "run", &run, rev, 0, "brief"); err != nil {
		t.Fatalf("admitProviderDispatch: %v", err)
	}
	if run.Tasks[0].Worker != "codex" || run.Tasks[0].State != marshal.Reassigned {
		t.Fatalf("a Claude task on a spent family stayed: worker %s state %s", run.Tasks[0].Worker, run.Tasks[0].State)
	}
	now := fx.clock()
	if !modelLimited("agy", "claude-opus-5-5-high", now) {
		t.Fatal("the spent model is not kept out of use")
	}
	if isProviderUnavailable("agy", now) {
		t.Fatal("agy as a whole is marked cooling for one spent family")
	}
}

// With no other provider able to take it, a task whose family is out is
// refused, not launched on the spent model.
func TestAdmissionRefusesClaudeTaskWhenNoProviderIsLeft(t *testing.T) {
	fx, run, rev := agyRouteFixture(t, "claude-opus-5-5-high")
	fx.GovernedDrivers = map[string]driver.Driver{"agy": &extremeMockDriver{mode: marshal.Governed}}
	fx.Drivers = fx.GovernedDrivers
	_, err := fx.admitProviderDispatch(t.Context(), "run", &run, rev, 0, "brief")
	if err == nil {
		t.Fatal("a task on a spent family was admitted with no provider left")
	}
	if run.Tasks[0].Worker != "agy" {
		t.Fatalf("worker = %s", run.Tasks[0].Worker)
	}
}

// A model no agy family covers keeps the provider-level rule.
func TestAdmissionUnplacedModelKeepsProviderRule(t *testing.T) {
	fx, run, rev := agyRouteFixture(t, "mystery-model")
	if _, err := fx.admitProviderDispatch(t.Context(), "run", &run, rev, 0, "brief"); err != nil {
		t.Fatalf("admitProviderDispatch: %v", err)
	}
	if run.Tasks[0].Worker != "codex" {
		t.Fatalf("worker = %s; an unplaced model reads the provider's worst window", run.Tasks[0].Worker)
	}
}
