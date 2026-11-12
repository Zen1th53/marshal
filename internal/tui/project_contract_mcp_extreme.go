//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/store"
)

type projectContractMCPReceipt struct {
	OccurrenceID string                     `json:"occurrence_id"`
	State        string                     `json:"state"`
	Accepted     bool                       `json:"accepted"`
	Current      bool                       `json:"current"`
	Contract     projectcontext.ContractRef `json:"contract"`
	Reason       string                     `json:"reason,omitempty"`
}

// Parent invocation holds proposalMCP.mu and has authenticated the native lease.
// Incoming JSON supplies data only; launch/actor/project are derived here.
func (w *Workspace) projectContractMCP(ctx context.Context, l *proposalMCPLease, method string, raw json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "project_contract_context":
		if err := proposalmcp.DecodeStrict(raw, &struct{}{}); err != nil {
			return nil, err
		}
		return w.readNativePlanningMCP(ctx, l)
	case "project_contract_context_abort":
		var args struct {
			ReadID string `json:"read_id"`
		}
		if err := proposalmcp.DecodeStrict(raw, &args); err != nil {
			return nil, err
		}
		if !validProposalID(args.ReadID) {
			return nil, model.ErrInvalid
		}
		if l.planning != nil && l.planning.readID == args.ReadID {
			l.planning = nil
		}
		return json.RawMessage(`{}`), nil
	case "project_contract_submit":
		var args proposalMCPSubmission
		if err := proposalmcp.DecodeStrict(raw, &args); err != nil {
			return nil, err
		}
		if !validProposalID(args.SubmissionID) || len(args.Proposal) > projectcontext.MaximumContractSubmissionBytes {
			return nil, projectcontext.ErrBudget
		}
		var sub store.ProjectContractSubmission
		if err := proposalmcp.DecodeStrict(args.Proposal, &sub); err != nil {
			return nil, err
		}
		if sub.Contract.Project != w.runtime.ProjectContractBinding() {
			return nil, projectcontext.Changed("project_contract_mcp_extreme.go:67")
		}
		// A secret in the draft is removed before the draft is journaled, so
		// registration never refuses it and the secret is never kept.
		if err := redactProjectContractSubmission(&sub); err != nil {
			return nil, err
		}
		p, _, err := w.store.AdmitMCPProjectContract(ctx, store.MCPProposal{ID: occurrenceMCP(l.id, args.SubmissionID), Launch: l.id, Owner: l.owner, Provider: l.provider, Submission: args.SubmissionID}, sub)
		if err != nil {
			return nil, explainProjectContractSubmission(sub, err)
		}
		return w.resumeProjectContractMCP(ctx, l, p)
	case "project_contract_receipt":
		var args struct {
			SubmissionID string `json:"submission_id"`
		}
		if err := proposalmcp.DecodeStrict(raw, &args); err != nil {
			return nil, err
		}
		if !validProposalID(args.SubmissionID) {
			return nil, model.ErrInvalid
		}
		p, err := w.store.MCPProposal(ctx, w.projectID, occurrenceMCP(l.id, args.SubmissionID))
		if err != nil {
			return nil, err
		}
		return w.encodeProjectContractMCP(ctx, p)
	case "project_contract_inventory", "project_contract_read":
		var args struct {
			After string `json:"after,omitempty"`
			Limit int    `json:"limit,omitempty"`
		}
		if err := proposalmcp.DecodeStrict(raw, &args); err != nil {
			return nil, err
		}
		if args.Limit == 0 {
			args.Limit = 4
		}
		if args.Limit < 1 || args.Limit > 8 || len(args.After) > 256 || strings.ContainsRune(args.After, 0) {
			return nil, fmt.Errorf("%w: limit must be from 1 to 8, and after must be empty or the next cursor from the previous page", model.ErrInvalid)
		}
		control := w.controlSource()
		if control == nil {
			return nil, errNoRuntime
		}
		authority, _ := control.Authority.(*runtimeControlAuthority)
		if authority == nil || authority.runtime != w.runtime || authority.localControl == nil {
			return nil, errNoRuntime
		}
		ownerContext := authority.localControl.Context(ctx)
		principal := authz.Principal{ID: l.owner, Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
		if method == "project_contract_inventory" {
			page, err := w.runtime.InspectProjectContractInventory(ownerContext, principal, args.After, args.Limit)
			if err != nil {
				return nil, err
			}
			return boundedProjectContractJSON(page)
		}
		accepted, err := w.store.ReadAcceptedProjectContract(ctx, w.runtime.ProjectContractBinding())
		if err != nil {
			return nil, err
		}
		// This is current accepted architecture inspection, not a qualified
		// execution packet. Reference bodies are retrieved separately through
		// authorized exact memory reads and ALL applicable refs enter admission.
		contract := accepted.Contract
		all := contract.Required
		contract.Required = []projectcontext.MemoryRef{}
		next := ""
		for _, ref := range all {
			if ref.ID <= args.After {
				continue
			}
			// The contract is owner inspection; an inaccessible private reference
			// must not disclose even its existence to this native reader.
			if ref.ProjectID != w.projectID {
				return nil, projectcontext.Changed("project_contract_mcp_extreme.go:143")
			}
			if _, readErr := w.runtime.InspectProjectContractMemory(ownerContext, principal, ref.ID); readErr != nil {
				if errors.Is(readErr, authz.ErrUnauthorized) {
					continue
				}
				return nil, readErr
			}
			if len(contract.Required) == args.Limit {
				next = contract.Required[len(contract.Required)-1].ID
				break
			}
			contract.Required = append(contract.Required, ref)
		}
		return boundedProjectContractJSON(struct {
			Contract  projectcontext.Contract `json:"contract"`
			Version   int64                   `json:"version"`
			Digest    string                  `json:"digest"`
			Next      string                  `json:"next_after_id,omitempty"`
			Qualified bool                    `json:"qualified"`
		}{contract, accepted.Version, accepted.Digest, next, false})
	default:
		return nil, errors.New("unknown project contract method")
	}
}

