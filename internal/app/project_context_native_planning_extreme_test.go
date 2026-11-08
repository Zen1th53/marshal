//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestNativePlanningFullReadIsOpaqueCurrentAndOneShot(t *testing.T) {
	r, b, record, _ := requiredContextFixture(t, true)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := control.Context(t.Context())
	principal := authz.Principal{ID: "owner", Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	view, token, err := r.PrepareNativePlanningRead(ctx, principal, b.Session, 16384)
	if err != nil || token == nil || view.Bootstrap || !strings.Contains(view.Context, record.Body) || view.Qualified {
		t.Fatal("complete read missing or invented authority", err)
	}
	raw, err := json.Marshal(token)
	if err != nil || string(raw) != "{}" {
		t.Fatal("opaque read serialized authority", err)
	}
	var forged NativePlanningRead
	if err := json.Unmarshal(raw, &forged); err != nil {
		t.Fatal(err)
	}
	effects := 0
	effect := func(context.Context) error { effects++; return nil }
	if err := r.WithNativePlanningRead(ctx, &forged, b.GoalID, b.GoalRevision, effect); err == nil || effects != 0 {
		t.Fatal("restored JSON proof accepted")
	}
	if err := (&Runtime{}).WithNativePlanningRead(ctx, token, b.GoalID, b.GoalRevision, effect); err == nil || effects != 0 {
		t.Fatal("foreign runtime accepted")
	}
	if err := r.WithNativePlanningRead(ctx, token, b.GoalID, b.GoalRevision+1, effect); err == nil || effects != 0 {
		t.Fatal("foreign Goal revision accepted")
	}
	if err := r.WithNativePlanningRead(ctx, token, b.GoalID, b.GoalRevision, effect); err != nil || effects != 1 {
		t.Fatal("current full read refused", err)
	}
	if err := r.WithNativePlanningRead(ctx, token, b.GoalID, b.GoalRevision, effect); err == nil || effects != 1 {
		t.Fatal("read token reused")
	}
}

