package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
)

type runtimeEventAuthorizerA06 struct{}

func (runtimeEventAuthorizerA06) Authorize(_ context.Context, r events.AuthorizationRequest) (events.AuthorizationDecision, error) {
	return events.AuthorizationDecision{
		Allowed: true, Identity: r.Identity, Action: r.Action, EventID: r.EventID, Type: r.Type,
		TaskID: r.TaskID, RunID: r.RunID, ResourceID: r.ResourceID, EvidenceID: r.EvidenceID,
		IdempotencyKey: r.IdempotencyKey,
		PolicyDigest:   "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		FreshUntil:     time.Now().Add(time.Hour),
	}, nil
}

type runtimeEventBusA06 struct {
	fail      bool
	published []events.Event
}

func (b *runtimeEventBusA06) Publish(_ context.Context, e events.Event) error {
	if b.fail {
		return errors.New("MARSHAL_TEST_SECRET_T43_A06_BUS_31d7")
	}
	b.published = append(b.published, events.CloneEvent(e))
	return nil
}
func (b *runtimeEventBusA06) Subscribe(context.Context, events.Sequence) (<-chan events.Event, func(), error) {
	ch := make(chan events.Event, 1)
	return ch, func() {}, nil
}

func TestRuntimeClaimRecordsCanonicalStructuredLeaseEvent(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	bus := &runtimeEventBusA06{}
	rt, err := OpenWithOptions(context.Background(), repo.Path(), Options{
		EventAuthorizer: runtimeEventAuthorizerA06{},
		EventFreshness:  events.FreshnessValidatorFunc(func(context.Context, events.AuthorizationRequest, events.AuthorizationDecision) error { return nil }),
		EventBus:        bus,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close() })
	agent, err := rt.RegisterAgent(context.Background(), RegisterAgentRequest{Name: "event-agent", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ImportTasks(context.Background(), []model.Task{{ID: "TASK-EVENT-A06", Title: "events", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}

	claim, err := rt.Claim(context.Background(), ClaimRequest{TaskID: "TASK-EVENT-A06", AgentID: agent.ID, ExpectedRevision: 0})
	if err != nil {
		t.Fatal(err)
	}
	items, err := rt.StructuredEventsSince(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("events=%+v", items)
	}
	if items[0].Type != "scheduler.lease.acquired" || items[0].Subject != events.SubjectID(agent.ID) || items[0].TaskID != "TASK-EVENT-A06" || items[0].ResourceID != events.ResourceID(claim.Lease.ID) {
		t.Fatalf("lease event=%+v", items[0])
	}
	if items[0].Data["session_id"] != claim.Session.ID || items[0].Data["result"] != "acquired" {
		t.Fatalf("data=%v", items[0].Data)
	}
	if items[1].Type != "events.appended" {
		t.Fatalf("audit=%+v", items[1])
	}
}

func TestRuntimeClaimLostLiveDeliveryReconcilesWithoutSecondLease(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	bus := &runtimeEventBusA06{fail: true}
	rt, err := OpenWithOptions(context.Background(), repo.Path(), Options{
		EventAuthorizer: runtimeEventAuthorizerA06{},
		EventFreshness:  events.FreshnessValidatorFunc(func(context.Context, events.AuthorizationRequest, events.AuthorizationDecision) error { return nil }),
		EventBus:        bus,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close() })
	agent, err := rt.RegisterAgent(context.Background(), RegisterAgentRequest{Name: "retry-agent", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ImportTasks(context.Background(), []model.Task{{ID: "TASK-EVENT-RETRY", Title: "retry", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}

	first, err := rt.Claim(context.Background(), ClaimRequest{TaskID: "TASK-EVENT-RETRY", AgentID: agent.ID, ExpectedRevision: 0})
	if err == nil || first.Lease.ID == "" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	if strings.Contains(err.Error(), "MARSHAL_TEST_SECRET_T43_A06_BUS_31d7") {
		t.Fatalf("secret leaked in public error: %q", err.Error())
	}
	if got := events.ReasonCode(err); got != events.CodeStoreFailed {
		t.Fatalf("reason=%s err=%v", got, err)
	}
	bus.fail = false
	second, err := rt.Claim(context.Background(), ClaimRequest{TaskID: "TASK-EVENT-RETRY", AgentID: agent.ID, ExpectedRevision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if second.Lease.ID != first.Lease.ID || second.Session.ID != first.Session.ID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	items, err := rt.StructuredEventsSince(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("events=%+v", items)
	}
}

func TestOpenRejectsStructuredEventBusWithoutAuthority(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	_, err := OpenWithOptions(context.Background(), repo.Path(), Options{EventBus: &runtimeEventBusA06{}})
	if !errors.Is(err, events.ErrAuthorizationUnavailable) {
		t.Fatalf("error=%v", err)
	}
}

func TestStructuredEventsSurviveRuntimeReopen(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	options := Options{
		EventAuthorizer: runtimeEventAuthorizerA06{},
		EventFreshness:  events.FreshnessValidatorFunc(func(context.Context, events.AuthorizationRequest, events.AuthorizationDecision) error { return nil }),
		EventBus:        &runtimeEventBusA06{},
	}
	rt, err := OpenWithOptions(context.Background(), repo.Path(), options)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := rt.RegisterAgent(context.Background(), RegisterAgentRequest{Name: "reopen-agent", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ImportTasks(context.Background(), []model.Task{{ID: "TASK-EVENT-REOPEN", Title: "reopen", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Claim(context.Background(), ClaimRequest{TaskID: "TASK-EVENT-REOPEN", AgentID: agent.ID, ExpectedRevision: 0}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}

	options.EventBus = &runtimeEventBusA06{}
	rt, err = OpenWithOptions(context.Background(), repo.Path(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close() })
	items, err := rt.StructuredEventsSince(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Type != "scheduler.lease.acquired" || items[1].Type != "events.appended" {
		t.Fatalf("events=%+v", items)
	}
}