func boundedProjectContractJSON(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	// Bound the local inspection projection. The MCP child MUST separately
	// measure the entire escaped JSON-RPC response, including its actual ID.
	if len(raw) > 3072 {
		return nil, fmt.Errorf("%w: inspection page is too large; reduce limit", projectcontext.ErrBudget)
	}
	return raw, nil
}

func (w *Workspace) resumeProjectContractMCP(ctx context.Context, l *proposalMCPLease, p store.MCPProposal) (json.RawMessage, error) {
	if p.Launch != l.id || p.Owner != l.owner || p.Provider != l.provider {
		return nil, projectcontext.Changed("project_contract_mcp_extreme.go:184")
	}
	if p.State == "admitted" {
		req, err := w.runtime.RegisterMCPProjectContract(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		if err = w.queuePermissionChecked(req); err != nil {
			return nil, fmt.Errorf("contract admitted; retry the same submission to queue Decision: %w", err)
		}
		// The Board shows the draft as the owner's gate at once.
		w.refreshGatesSoon()
	}
	return w.encodeProjectContractMCP(ctx, p)
}

func (w *Workspace) encodeProjectContractMCP(ctx context.Context, p store.MCPProposal) (json.RawMessage, error) {
	var sub store.ProjectContractSubmission
	if err := proposalmcp.DecodeStrict(json.RawMessage(p.Payload), &sub); err != nil {
		return nil, err
	}
	if sub.Action != "project-contract" || sub.Contract.Project != w.runtime.ProjectContractBinding() {
		return nil, projectcontext.Changed("project_contract_mcp_extreme.go:204")
	}
	digest, err := sub.Contract.CanonicalDigest()
	if err != nil {
		return nil, err
	}
	r := projectContractMCPReceipt{OccurrenceID: p.ID, State: p.State, Contract: projectcontext.ContractRef{ID: sub.Contract.ID, Revision: sub.Contract.Revision, Digest: digest}, Reason: p.Reason}
	if p.State == "admitted" {
		r.State = "pending_permission"
	}
	if p.State == "committed" || p.State == "applied" {
		accepted, readErr := w.store.ReadAcceptedProjectContract(ctx, sub.Contract.Project)
		if readErr != nil {
			return nil, readErr
		}
		r.Accepted = true
		r.Current = accepted.Contract.ID == r.Contract.ID && accepted.Version == r.Contract.Revision && accepted.Digest == r.Contract.Digest
	}
	return boundedProjectContractJSON(r)
}

// recordProjectContractInstruction binds an owner message from the bound live
// chat to the newest pending contract draft of the live MCP launch.
func (w *Workspace) recordProjectContractInstruction(ctx context.Context, session, ref, text string, at time.Time) (app.OperatorInstructionBinding, bool, error) {
	launch := w.proposalLaunchID()
	binding, fresh, err := app.OperatorInstructionBinding{}, false, error(store.ErrNoPendingProjectContract)
	if launch != "" {
		binding, fresh, err = w.runtime.RecordProjectContractOperatorInstruction(ctx, session, launch, ref, text, at)
	}
	if errors.Is(err, store.ErrNoPendingProjectContract) {
		// The project contract IS the accepted architecture and nothing is
		// pending: there is nothing to accept, which is not an error.
		if _, readErr := w.store.ReadAcceptedProjectContract(ctx, w.runtime.ProjectContractBinding()); readErr == nil {
			return app.OperatorInstructionBinding{}, false, nil
		}
	}
	return binding, fresh, err
}

// redactProjectContractSubmission removes any secret from a contract draft
// (Extreme never refuses work for one). The draft keeps redaction.Marker where
// the value was; the value itself is never kept.
func redactProjectContractSubmission(sub *store.ProjectContractSubmission) error {
	raw, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	out, r, err := security.NewFirewall(security.FirewallConfig{}).AdmitJSON(raw)
	if err != nil || !r.Applied() {
		return err
	}
	var clean store.ProjectContractSubmission
	if err := proposalmcp.DecodeStrict(out, &clean); err != nil {
		return err
	}
	*sub = clean
	return nil
}
