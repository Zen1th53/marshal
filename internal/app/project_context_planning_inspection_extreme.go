//go:build !governed

package app

import (
	"context"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

type ProjectPlanningInspection struct {
	Context       string                     `json:"context"`
	Contract      projectcontext.ContractRef `json:"contract"`
	GoalID        string                     `json:"goal_id"`
	GoalRevision  int64                      `json:"goal_revision"`
	PacketDigest  string                     `json:"packet_digest"`
	RequiredCount int                        `json:"required_count"`
	Qualified     bool                       `json:"qualified"`
}

// Trusted native-parent composition supplies principal/session. This is a
// complete current inspection packet, not an invocation or execution grant.
// Ordinary ranked Recall and paged reference reads cannot replace this packet.
func (r *Runtime) InspectProjectPlanningContext(ctx context.Context, principal authz.Principal, session string, maxBytes int) (ProjectPlanningInspection, error) {
	var zero ProjectPlanningInspection
	if err := r.authorizeProjectContractInspection(ctx, principal); err != nil {
		return zero, err
	}
	goal, err := r.store.GetActiveGoalContract(ctx, session)
	if err != nil {
		return zero, err
	}
	head, err := runtimeProofGit(ctx, r.ProjectRoot(), "rev-parse", "HEAD")
	if err != nil {
		return zero, err
	}
	head = strings.TrimSpace(head)
	tree, err := execution.WorkspaceTreeDigest(r.ProjectRoot())
	if err != nil {
		return zero, err
	}
	audience, err := r.projectContractOwnerAudience(ctx, principal.ID)
	if err != nil {
		return zero, err
	}
	b := projectcontext.Binding{FormatVersion: projectcontext.FormatVersion, Project: r.ProjectContractBinding(), Principal: principal.ID, Session: session, GoalID: goal.ID, GoalRevision: goal.Revision, Base: head, Tree: tree}
	view, err := r.store.ReadProjectPlanningContext(ctx, store.ProjectPlanningContextRequest{Binding: b, Audience: audience, ByteCap: maxBytes, Now: time.Now().UTC()})
	if err != nil {
		return zero, err
	}
	if err := security.NewFirewall(security.FirewallConfig{}).ScanText(view.Packet.Text); err != nil {
		return zero, err
	}
	grounding := &requiredProjectGroundingBudget{Hashes: map[string]string{}}
	for _, rec := range view.Snapshot.Records {
		inputs, err := requiredProjectFreshnessInputsBounded(r.ProjectRoot(), head, rec, grounding)
		if err != nil {
			return zero, err
		}
		if r.memoryService.EvaluateFreshness(rec, inputs).Classification != FreshnessFresh {
			return zero, projectcontext.Changed("project_context_planning_inspection_extreme.go:67")
		}
	}
	afterHead, err := runtimeProofGit(ctx, r.ProjectRoot(), "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(afterHead) != head {
		return zero, projectcontext.Changed("project_context_planning_inspection_extreme.go:72")
	}
	afterTree, err := execution.WorkspaceTreeDigest(r.ProjectRoot())
	if err != nil || afterTree != tree {
		return zero, projectcontext.Changed("project_context_planning_inspection_extreme.go:76")
	}
	return ProjectPlanningInspection{Context: view.Packet.Text, Contract: view.Snapshot.CurrentContract, GoalID: goal.ID, GoalRevision: goal.Revision, PacketDigest: view.Packet.Digest, RequiredCount: len(view.Packet.Loaded)}, nil
}
