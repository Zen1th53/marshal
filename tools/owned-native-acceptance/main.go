// owned-native-acceptance is a manual operator harness for an owned acceptance
// project: <project-dir> <proposal.json> <out.json> [mode [arg]]. Several modes
// carry run/session IDs from one past acceptance run and refuse to act on any
// other project. Not part of the release or install path.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func run() error {
	if len(os.Args) < 4 {
		return fmt.Errorf("usage: %s <project-dir> <proposal.json> <out.json> [mode [arg]]", os.Args[0])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	r, err := app.Open(ctx, os.Args[1])
	if err != nil {
		return err
	}
	defer r.Close()
	if len(os.Args) > 4 && os.Args[4] == "cancel-five-owned-historical-parents" {
		expected := map[string]string{
			"run-PLAN-3c3c783b19c2c82a6c65f87af8401051-r3-1791700852014057399": "SESSION-real-codex-continuation",
			"run-PLAN-b59ad18685609632dca6e1d4cd860a0e-r3-1791708537134273858": "SESSION-real-codex-final-5ec",
			"run-PLAN-606b9fb044d75af186855920755d822c-r3-1791706232542513499": "SESSION-real-codex-transport-3c011",
			"run-PLAN-02ee6b26de00ba9446dbb8f917f287fa-r3-1791703768694070986": "SESSION-real-codex-modelgate-402f",
			"run-PLAN-108b77d22cb3b11bad7a79017049f5e9-r3-1791703408000989852": "SESSION-real-codex-final-402f",
		}
		local, err := r.OpenLocalControl(ctx)
		if err != nil {
			return err
		}
		outcomes := map[string]any{}
		for id, session := range expected {
			current, err := r.Execution().Engine().GetRun(ctx, id)
			if err != nil {
				return err
			}
			if current.ProjectID != projectid.ID(r.ProjectIdentity()) || current.SessionID != session || current.ParentRunID != "" || current.BaseCommit != "0781ae5e180d212be1f6a922876dc2eb333e97f9" {
				return fmt.Errorf("exact owned historical parent mismatch: %s", id)
			}
			for _, task := range current.Tasks {
				if task.State == "RUNNING" || task.LeaseID != "" || task.NativeTurn != nil {
					return fmt.Errorf("active historical task; refuse: %s", id)
				}
			}
			result, callErr := r.CommandRunControl(local.Context(ctx), app.CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: session, TargetID: "run:" + id, ExpectedVersion: current.Version, IdempotencyKey: "owner-historical-parent-cancel-bd428-" + id}, "cancel")
			if callErr != nil {
				outcomes[id] = map[string]any{"before": current, "error": callErr.Error(), "scope": "operator cancellation refused; no force/retry"}
			} else {
				outcomes[id] = map[string]any{"before": current, "result": result, "scope": "supported typed OWNER cancellation; not model decision"}
			}
		}
		body, err := json.MarshalIndent(outcomes, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(os.Args[3], body, 0600); err != nil {
			return err
		}
		fmt.Println("Five exact historical parent cancellation outcomes retained; no approval/receipt rewrite")
		return nil
	}
	if len(os.Args) > 4 && os.Args[4] == "register-owned-codex" {
		agents, err := r.Agents(ctx)
		if err != nil {
			return err
		}
		for _, a := range agents {
			if a.ModelProvider == "codex" && a.Status != model.AgentDisabled {
				return fmt.Errorf("existing Codex principal; refuse duplicate setup")
			}
		}
		a, err := r.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "owned-acceptance-codex", Role: model.RoleDeveloper, ModelProvider: "codex", ModelName: "gpt-6.1-sol"})
		if err != nil {
			return err
		}
		body, _ := json.MarshalIndent(a, "", "  ")
		if err := os.WriteFile(os.Args[3], body, 0600); err != nil {
			return err
		}
		fmt.Printf("registered actual owned Codex principal: %s; not model approval\n", a.ID)
		return nil
	}
	if len(os.Args) > 4 && os.Args[4] == "approve-owned-final-plan" {
		current, err := r.Plans().Current(ctx, projectid.ID(r.ProjectIdentity()))
		if err != nil {
			return err
		}
		if len(os.Args) < 6 || current.ID != os.Args[5] || current.Version != 1 {
			return fmt.Errorf("exact owner reviewed final plan changed")
		}
		result, err := r.Plans().ApproveVersion(ctx, projectid.ID(r.ProjectIdentity()), 1)
		if err != nil {
			return err
		}
		body, _ := json.MarshalIndent(result, "", "  ")
		if err := os.WriteFile(os.Args[3], body, 0600); err != nil {
			return err
		}
		fmt.Printf("exact owner reviewed plan approved: %s version=%d\n", result.ID, result.Version)
		return nil
	}
	if len(os.Args) > 4 && os.Args[4] == "cancel-owned-82e-lane" {
		id := "lane-2cf2078786462ea8a539e1d9186f10c6"
		run, err := r.Execution().Engine().GetRun(ctx, id)
		if err != nil {
			return err
		}
		if run.ProjectID != projectid.ID(r.ProjectIdentity()) || run.SessionID != "SESSION-real-codex-final-5ec" || run.ParentRunID != "run-PLAN-b59ad18685609632dca6e1d4cd860a0e-r3-1791708537134273858" || run.LaneTaskID != "codex" {
			return fmt.Errorf("exact owned82e lane mismatch")
		}
		local, err := r.OpenLocalControl(ctx)
		if err != nil {
			return err
		}
		result, err := r.CommandRunControl(local.Context(ctx), app.CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: run.SessionID, TargetID: "run:" + id, ExpectedVersion: run.Version, IdempotencyKey: "owned82e-expired-cancel-sgr"}, "cancel")
		if err != nil {
			return err
		}
		body, _ := json.MarshalIndent(result, "", "  ")
		if err := os.WriteFile(os.Args[3], body, 0600); err != nil {
			return err
		}
		fmt.Printf("owned expired82e lane cancelled; operator setup only\n")
		return nil
	}
	if len(os.Args) > 4 && os.Args[4] == "cancel-owned-3c-lane" {
		id := "lane-90d99dac546815fe9dbd1578c18807c2"
		run, err := r.Execution().Engine().GetRun(ctx, id)
		if err != nil {
			return err
		}
		if run.ProjectID != projectid.ID(r.ProjectIdentity()) || run.SessionID != "SESSION-real-codex-transport-3c011" || run.ParentRunID != "run-PLAN-606b9fb044d75af186855920755d822c-r3-1791706232542513499" || run.LaneTaskID != "codex" {
			return fmt.Errorf("exact owned3c lane mismatch")
		}
		local, err := r.OpenLocalControl(ctx)
		if err != nil {
			return err
		}
		result, err := r.CommandRunControl(local.Context(ctx), app.CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: run.SessionID, TargetID: "run:" + id, ExpectedVersion: run.Version, IdempotencyKey: "owned3c-expired-cancel-5ec"}, "cancel")
		if err != nil {
			return err
		}
		body, _ := json.MarshalIndent(result, "", "  ")
		if err := os.WriteFile(os.Args[3], body, 0600); err != nil {
			return err
		}
		fmt.Printf("owned expired3c lane cancelled; operator setup only\n")
		return nil
	}
	if len(os.Args) > 4 && os.Args[4] == "cancel-owned-old-lane" {
		id := "lane-dc927dc6ad9c76ffe93e8da60132d926"
		run, err := r.Execution().Engine().GetRun(ctx, id)
		if err != nil {
			return err
		}
		if run.ProjectID != projectid.ID(r.ProjectIdentity()) || run.SessionID != "SESSION-real-codex-continuation" || run.ParentRunID != "run-PLAN-3c3c783b19c2c82a6c65f87af8401051-r3-1791700852014057399" || run.LaneTaskID != "codex" {
			return fmt.Errorf("owned exact lane binding mismatch")
		}
		local, err := r.OpenLocalControl(ctx)
		if err != nil {
			return err
		}
		result, err := r.CommandRunControl(local.Context(ctx), app.CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: run.SessionID, TargetID: "run:" + id, ExpectedVersion: run.Version, IdempotencyKey: "owned-old-lane-cancel-402f"}, "cancel")
		if err != nil {
			return err
		}
		body, _ := json.MarshalIndent(result, "", "  ")
		if err := os.WriteFile(os.Args[3], body, 0600); err != nil {
			return err
		}
		fmt.Printf("owned operator cancellation: %s %s version=%d; not model approval\n", result.Run.RunID, result.Run.State, result.Run.Version)
		return nil
	}
	if len(os.Args) > 4 && os.Args[4] == "probe:opencode" {
		profile, err := r.VerifyMarshalWorker(ctx, "opencode", "ollama/qwythos-9b")
		if err != nil {
			return err
		}
		fmt.Printf("actual enforced harness probe recorded: opencode %s\n", profile.ProbeEvidenceID)
		return nil
	}
	if len(os.Args) > 4 && os.Args[4] == "codex-plan" {
		body, err := os.ReadFile(os.Args[2])
		if err != nil {
			return err
		}
		draft, err := r.Marshal().DraftFromProposal(body, "codex")
		if err != nil {
			return err
		}
		profile, err := r.Store().GetHarnessProfile(ctx, "codex")
		if err != nil || profile == nil {
			return fmt.Errorf("actual Codex capability receipt absent: %v", err)
		}
		capacity := goalintake.UnknownCapacity("codex", true)
		sessionID := "SESSION-real-codex-final-402f"
		if len(os.Args) > 5 {
			sessionID = os.Args[5]
		}
		req := app.CreatePlanRequest{SessionID: sessionID, ProjectID: projectid.ID(r.ProjectIdentity()), Mode: plan.ModeUltra, Checks: map[string][]string{}, TaskProviders: map[string]string{}, Candidates: []goalintake.Candidate{{Provider: "codex", Model: profile.DefaultModel, Capacity: capacity, Governance: constitution.GovernanceVerified}}, HarnessCandidates: []plan.HarnessCandidate{{Profile: *profile, InstalledVersion: profile.InstalledVersion, Provider: "codex", Capacity: capacity}}}
		for _, task := range draft.Tasks {
			if task.Worker == "codex" {
				req.TaskProviders[task.PlanTaskID] = "codex"
				req.Checks[task.PlanTaskID] = draft.Plan.Checks[task.PlanTaskID]
				for _, p := range draft.Plan.Tasks {
					if p.ID == task.PlanTaskID {
						req.Tasks = append(req.Tasks, p)
					}
				}
			}
		}
		p, err := r.Plans().Create(ctx, req)
		if err != nil {
			return err
		}
		body, err = json.MarshalIndent(p, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(os.Args[3], append(body, '\n'), 0600); err != nil {
			return err
		}
		fmt.Printf("canonical existing-model-data Codex-only plan: %s version=%d state=%s; NOT APPROVED\n", p.ID, p.Version, p.State)
		return nil
	}
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		return err
	}
	for _, provider := range []string{"codex", "claude"} {
		req := permission.Request{Kind: "credential", Object: provider, Scope: "this owned private acceptance project, until revoked", Who: "operator", Reason: "Owner explicitly authorized bounded paid provider acceptance tests"}
		if err := r.CommandPermission(local.Context(ctx), req, true, "owner-authorized private acceptance operator consent"); err != nil {
			return err
		}
		fmt.Printf("operator credential consent committed: %s\n", provider)
	}
	body, err := os.ReadFile(os.Args[2])
	if err != nil {
		return err
	}
	draft, err := r.Marshal().DraftFromProposal(body, "codex")
	if err != nil {
		return err
	}
	req := app.CreatePlanRequest{SessionID: "SESSION-real-three-lane", ProjectID: projectid.ID(r.ProjectIdentity()), Mode: plan.ModeUltra, Tasks: draft.Plan.Tasks, Checks: draft.Plan.Checks, TaskProviders: map[string]string{}}
	models := map[string]string{"codex": "gpt-6.1-sol", "claude": "sonnet", "opencode": "ollama/qwythos-9b"}
	for _, task := range draft.Tasks {
		profile, err := r.VerifyMarshalWorker(ctx, task.Worker, models[task.Worker])
		if err != nil {
			return fmt.Errorf("actual %s sandboxed probe: %w", task.Worker, err)
		}
		capacity := goalintake.UnknownCapacity(task.Worker, true)
		req.TaskProviders[task.PlanTaskID] = task.Worker
		req.Candidates = append(req.Candidates, goalintake.Candidate{Provider: task.Worker, Model: profile.DefaultModel, Capacity: capacity, Governance: constitution.GovernanceVerified})
		req.HarnessCandidates = append(req.HarnessCandidates, plan.HarnessCandidate{Profile: profile, InstalledVersion: profile.InstalledVersion, Provider: task.Worker, Capacity: capacity})
		fmt.Printf("actual enforced harness probe recorded: %s %s\n", task.Worker, profile.ProbeEvidenceID)
	}
	p, err := r.Plans().Create(ctx, req)
	if err != nil {
		return err
	}
	body, err = json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(os.Args[3], append(body, '\n'), 0600); err != nil {
		return err
	}
	fmt.Printf("canonical model-data plan ready: %s version=%d state=%s; NOT APPROVED\n", p.ID, p.Version, p.State)
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
