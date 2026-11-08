//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestCanonicalGoalConfirmationInspectionNeverBecomesExecutionAuthority(t *testing.T) {
	r, b, record, _ := requiredContextFixture(t, true)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := control.Context(t.Context())
	principal := authz.Principal{ID: "owner", Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	assertInspection := func() model.GoalContract {
		t.Helper()
		canonical, err := r.store.GetActiveGoalContract(ctx, b.Session)
		if err != nil {
			t.Fatal(err)
		}
		view, err := r.InspectProjectPlanningContext(ctx, principal, b.Session, 16384)
		if err != nil || view.Qualified || view.GoalRevision != canonical.Revision || !strings.Contains(view.Context, record.Body) {
			t.Fatal("canonical inspection omitted required context or acquired execution authority", err)
		}
		start := strings.Index(view.Context, "{") // Inspection uses no untrusted prefix.
		if start < 0 {
			t.Fatal("inspection packet missing")
		}
		var envelope struct {
			Goal struct {
				Confirmation    model.ConfirmationState `json:"confirmation"`
				AuthoritySource string                  `json:"authority_source"`
			} `json:"goal"`
		}
		if err := json.Unmarshal([]byte(view.Context[start:]), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Goal.Confirmation != canonical.Confirmation || envelope.Goal.AuthoritySource != canonical.AuthoritySource {
			t.Fatal("runtime omitted or inferred canonical Goal confirmation/source")
		}
		return canonical
	}
	current := assertInspection()
	if !current.Confirmation.Settled() {
		t.Fatal("initial fixture must have canonical settled purpose")
	}
	pending, err := r.CommandEditGoal(ctx, CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: b.Session, TargetID: current.ID, ExpectedVersion: current.Revision, IdempotencyKey: "goal-context-confirmation-edit"}, GoalEdit{Reason: "Local owner revises purpose metadata; no execution permission"})
	if err != nil || pending.Confirmation != model.ConfirmationPending {
		t.Fatal("supported Goal edit did not return to pending", err)
	}
	if observed := assertInspection(); observed.Confirmation != model.ConfirmationPending {
		t.Fatal("pending canonical state was delivered as approved")
	}
	view, token, err := r.PrepareNativePlanningRead(ctx, principal, b.Session, 16384)
	if err != nil || token != nil || !view.Bootstrap || view.Qualified || view.Context == "" {
		t.Fatal("pending Goal inspection acquired a planning proof", err)
	}
	effects := 0
	if err := r.WithNativePlanningRead(ctx, token, pending.ID, pending.Revision, func(context.Context) error { effects++; return nil }); err == nil || effects != 0 {
		t.Fatal("pending metadata authorized a planning effect")
	}
}

func TestRequiredPlanningInspectionIncludesAllEssentialsWithoutExecutionAuthority(t *testing.T) {
	r, b, record, _ := requiredContextFixture(t, true)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := control.Context(t.Context())
	principal := authz.Principal{ID: "owner", Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	view, err := r.InspectProjectPlanningContext(ctx, principal, b.Session, 16384)
	if err != nil || view.Qualified || view.RequiredCount != 1 || view.GoalID != b.GoalID || view.GoalRevision != b.GoalRevision || view.Contract != b.Contract || !strings.Contains(view.Context, record.Body) || !strings.Contains(view.Context, "Preserve reliable project execution") {
		t.Fatal("full current first-goal inspection refused or omitted essential memory", err)
	}
	t.Logf("complete packet: %d bytes", len(view.Context))
	for _, scenario := range []string{"unbound-owner", "foreign-goal", "overflow", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			current, session, capBytes := ctx, b.Session, 16384
			switch scenario {
			case "unbound-owner":
				current = t.Context()
			case "foreign-goal":
				session = "foreign"
			case "overflow":
				capBytes = 1
			case "revoked":
				if _, err := r.store.UpdateMemory(t.Context(), record.ProjectID, record.ID, record.Revision, func(m *model.MemoryRecordV2) error { m.ACLScope = "foreign"; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			refused, err := r.InspectProjectPlanningContext(current, principal, session, capBytes)
			if err == nil || refused.Context != "" || refused.Qualified || refused.RequiredCount != 0 {
				t.Fatal("unavailable context leaked a partial packet", err)
			}
		})
	}
}
