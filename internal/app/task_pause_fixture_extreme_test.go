//go:build !governed

package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

// This test exercises the actual Runtime-owned Codex harness and its complete
// accepted project packet. Only the model process is simulated: it waits until
// the canonical pause cancels its context, then lets the supervisor settle.
func pausePlanFixture(t *testing.T) (*Runtime, CreatePlanRequest, func(chan struct{}, chan struct{}, chan struct{})) {
	t.Helper()
	client := &requiredOwnedFixtureAdapter{provider: "codex"}
	r, _, rec, _ := requiredOwnedContextFixture(t, client)
	client.required = rec.Body
	if _, err := r.RegisterAgent(t.Context(), RegisterAgentRequest{Name: "owned pause fixture worker", Role: model.RoleDeveloper, ModelProvider: "codex", ModelName: "test-model"}); err != nil {
		t.Fatal(err)
	}
	request := planCreateRequest()
	request.SessionID = "SESSION-required"
	capacity := goalintake.UnknownCapacity("codex", true)
	profile := model.HarnessProfile{Harness: "codex", InstalledVersion: "owned-fixture", SupportedModels: []string{"test-model"}, DefaultModel: "test-model", ProbeEvidenceID: "owned-pause-fixture", ProbedAt: time.Now().UTC()}
	if err := r.Store().SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	request.Candidates = []goalintake.Candidate{{Provider: "codex", Model: "test-model", Capacity: capacity, Governance: constitution.GovernanceVerified}}
	request.HarnessCandidates = []plan.HarnessCandidate{{Profile: profile, InstalledVersion: profile.InstalledVersion, Provider: "codex", Capacity: capacity}}
	request.Tasks[0].Instructions = "Approved owner instructions for the exact pause fixture task."
	register := func(started, cancelled, settle chan struct{}) {
		client.handle = func(ctx context.Context, _ adapter.Request) (adapter.Result, error) {
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-settle
			return adapter.Result{}, ctx.Err()
		}
	}
	return r, request, register
}