func TestNativePlanningReadRefusesChangedMandatoryStateBeforeEffect(t *testing.T) {
	for _, scenario := range []string{"acl", "body", "overflow", "unauthenticated"} {
		t.Run(scenario, func(t *testing.T) {
			r, b, record, _ := requiredContextFixture(t, true)
			control, err := r.OpenLocalControl(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			ctx := control.Context(t.Context())
			principal := authz.Principal{ID: "owner", Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
			view, token, err := r.PrepareNativePlanningRead(ctx, principal, b.Session, 16384)
			if err != nil || token == nil || view.Context == "" {
				t.Fatal(err)
			}
			switch scenario {
			case "acl", "body":
				_, err = r.store.UpdateMemory(t.Context(), record.ProjectID, record.ID, record.Revision, func(m *model.MemoryRecordV2) error {
					if scenario == "acl" {
						m.ACLScope = "foreign"
					} else {
						m.Body = "Changed essential policy"
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			case "overflow":
				view, token, err = r.PrepareNativePlanningRead(ctx, principal, b.Session, 1)
				if !errors.Is(err, projectcontext.ErrBudget) || token != nil || view.Context != "" {
					t.Fatal("partial overflowing read accepted", err)
				}
				return
			case "unauthenticated":
				ctx = t.Context()
			}
			effects := 0
			if err := r.WithNativePlanningRead(ctx, token, b.GoalID, b.GoalRevision, func(context.Context) error { effects++; return nil }); err == nil || effects != 0 {
				t.Fatal("changed/missing authenticated memory reached effect")
			}
		})
	}
}

func TestNativePlanningMissingContractIsIntakeOnly(t *testing.T) {
	r := openNetpolRuntime(t)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := control.Context(t.Context())
	principal := authz.Principal{ID: "owner", Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	view, token, err := r.PrepareNativePlanningRead(ctx, principal, "SESSION-intake", 16384)
	if err != nil || !view.Bootstrap || token != nil || view.Qualified || !strings.Contains(view.Context, "INTAKE ONLY") {
		t.Fatal("missing contract falsely qualified planning", err)
	}
	if err := r.WithNativePlanningRead(ctx, token, "GOAL-invented", 1, func(context.Context) error { t.Fatal("bootstrap reached planning"); return nil }); err == nil {
		t.Fatal("bootstrap admitted plan")
	}
}

func TestNativePlanningPersistenceRechecksMemoryInsideWriterTransaction(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "current-write", true: "mutation-after-runtime-validation"}[changed], func(t *testing.T) {
			r, b, record, _ := requiredContextFixture(t, true)
			control, err := r.OpenLocalControl(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			ctx := control.Context(t.Context())
			principal := authz.Principal{ID: "owner", Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
			view, token, err := r.PrepareNativePlanningRead(ctx, principal, b.Session, 16384)
			if err != nil {
				t.Fatal(err)
			}
			goal, err := r.store.GetActiveGoalContract(ctx, b.Session)
			if err != nil {
				t.Fatal(err)
			}
			p := plan.ExecutionPlan{ID: "PLAN-native-fenced", ProjectID: projectid.ID(r.ProjectIdentity()), Goal: plan.BindGoal(goal), RequiredProjectContract: &view.Contract, Version: 1, State: plan.StateReady, Mode: plan.ModeStandard}
			run := marshal.Run{PlanID: p.ID, PlanVersion: 1, State: marshal.Drafting, Settings: marshal.DefaultSettings()}
			err = r.WithNativePlanningRead(ctx, token, b.GoalID, b.GoalRevision, func(fenced context.Context) error {
				// Deterministic mutation after the Runtime full-reader check, before the
				// actual canonical writer starts. This is the SQL race gap, not a fake
				// bypass seam or a concurrent probabilistic assertion.
				if changed {
					if _, e := r.store.UpdateMemory(ctx, record.ProjectID, record.ID, record.Revision, func(m *model.MemoryRecordV2) error { m.ACLScope = "foreign"; return nil }); e != nil {
						return e
					}
				}
				return r.store.SaveMarshalPlanState(fenced, r.ProjectID(), "RUN-native-fenced", p, 0, run, 0, nil)
			})
			_, readErr := r.store.GetMarshalRun(ctx, r.ProjectID(), "RUN-native-fenced")
			if changed {
				if err == nil || readErr == nil {
					t.Fatal("stale full-read reached canonical writer", err)
				}
			} else if err != nil || readErr != nil {
				t.Fatal("current full-read writer refused", err, readErr)
			}
		})
	}
}

func TestNativePlanningWriterRefusesRoleRevocationAfterFullRead(t *testing.T) {
	r, b, _, _ := requiredContextFixture(t, true)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := control.Context(t.Context())
	principal := authz.Principal{ID: "owner", Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	if err := r.store.RegisterAgent(ctx, model.Agent{ID: principal.ID, ProjectID: r.ProjectID(), DisplayName: "native owner reader fixture", Role: model.RoleDeveloper, Status: model.AgentRegistered, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if err := r.store.PutRoleBinding(ctx, authz.RoleBinding{ID: "ROLE-native-planning", PrincipalID: principal.ID, Role: "marshal", ScopeID: r.ProjectID(), BoundBy: "owner", BoundAt: time.Now().UTC().Add(-time.Hour), PolicyDigest: "sha256:" + strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	view, token, err := r.PrepareNativePlanningRead(ctx, principal, b.Session, 16384)
	if err != nil {
		t.Fatal(err)
	}
	goal, err := r.store.GetActiveGoalContract(ctx, b.Session)
	if err != nil {
		t.Fatal(err)
	}
	p := plan.ExecutionPlan{ID: "PLAN-native-role-fenced", ProjectID: projectid.ID(r.ProjectIdentity()), Goal: plan.BindGoal(goal), RequiredProjectContract: &view.Contract, Version: 1, State: plan.StateReady, Mode: plan.ModeStandard}
	run := marshal.Run{PlanID: p.ID, PlanVersion: 1, State: marshal.Drafting, Settings: marshal.DefaultSettings()}
	err = r.WithNativePlanningRead(ctx, token, b.GoalID, b.GoalRevision, func(fenced context.Context) error {
		if e := r.store.RevokeRoleBinding(ctx, "ROLE-native-planning", time.Now().UTC()); e != nil {
			return e
		}
		return r.store.SaveMarshalPlanState(fenced, r.ProjectID(), "RUN-native-role-fenced", p, 0, run, 0, nil)
	})
	if err == nil {
		t.Fatal("revoked role accepted by canonical native writer")
	}
	if _, err := r.store.GetMarshalRun(ctx, r.ProjectID(), "RUN-native-role-fenced"); err == nil {
		t.Fatal("revoked role persisted plan/run")
	}
}
