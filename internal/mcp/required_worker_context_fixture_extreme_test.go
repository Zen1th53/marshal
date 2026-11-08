//go:build !governed

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
	"os"
	"testing"
	"time"
)

// The fixture accepts the exact current inventory through the real separate
// proposal and Decision journal. No execution or source attestation is fabricated.
func acceptedWorkerContext(t *testing.T, r *app.Runtime, base context.Context) context.Context {
	t.Helper()
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := control.Context(base)
	owner, ok := auth.LocalFromContext(ctx)
	if !ok {
		t.Fatal("missing actual local owner")
	}
	now := time.Now().UTC()
	rec := model.MemoryRecordV2{ID: "MEM-extra-required", ProjectID: r.ProjectID(), Revision: 1, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityOperator, Confidence: model.ConfidenceVerified, Title: "Preserve worker boundaries", Body: "Preserve exact worker scope and private source evidence.", Scope: "project", ScopeID: r.ProjectID(), ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "runtime_service", Reference: owner.ID()}}
	rec.ContentDigest = rec.CanonicalDigest()
	if err := r.Store().WriteMemoryV2(ctx, rec); err != nil {
		t.Fatal(err)
	}
	audience := store.ProjectContextAudience{Actor: owner.ID(), ScopeIDs: []string{r.ProjectID(), owner.ID()}}
	rows, inventory, err := r.Store().ProjectContractInventory(ctx, r.ProjectContractBinding(), audience)
	if err != nil {
		t.Fatal(err)
	}
	c := projectcontext.Contract{FormatVersion: 1, Project: r.ProjectContractBinding(), ID: "ARCH-extra-worker", Revision: 1, Purpose: "Execute reviewed work without omitting accepted policy", Components: []projectcontext.Component{{ID: "core", Responsibility: "Govern exact worker scope", Paths: []string{"knowledge.go", "output.txt", "cache_guard.go"}}}, Invariants: []string{"Preserve worker scope and private evidence"}, ReviewedInventoryDigest: inventory}
	for _, row := range rows {
		class := projectcontext.ContextOnly
		if row.ID == rec.ID {
			class = projectcontext.Policy
		}
		c.Required = append(c.Required, projectcontext.MemoryRef{ID: row.ID, ProjectID: row.ProjectID, Scope: row.Scope, ScopeID: row.ScopeID, Revision: row.Revision, ContentDigest: row.ContentDigest, StateFingerprint: projectcontext.MemoryFingerprint(row), Class: class, Applies: projectcontext.Applicability{ProjectWide: true}, AcceptanceOccurrence: "owner-worker-classification", Reason: "Reviewed exact fixture inventory; historical recall remains optional"})
	}
	if err := r.Store().InitMCPProposals(ctx); err != nil {
		t.Fatal(err)
	}
	source, _, err := r.Store().AdmitMCPProjectContract(ctx, store.MCPProposal{ID: "mcp:extra-contract", Launch: "extra-launch", Owner: owner.ID(), Provider: "codex", Submission: "extra-contract"}, store.ProjectContractSubmission{Action: "project-contract", Contract: c})
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := c.CanonicalDigest()
	binding, _ := json.Marshal(store.ProjectContractApprovalBinding{ProposalDigest: source.Digest, ContractDigest: digest, InventoryDigest: inventory})
	req := permission.Request{Kind: "project-contract", Object: c.ID, Scope: r.ProjectID(), Who: "Marshal", Reason: "Review exact worker contract", ProposalID: source.ID, ApprovalBinding: string(binding), SourceLaunch: source.Launch, SourceOccurrence: "mcp:extra-decision"}
	raw, _ := json.Marshal(map[string]any{"action": "decide", "id": "QUESTION-extra", "allow": true, "reason": "Reviewed exact current inventory"})
	if _, _, err := r.Store().AdmitMCPProposal(ctx, r.ProjectID(), store.MCPProposal{ID: req.SourceOccurrence, Launch: source.Launch, Owner: source.Owner, Provider: source.Provider, Submission: "extra-decision", Payload: string(raw)}); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(req)
	if err := r.Store().AcceptMCPDecision(ctx, r.ProjectID(), req.SourceOccurrence, string(request), req.Key()); err != nil {
		t.Fatal(err)
	}
	if err := r.Store().SetMCPProposalState(ctx, r.ProjectID(), req.SourceOccurrence, "applying", "protected fixture effect"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Store().CommitMCPProjectContractDecision(ctx, req, true, audience, now, model.Event{ID: "extra-contract-accepted", ProjectID: r.ProjectID(), Type: "PERMISSION_DECIDED", Timestamp: now, Data: map[string]any{"source_occurrence": req.SourceOccurrence}}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

func assertLegacyWorkerRefused(t *testing.T, r *app.Runtime, id string, err error) {
	t.Helper()
	if !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatalf("unqualified legacy route error=%v", err)
	}
	task, e := r.Task(t.Context(), id)
	if e != nil || task.Status != model.TaskReady || task.Worktree != nil || task.OwnerAgentID != nil {
		t.Fatalf("legacy refusal mutated task: %+v %v", task, e)
	}
	entries, e := os.ReadDir(statepath.Path(r.ProjectRoot(), "worktrees"))
	if e != nil || len(entries) != 0 {
		t.Fatal("legacy refusal created worktree", e)
	}
	artifacts, e := r.Artifacts(t.Context())
	if e != nil || len(artifacts) != 0 {
		t.Fatal("legacy refusal persisted artifacts", e)
	}
	grants, e := r.Store().ListCapabilityGrants(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	for _, g := range grants {
		if string(g.TaskID) == id {
			t.Fatal("legacy refusal created task capability")
		}
	}
}
