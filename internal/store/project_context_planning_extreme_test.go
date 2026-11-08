//go:build !governed

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func planningFixture(t *testing.T) (*Store, projectcontext.Contract, ProjectPlanningContextRequest) {
	t.Helper()
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	ids := []string{"MEM-project", "MEM-alpha", "MEM-beta", "MEM-component", "MEM-path", "MEM-optional"}
	records := []model.MemoryRecordV2{}
	for _, id := range ids {
		r := contextStoreRecord(id, now)
		r.Body = "Mandatory accepted essential " + id
		if id == "MEM-alpha" || id == "MEM-beta" {
			r.Scope = "task"
			r.ScopeID = "TASK-" + strings.TrimPrefix(id, "MEM-")
		}
		r.ContentDigest = r.CanonicalDigest()
		if err := s.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
		records = append(records, r)
	}
	if _, err := s.ImportTasks(t.Context(), []model.Task{{ID: "TASK-alpha", Title: "alpha", Status: model.TaskReady, Risk: model.R1}, {ID: "TASK-beta", Title: "beta", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	c, a, now := contextStoreDraft(t, s, records)
	c.Required[1].Applies = projectcontext.Applicability{TaskIDs: []string{"alpha"}}
	c.Required[2].Applies = projectcontext.Applicability{TaskIDs: []string{"beta"}}
	c.Required[3].Applies = projectcontext.Applicability{Components: []string{"store"}}
	c.Required[4].Applies = projectcontext.Applicability{Paths: []string{"docs"}}
	c.Required[5].Class = projectcontext.ContextOnly
	req := admitContextDecision(t, s, c, true, "planning-all")
	c, err := s.CommitMCPProjectContractDecision(t.Context(), req, true, a, now, contextAudit(req, now))
	if err != nil {
		t.Fatal(err)
	}
	seed := contextAdmission(t, s, c, a, now)
	b := seed.Binding
	b.TaskID = ""
	b.CanonicalTaskID = ""
	b.Attempt = 0
	b.Provider = ""
	b.RequiredStateDigest = ""
	b.InventoryDigest = ""
	b.ManifestDigest = ""
	return s, c, ProjectPlanningContextRequest{Binding: b, Audience: a, Prefix: "Complete planning request ⚡ <facts>\n", ByteCap: 16384, Now: now}
}

func TestProjectPlanningContextLoadsAllAcceptedScopesAndExactCurrentGoal(t *testing.T) {
	s, c, req := planningFixture(t)
	result, err := s.ReadProjectPlanningContext(t.Context(), req)
	if err != nil || len(result.Snapshot.Records) != 5 || len(result.Packet.Loaded) != 5 || result.Selection.Mode != projectcontext.SelectionPlanning {
		t.Fatal("project/task/component/path essentials omitted", result, err)
	}
	for _, id := range []string{"MEM-project", "MEM-alpha", "MEM-beta", "MEM-component", "MEM-path"} {
		if !strings.Contains(result.Packet.Text, "Mandatory accepted essential "+id) {
			t.Fatal("missing required fact", id)
		}
	}
	if strings.Contains(result.Packet.Text, "MEM-optional") || !strings.Contains(result.Packet.Text, c.Purpose) || !strings.Contains(result.Packet.Text, "Finish the actual task") || !strings.Contains(result.Packet.Text, "Do not forget required policy") || !strings.Contains(result.Packet.Text, "Accepted work conforms to architecture") || result.Goal.ID != req.Binding.GoalID || result.Goal.Revision != req.Binding.GoalRevision {
		t.Fatal("mission substituted for actual Goal or current essentials omitted", result)
	}
	if result.Binding.RequiredStateDigest != result.Snapshot.StateDigest || result.Binding.Contract != result.Snapshot.CurrentContract || result.Binding.ManifestDigest != result.Packet.ManifestDigest || result.Packet.Bytes != len(result.Packet.Text) {
		t.Fatal("snapshot/packet binding mismatch", result)
	}
	// Inspection can canonically fill an absent Contract, never accept a draft.
	bootstrap := req
	bootstrap.Binding.Contract = projectcontext.ContractRef{}
	filled, err := s.ReadProjectPlanningContext(t.Context(), bootstrap)
	if err != nil || filled.Binding.Contract != result.Binding.Contract || filled.Packet.Digest != result.Packet.Digest {
		t.Fatal("canonical bootstrap data differs", filled, err)
	}
	s.db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, err = s.ReadProjectPlanningContext(ctx, req); err != nil {
		t.Fatal("single-connection read transaction reentered store", err)
	}
	if n := queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts`); n != 0 {
		t.Fatal("planning inspection forged qualification", n)
	}
	if n := queryInt(t, s.db, `SELECT count(*) FROM audit_events WHERE event_type='PROJECT_CONTEXT_QUALIFIED'`); n != 0 {
		t.Fatal("planning read wrote a qualification audit", n)
	}
	alpha, err := s.ReadProjectContextSnapshot(t.Context(), c.Project, req.Audience, projectcontext.Selection{TaskID: "alpha"}, req.Now)
	if err != nil || len(alpha.Records) != 2 {
		t.Fatal("ordinary worker selection expanded", alpha, err)
	}
}

func TestProjectPlanningContextHiddenACLAndScopeNeverSilentlyOmit(t *testing.T) {
	for _, failure := range []string{"hidden-acl", "missing-scope", "new-unknown", "stale-required", "changed-goal", "changed-contract", "worker-task", "bad-principal", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			s, c, req := planningFixture(t)
			ctx := t.Context()
			switch failure {
			case "hidden-acl":
				_, err := s.UpdateMemory(ctx, c.Project.StorageID, "MEM-beta", 1, func(r *model.MemoryRecordV2) error { r.ACLScope = "foreign"; return nil })
				if err != nil {
					t.Fatal(err)
				}
			case "missing-scope":
				req.Audience.ScopeIDs = []string{c.Project.StorageID, "TASK-alpha"}
			case "new-unknown":
				r := contextStoreRecord("MEM-new-unclassified", req.Now)
				if err := s.WriteMemoryV2(ctx, r); err != nil {
					t.Fatal(err)
				}
			case "stale-required":
				_, err := s.UpdateMemory(ctx, c.Project.StorageID, "MEM-beta", 1, func(r *model.MemoryRecordV2) error { r.Lifecycle = model.MemoryStale; return nil })
				if err != nil {
					t.Fatal(err)
				}
			case "changed-goal":
				g, err := s.GetActiveGoalContract(ctx, req.Binding.Session)
				if err != nil {
					t.Fatal(err)
				}
				g.DesiredOutcome = "New actual task"
				if err = s.SaveGoalContract(ctx, g, g.Revision); err != nil {
					t.Fatal(err)
				}
			case "changed-contract":
				next := c
				next.Revision++
				next.SupersedesDigest, _ = c.CanonicalDigest()
				next.RevisionReason = "Reviewed changed task-only policy"
				next.Required = append([]projectcontext.MemoryRef{}, next.Required...)
				for i := range next.Required {
					if next.Required[i].ID == "MEM-beta" {
						next.Required[i].Reason = "Updated beta requirement"
					}
				}
				decision := admitContextDecision(t, s, next, true, "planning-next")
				if _, err := s.CommitMCPProjectContractDecision(ctx, decision, true, req.Audience, req.Now, contextAudit(decision, req.Now)); err != nil {
					t.Fatal(err)
				}
			case "worker-task":
				req.Binding.TaskID = "alpha"
			case "bad-principal":
				req.Binding.Principal = "forged"
			case "cancelled":
				child, cancel := context.WithCancel(ctx)
				cancel()
				ctx = child
			}
			result, err := s.ReadProjectPlanningContext(ctx, req)
			if err == nil || result.Packet.Text != "" {
				t.Fatal("planning silently omitted unavailable essential or returned partial packet", failure, result, err)
			}
			if failure == "hidden-acl" || failure == "missing-scope" {
				if !errors.Is(err, authz.ErrUnauthorized) {
					t.Fatal("ACL must precede body/digest validation", err)
				}
			}
		})
	}
}

func TestProjectPlanningContextCompleteBudgetAndInspectionDoNotQualify(t *testing.T) {
	s, _, req := planningFixture(t)
	result, err := s.ReadProjectPlanningContext(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.ByteCap = result.Packet.Bytes
	exact, err := s.ReadProjectPlanningContext(t.Context(), req)
	if err != nil || exact.Packet.Digest != result.Packet.Digest {
		t.Fatal("exact whole-prefix byte cap changed packet", exact, err)
	}
	req.ByteCap--
	if got, err := s.ReadProjectPlanningContext(t.Context(), req); !errors.Is(err, projectcontext.ErrBudget) || got.Packet.Text != "" {
		t.Fatal("mandatory planning packet trimmed", got, err)
	}
	req.ByteCap = 16384
	req.Prefix = strings.Repeat("Escaped complete provider prefix ⚡ <constraints>\n", 500)
	if _, err = s.ReadProjectPlanningContext(t.Context(), req); !errors.Is(err, projectcontext.ErrBudget) {
		t.Fatal("full prefix omitted from hard cap", err)
	}
	for _, cap := range []int{0, projectcontext.MaximumPacketBytes + 1} {
		req.ByteCap = cap
		if _, err = s.ReadProjectPlanningContext(t.Context(), req); !errors.Is(err, projectcontext.ErrBudget) {
			t.Fatal("invalid bound accepted", cap, err)
		}
	}
	if n := queryInt(t, s.db, `SELECT count(*) FROM extreme_project_context_receipts`); n != 0 {
		t.Fatal("bounded inspection qualified a model turn", n)
	}
}
