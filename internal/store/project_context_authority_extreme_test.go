//go:build !governed

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func authorityFixture(t *testing.T) (*Store, ProjectContextAdmission, ProjectContextAuthorityRequest) {
	t.Helper()
	s, c, audience, now := acceptedContextStore(t)
	a := contextAdmission(t, s, c, audience, now)
	if err := s.RegisterAgent(t.Context(), model.Agent{ID: "owner", ProjectID: c.Project.StorageID, DisplayName: "owner", Role: model.RoleDeveloper, ModelProvider: "codex", ModelName: "configured-alias", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	p := storedPlan()
	if err := s.SavePlan(t.Context(), p, 0); err != nil {
		t.Fatal(err)
	}
	p, err := p.Approve(now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SavePlan(t.Context(), p, p.Version); err != nil {
		t.Fatal(err)
	}
	p, err = s.GetActivePlan(t.Context(), planTestProject)
	if err != nil {
		t.Fatal(err)
	}
	a.Stage = "admission"
	a.Binding.PlanID, a.Binding.PlanVersion, a.Binding.ApprovalScopeDigest = p.ID, p.Version, p.ApprovalScopeDigest
	a.Binding.CanonicalTaskID = "TASK-authority"
	a.Selection.CanonicalTaskID = a.Binding.CanonicalTaskID
	a.Binding.Model = "resolved-explicit-selector"
	ctx := WithTaskBinding(t.Context(), TaskBinding{a.Binding.Session, a.Binding.GoalID, p.ID, a.Binding.GoalRevision, p.Version})
	if _, err = s.ImportTasks(ctx, []model.Task{{ID: a.Binding.CanonicalTaskID, Title: "add the cache", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	repacketContextAdmission(t, s, &a)
	return s, a, ProjectContextAuthorityRequest{Binding: a.Binding, Purpose: ProjectContextAuthorityWorker}
}

func authorityRole(t *testing.T, s *Store, a ProjectContextAdmission, id, scope string) {
	t.Helper()
	if err := s.PutRoleBinding(t.Context(), authz.RoleBinding{ID: id, PrincipalID: a.Binding.Principal, Role: "developer", ScopeID: scope, BoundBy: "owner", BoundAt: a.Now.Add(-time.Hour), PolicyDigest: "sha256:" + strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
}
func authorityGrant(t *testing.T, s *Store, a ProjectContextAdmission, id, scope string, issued, expires time.Time) {
	t.Helper()
	if err := s.PutCapabilityGrant(t.Context(), capability.Grant{ID: capability.GrantID(id), Subject: capability.SubjectID(a.Binding.Principal), TaskID: capability.TaskID(scope), Kind: capability.KindFilesystemRead, Scope: capability.Scope{Resource: "internal/store", Actions: []string{"read"}}, Issuer: "owner", IssuedAt: issued, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
}

func TestProjectContextAuthorityJoinRefusesCurrentnessDrift(t *testing.T) {
	for _, scenario := range []string{"role-revoke", "grant-revoke", "grant-expire", "agent-disable", "agent-revision", "task-pause", "task-revision", "task-binding", "policy-generation", "policy-superseded", "new-role", "new-grant", "session-terminate"} {
		t.Run(scenario, func(t *testing.T) {
			s, a, req := authorityFixture(t)
			authorityRole(t, s, a, "role-alpha", a.Binding.CanonicalTaskID)
			authorityGrant(t, s, a, "grant-alpha", a.Binding.TaskID, a.Now.Add(-time.Hour), a.Now.Add(time.Minute))
			p := testPolicyRecord(t, "authority-policy")
			p.State = policy.StateActive
			p.Status = "active"
			p.ActivatedAt = &a.Now
			if err := s.PutPolicy(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			req.RuntimePolicyID, req.RuntimePolicyVersion = string(p.Policy.ID), int64(p.Policy.Version)
			if _, err := s.db.ExecContext(t.Context(), `INSERT INTO sessions(session_id,agent_id,project_id,task_id,role,started_at,last_heartbeat,status,revision) VALUES('task-live','owner','PROJECT-local',?,'developer',?,?,'active',1)`, a.Binding.CanonicalTaskID, a.Now.Format(time.RFC3339Nano), a.Now.Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			observed, err := s.ReadProjectContextAuthority(t.Context(), req, a.Now)
			if err != nil {
				t.Fatal(err)
			}
			a.ExpectedAuthority = &observed
			// Mutation occurs AFTER qualification and BEFORE writer-reserved admission.
			switch scenario {
			case "role-revoke":
				err = s.RevokeRoleBinding(t.Context(), "role-alpha", a.Now.Add(time.Second))
			case "grant-revoke":
				err = s.RevokeCapabilityGrant(t.Context(), "grant-alpha", a.Now.Add(time.Second))
			case "grant-expire":
				a.Now = a.Now.Add(time.Minute)
			case "agent-disable":
				_, err = s.db.ExecContext(t.Context(), `UPDATE agents SET status='disabled' WHERE agent_id='owner'`)
			case "agent-revision":
				_, err = s.db.ExecContext(t.Context(), `UPDATE agents SET revision=revision+1 WHERE agent_id='owner'`)
			case "task-pause":
				_, err = s.db.ExecContext(t.Context(), `UPDATE task_controls SET state='paused' WHERE task_id=?`, a.Binding.CanonicalTaskID)
			case "task-revision":
				_, err = s.db.ExecContext(t.Context(), `UPDATE tasks SET revision=revision+1 WHERE task_id=?`, a.Binding.CanonicalTaskID)
			case "task-binding":
				_, err = s.db.ExecContext(t.Context(), `UPDATE task_controls SET session_id='foreign-session' WHERE task_id=?`, a.Binding.CanonicalTaskID)
			case "policy-generation":
				_, err = s.db.ExecContext(t.Context(), `UPDATE policy_versions SET generation=generation+1 WHERE policy_id=?`, req.RuntimePolicyID)
			case "policy-superseded":
				_, err = s.db.ExecContext(t.Context(), `UPDATE policy_versions SET state='superseded' WHERE policy_id=?`, req.RuntimePolicyID)
			case "new-role":
				authorityRole(t, s, a, "role-new", a.Binding.Project.CanonicalID)
			case "new-grant":
				authorityGrant(t, s, a, "grant-new", a.Binding.CanonicalTaskID, a.Now.Add(-time.Hour), a.Now.Add(time.Hour))
			case "session-terminate":
				_, err = s.db.ExecContext(t.Context(), `UPDATE sessions SET status='terminated' WHERE session_id='task-live'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.CommitProjectContextAdmission(t.Context(), a); !errors.Is(err, projectcontext.ErrChanged) {
				t.Fatalf("%s admitted: %v", scenario, err)
			}
			if n := queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts`); n != 0 {
				t.Fatalf("partial receipts: %d", n)
			}
		})
	}
}

func TestProjectContextAuthorityScopedBoundariesAndSingleConnection(t *testing.T) {
	s, a, req := authorityFixture(t)
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO sessions(session_id,agent_id,project_id,task_id,role,started_at,last_heartbeat,status,revision) VALUES('task-live','owner','PROJECT-local',?,'developer',?,?,'active',1)`, a.Binding.CanonicalTaskID, a.Now.Add(-time.Minute).Format(time.RFC3339Nano), a.Now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	authorityRole(t, s, a, "role-alpha", a.Binding.CanonicalTaskID)
	authorityGrant(t, s, a, "grant-alpha", a.Binding.CanonicalTaskID, a.Now.Add(-time.Hour), a.Now.Add(123*time.Nanosecond))
	observed, err := s.ReadProjectContextAuthority(t.Context(), req, a.Now)
	if err != nil {
		t.Fatal(err)
	}
	if observed.ActiveRoleBindings != 1 || observed.ActiveCapabilityGrants != 1 || observed.ValidUntil == nil || !observed.ValidUntil.Equal(a.Now.Add(123*time.Nanosecond)) {
		t.Fatal("lost exact validity", observed)
	}
	authorityRole(t, s, a, "role-beta", "TASK-unrelated-beta")
	authorityGrant(t, s, a, "grant-beta", "TASK-unrelated-beta", a.Now.Add(-time.Hour), a.Now.Add(time.Hour))
	if _, err = s.db.ExecContext(t.Context(), `UPDATE sessions SET last_heartbeat=?,revision=revision+1`, a.Now.Add(time.Second).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadProjectContextAuthority(t.Context(), req, a.Now)
	if err != nil || got.Digest != observed.Digest {
		t.Fatal("unrelated authority invalidated alpha", got, err)
	}
	a.ExpectedAuthority = &observed
	s.db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	r, err := s.CommitProjectContextAdmission(ctx, a)
	if err != nil || r.ValidUntil == nil || !r.ValidUntil.Equal(*observed.ValidUntil) {
		t.Fatal("single connection deadlock or missing receipt boundary", r, err)
	}
	a.Now = *observed.ValidUntil
	a.ID = "expired-alpha"
	if _, err = s.CommitProjectContextAdmission(ctx, a); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatal("nanosecond expiry admitted", err)
	}
}

func TestProjectContextAuthorityCanonicalExpiryFormatsAndBarrier(t *testing.T) {
	for _, nanoNow := range []bool{false, true} {
		t.Run(fmt.Sprint(nanoNow), func(t *testing.T) {
			s, a, req := authorityFixture(t)
			if nanoNow {
				a.Now = a.Now.Add(time.Nanosecond)
			}
			// Exactsecond Z and variable-width fractions must compare by instant.
			authorityGrant(t, s, a, "expired-exact", a.Binding.CanonicalTaskID, a.Now.Add(-time.Hour), a.Now.Truncate(time.Second))
			authorityGrant(t, s, a, "live-nano", a.Binding.CanonicalTaskID, a.Now.Add(-time.Hour), a.Now.Add(12*time.Nanosecond))
			authorityGrant(t, s, a, "live-exact", a.Binding.CanonicalTaskID, a.Now.Add(-time.Hour), a.Now.Truncate(time.Second).Add(time.Second))
			observed, err := s.ReadProjectContextAuthority(t.Context(), req, a.Now)
			if err != nil || observed.ActiveCapabilityGrants != 2 {
				t.Fatal("UTC fraction ordering dropped or resurrected rows", observed, err)
			}
			a.ExpectedAuthority = &observed
			qualified := make(chan struct{})
			mutated := make(chan error, 1)
			go func() { <-qualified; mutated <- s.RevokeCapabilityGrant(t.Context(), "live-nano", a.Now) }()
			close(qualified)
			if err = <-mutated; err != nil {
				t.Fatal(err)
			}
			if _, err = s.CommitProjectContextAdmission(t.Context(), a); !errors.Is(err, projectcontext.ErrChanged) {
				t.Fatal("barrier mutation between resolve and admission missed", err)
			}
		})
	}
}

func TestProjectContextAuthorityRegistryAndMetadataNegatives(t *testing.T) {
	for _, scenario := range []string{"invalid-request", "bad-goal", "foreign-provider", "future-role", "role-overflow", "session-overflow", "new-policy", "bad-policy", "oversized-metadata", "future-observation", "enrichment-only"} {
		t.Run(scenario, func(t *testing.T) {
			s, a, req := authorityFixture(t)
			observed, err := s.ReadProjectContextAuthority(t.Context(), req, a.Now)
			if err != nil {
				t.Fatal(err)
			}
			a.ExpectedAuthority = &observed
			switch scenario {
			case "invalid-request":
				req.RuntimePolicyVersion = -1
			case "bad-goal":
				req.Binding.GoalID = "not-current"
			case "foreign-provider":
				req.Binding.Provider = "claude"
			case "future-role":
				authorityRole(t, s, a, "future-role", a.Binding.CanonicalTaskID)
				_, err = s.db.ExecContext(t.Context(), `UPDATE role_bindings SET bound_at=? WHERE binding_id='future-role'`, a.Now.Add(3*time.Nanosecond).Format(time.RFC3339Nano))
				before, e := s.ReadProjectContextAuthority(t.Context(), req, a.Now)
				if e != nil || before.ActiveRoleBindings != 0 || before.ValidUntil == nil {
					t.Fatal(before, e)
				}
				a.ExpectedAuthority = &before
				a.Now = *before.ValidUntil
			case "role-overflow":
				_, err = s.db.ExecContext(t.Context(), `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<257) INSERT INTO role_bindings(binding_id,principal_id,role,scope_id,bound_by,bound_at,policy_digest) SELECT 'role-'||i,'owner','developer',?,'owner',?,? FROM n`, a.Binding.CanonicalTaskID, a.Now.Format(time.RFC3339Nano), "sha256:"+strings.Repeat("a", 64))
			case "session-overflow":
				_, err = s.db.ExecContext(t.Context(), `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<257) INSERT INTO sessions(session_id,agent_id,project_id,task_id,role,started_at,last_heartbeat,status) SELECT 'session-'||i,'owner','PROJECT-local',?,'developer',?,?,'active' FROM n`, a.Binding.CanonicalTaskID, a.Now.Format(time.RFC3339Nano), a.Now.Format(time.RFC3339Nano))
			case "new-policy", "bad-policy":
				p := testPolicyRecord(t, "new-authority-policy")
				p.State = policy.StateActive
				if err = s.PutPolicy(t.Context(), p); err != nil {
					t.Fatal(err)
				}
				if scenario == "bad-policy" {
					_, err = s.db.ExecContext(t.Context(), `UPDATE policy_versions SET digest=? WHERE policy_id='new-authority-policy'`, "sha256:"+strings.Repeat("b", 64))
				}
			case "oversized-metadata":
				_, err = s.db.ExecContext(t.Context(), `UPDATE agents SET capabilities_json=? WHERE agent_id='owner'`, strings.Repeat("x", (256<<10)+1))
			case "future-observation":
				a.ExpectedAuthority.CheckedAt = a.Now.Add(time.Nanosecond)
			case "enrichment-only":
				a.ExpectedAuthority.Request.Binding.InventoryDigest = "unrelated-enriched"
				a.ExpectedAuthority.Request.Binding.ManifestDigest = "enriched"
				a.ExpectedAuthority.Request.Binding.RequiredStateDigest = "derived"
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "invalid-request" || scenario == "bad-goal" || scenario == "foreign-provider" || scenario == "role-overflow" || scenario == "session-overflow" || scenario == "bad-policy" || scenario == "oversized-metadata" {
				if _, err = s.ReadProjectContextAuthority(t.Context(), req, a.Now); err == nil {
					t.Fatal("invalid metadata qualified", scenario)
				}
			} else if scenario == "enrichment-only" {
				if _, err = s.CommitProjectContextAdmission(t.Context(), a); err != nil {
					t.Fatal("derived packet fields changed authority", err)
				}
			} else if _, err = s.CommitProjectContextAdmission(t.Context(), a); !errors.Is(err, projectcontext.ErrChanged) {
				t.Fatal("currentness drift admitted", scenario, err)
			}
		})
	}
}

func TestProjectContextAuthorityPurposeAndBindingCannotSelectAuthority(t *testing.T) {
	s, a, req := authorityFixture(t)
	for _, change := range []string{"foreign-project", "foreign-task", "unknown-agent", "empty-provider", "unknown-purpose", "owner-provider", "display-task"} {
		bad := req
		switch change {
		case "foreign-project":
			bad.Binding.Project.StorageID = "PROJECT-foreign"
		case "foreign-task":
			bad.Binding.CanonicalTaskID = "TASK-foreign"
		case "unknown-agent":
			bad.Binding.Principal = "unregistered"
		case "empty-provider":
			bad.Binding.Provider = ""
		case "unknown-purpose":
			bad.Purpose = "arbitrary"
		case "owner-provider":
			bad.Purpose = ProjectContextAuthorityPlanning
		case "display-task":
			bad.Binding.TaskID = "not-approved"
		}
		if _, err := s.ReadProjectContextAuthority(t.Context(), bad, a.Now); err == nil {
			t.Fatal("forged authority request accepted", change)
		}
	}
	owner := req
	owner.Binding.Principal = "native-unregistered-owner"
	owner.Binding.Provider = ""
	owner.Purpose = ProjectContextAuthorityCompletion
	got, err := s.ReadProjectContextAuthority(t.Context(), owner, a.Now)
	if err != nil || got.ActiveRoleBindings != 0 || got.ActiveCapabilityGrants != 0 {
		t.Fatal("data-only owner absence should be captured, never grant", got, err)
	}
	observed, err := s.ReadProjectContextAuthority(t.Context(), req, a.Now)
	if err != nil {
		t.Fatal(err)
	}
	a.ExpectedAuthority = &observed
	a.ExpectedAuthority.Request.Binding.Base = "forged-base"
	if _, err = s.CommitProjectContextAdmission(t.Context(), a); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatal("observation for different binding admitted", err)
	}
}

func TestProjectContextAuthorityFutureActivationAndCurrentOverflow(t *testing.T) {
	t.Run("future activation", func(t *testing.T) {
		s, a, req := authorityFixture(t)
		authorityGrant(t, s, a, "future", a.Binding.CanonicalTaskID, a.Now.Add(42*time.Nanosecond), a.Now.Add(time.Hour))
		before, err := s.ReadProjectContextAuthority(t.Context(), req, a.Now)
		if err != nil {
			t.Fatal(err)
		}
		if before.ActiveCapabilityGrants != 0 || before.ValidUntil == nil || !before.ValidUntil.Equal(a.Now.Add(42*time.Nanosecond)) {
			t.Fatal("future grant boundary omitted", before)
		}
		a.ExpectedAuthority = &before
		a.Now = *before.ValidUntil
		if _, err = s.CommitProjectContextAdmission(t.Context(), a); !errors.Is(err, projectcontext.ErrChanged) {
			t.Fatal("activation not fenced", err)
		}
		after, err := s.ReadProjectContextAuthority(t.Context(), req, a.Now)
		if err != nil || after.ActiveCapabilityGrants != 1 || after.Digest == before.Digest {
			t.Fatal("activation fingerprint unchanged", after, err)
		}
	})
	t.Run("historical excluded current exhaustive", func(t *testing.T) {
		s, a, req := authorityFixture(t)
		for i := 0; i < 300; i++ {
			authorityGrant(t, s, a, fmt.Sprintf("old-%03d", i), a.Binding.CanonicalTaskID, a.Now.Add(-time.Hour), a.Now.Add(-time.Nanosecond))
		}
		got, err := s.ReadProjectContextAuthority(t.Context(), req, a.Now)
		if err != nil || got.ActiveCapabilityGrants != 0 {
			t.Fatal("expired history became authority", got, err)
		}
		for i := 0; i < 257; i++ {
			authorityGrant(t, s, a, fmt.Sprintf("live-%03d", i), a.Binding.CanonicalTaskID, a.Now.Add(-time.Hour), a.Now.Add(time.Hour))
		}
		if _, err = s.ReadProjectContextAuthority(t.Context(), req, a.Now); err == nil {
			t.Fatal("silently truncated relevant current grants")
		}
		if n := queryInt(t, s.db, `SELECT count(*) FROM capability_grants`); n != 557 {
			t.Fatal("history modified", n)
		}
	})
}
