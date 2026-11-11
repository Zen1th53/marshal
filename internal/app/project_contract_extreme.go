//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/store"
)

// OperatorInstructionBinding names the exact artifact a recorded owner
// instruction authorizes: the pending project contract draft the owner's
// browser Accept stands for.
type OperatorInstructionBinding struct {
	Kind, Ref, ID string
	Revision      int64
	Digest        string
}

func (r *Runtime) ProjectContractBinding() projectcontext.ProjectBinding {
	return projectcontext.ProjectBinding{CanonicalID: r.ProjectIdentity(), StorageID: r.ProjectID()}
}

// RegisterMCPProjectContract reconstructs an exact decision request from the
// parent's immutable checked draft. A request is never contract acceptance.
// Its caller owns the authenticated native lease while admitting the draft.
func (r *Runtime) RegisterMCPProjectContract(ctx context.Context, occurrence string) (permission.Request, error) {
	p, err := r.store.MCPProposal(ctx, r.ProjectID(), occurrence)
	if err != nil {
		return permission.Request{}, err
	}
	if p.State != "admitted" {
		return permission.Request{}, model.ErrConflict
	}
	var sub store.ProjectContractSubmission
	if err = proposalmcp.DecodeStrict(json.RawMessage(p.Payload), &sub); err != nil {
		return permission.Request{}, err
	}
	c, err := sub.Contract.Canonical()
	if err != nil {
		return permission.Request{}, err
	}
	if sub.Action != "project-contract" || c.Project != r.ProjectContractBinding() || sub.ExpectedVersion < 0 || c.Revision != sub.ExpectedVersion+1 {
		return permission.Request{}, projectcontext.Changed("project_contract_extreme.go:45")
	}
	digest, err := c.CanonicalDigest()
	if err != nil {
		return permission.Request{}, err
	}
	if err = security.NewFirewall(security.FirewallConfig{}).ScanText(p.Payload); err != nil {
		return permission.Request{}, err
	}
	binding, err := json.Marshal(store.ProjectContractApprovalBinding{ProposalDigest: p.Digest, ContractDigest: digest, InventoryDigest: c.ReviewedInventoryDigest, ExpectedVersion: sub.ExpectedVersion})
	if err != nil {
		return permission.Request{}, err
	}
	return permission.Request{Kind: "project-contract", Object: c.ID, Scope: r.ProjectID(), Who: "Marshal", Reason: "Review exact project purpose, architecture and mandatory memory classification; submission alone grants nothing", ProposalID: p.ID, SourceOccurrence: p.ID, SourceLaunch: p.Launch, ApprovalBinding: string(binding)}, nil
}

func privateProjectContractKind(req permission.Request) bool { return req.Kind == "project-contract" }

// This hook runs only after CommandPermission's authenticated local-control
// check and TUI's live, held native lease application guard. The store further
// requires a DIFFERENT applying Decision and its exact journaled request.
func (r *Runtime) commitBrokerProjectContractPermission(ctx context.Context, req permission.Request, allow bool, audit model.Event) (bool, error) {
	if !privateProjectContractKind(req) {
		return false, nil
	}
	if req.Scope != r.ProjectID() || req.SourceLaunch == "" || req.SourceOccurrence == "" || req.SourceOccurrence == req.ProposalID {
		return true, projectcontext.Changed("project_contract_extreme.go:71")
	}
	decision, err := r.store.MCPProposal(ctx, r.ProjectID(), req.SourceOccurrence)
	if err != nil {
		return true, err
	}
	if decision.State != "applying" || decision.Launch != req.SourceLaunch {
		return true, projectcontext.Changed("project_contract_extreme.go:78")
	}
	// The native decision owner is supplied by the checked parent journal,
	// never JSON authority labels. Exact ACLs still apply to referenced records.
	audience, err := r.projectContractOwnerAudience(ctx, decision.Owner)
	if err != nil {
		return true, err
	}
	// The Marshal decides its own contract draft here. An allow is the
	// Marshal's acceptance on the owner's command, so the same transaction
	// consumes the owner's recorded instruction for this exact contract.
	_, err = r.store.CommitMCPProjectContractDecisionOnInstruction(ctx, req, allow, audience, time.Now().UTC(), audit)
	return true, err
}

// RecordProjectContractOperatorInstruction binds the owner's browser Accept to
// the newest unaccepted contract draft of the live MCP launch, as the owner's
// recorded instruction for exactly that contract.
func (r *Runtime) RecordProjectContractOperatorInstruction(ctx context.Context, session, launch, ref, text string, at time.Time) (OperatorInstructionBinding, bool, error) {
	if r == nil || r.store == nil || !store.ArchitectureAcceptanceInstruction(text) {
		return OperatorInstructionBinding{}, false, model.ErrPolicyDenied
	}
	i, fresh, err := r.store.RecordProjectContractInstruction(ctx, r.ProjectContractBinding(), session, launch, ref, text, at)
	if err != nil {
		return OperatorInstructionBinding{}, false, err
	}
	return OperatorInstructionBinding{Kind: "project-contract", Ref: i.Ref, ID: i.ContractID, Revision: i.Revision, Digest: i.ContractDigest}, fresh, nil
}

// Inventory inspection authorizes before entering the short SQL snapshot.
// Caller principals are supplied by trusted workspace composition; no MCP
// input may supply a role, actor or allowed scope set.
func (r *Runtime) InspectProjectContractInventory(ctx context.Context, principal authz.Principal, after string, limit int) (store.ProjectContractInventoryPage, error) {
	if err := r.authorizeProjectContractInspection(ctx, principal); err != nil {
		return store.ProjectContractInventoryPage{}, err
	}
	audience, err := r.projectContractOwnerAudience(ctx, principal.ID)
	if err != nil {
		return store.ProjectContractInventoryPage{}, err
	}
	return r.store.ReadProjectContractInventoryPage(ctx, r.ProjectContractBinding(), audience, after, limit)
}

func (r *Runtime) authorizeProjectContractInspection(ctx context.Context, principal authz.Principal) error {
	owner, ok := auth.LocalFromContext(ctx)
	if r == nil || !ok || ctx.Value(localControlKey{}) != r || owner.ProjectID() != r.ProjectIdentity() || principal.ID == "" || !principalHasAuthority(principal, authz.AuthorityPolicyAdmin) {
		return authz.ErrDenied
	}
	if r.memoryService == nil {
		return model.ErrUnavailable
	}
	return r.memoryService.authorizer.Authorize(ctx, principal, authz.ActionMemoryRecall, r.ProjectID(), model.MemoryDurable)
}

func (r *Runtime) projectContractOwnerAudience(ctx context.Context, actor string) (store.ProjectContextAudience, error) {
	ids, err := r.store.ProjectContractTaskScopes(ctx, r.ProjectID())
	if err != nil {
		return store.ProjectContextAudience{}, err
	}
	return store.ProjectContextAudience{Actor: actor, ScopeIDs: append([]string{r.ProjectID(), actor}, ids...)}, nil
}

// InspectProjectContractMemory enforces canonical project task scope and ACL
// before retrieving a body. PolicyAdmin is derived by the authenticated native
// parent, never from JSON. It cannot disclose foreign or fabricated task scopes.
func (r *Runtime) InspectProjectContractMemory(ctx context.Context, principal authz.Principal, id string) (model.MemoryRecordV2, error) {
	if err := r.authorizeProjectContractInspection(ctx, principal); err != nil {
		return model.MemoryRecordV2{}, err
	}
	return r.store.ReadProjectContractOwnerMemory(ctx, r.ProjectID(), principal.ID, id)
}
