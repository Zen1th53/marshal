//go:build !governed

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/statepath"
	"path/filepath"
)

func (s *Server) ConfigureProposalMCP(cfg proposalmcp.Config) {
	if cfg.Namespace != statepath.DirName {
		s.proposalCall = nil
		return
	}
	s.proposalCall = nil
	if s.runtime == nil {
		return
	}
	actual, err := filepath.EvalSymlinks(s.runtime.ProjectRoot())
	if err != nil || s.role != "marshal" || cfg.ProjectID == "" || cfg.ProjectID != s.runtime.ProjectID() || cfg.ProjectRoot != actual {
		return
	}
	s.proposalCall = func(ctx context.Context, name string, raw json.RawMessage) (json.RawMessage, error) {
		if s.role != "marshal" {
			return nil, errors.New("proposal MCP requires live Marshal")
		}
		method := "submit"
		switch name {
		case "marshal_design_review":
			method = "design_review"
		case "marshal_memory_candidate_submit", "memory_remember":
			method = "memory_submit"
		case "marshal_memory_receipt":
			method = "memory_receipt"
		case "marshal_proposal_receipt":
			method = "receipt"
		case "marshal_project_contract_submit":
			method = "project_contract_submit"
		case "marshal_project_contract_receipt":
			method = "project_contract_receipt"
		case "marshal_project_contract_inventory":
			method = "project_contract_inventory"
		case "marshal_project_contract_read":
			method = "project_contract_read"
		case "marshal_project_contract_context":
			method = "project_contract_context"
		case "marshal_project_contract_context_abort":
			method = "project_contract_context_abort"
		case "marshal_architecture_request_answer":
			method = "architecture_request_answer"
		case "marshal_architecture_at":
			method = "project_contract_at_commit"
		case "marshal_architecture_history":
			method = "project_contract_history"
		case "marshal_architecture_conformance":
			method = "project_contract_conformance"
		}
		return proposalmcp.Call(ctx, cfg, method, raw)
	}
}

// checkLiveSubmission refuses a mistyped proposal here, with the field that is
// wrong, before it travels to the runtime. The runtime checks it again and
// keeps every authority and byte-limit decision; an envelope or a proposal over
// its cap is left to it so its typed limit refusal is unchanged.
func checkLiveSubmission(name string, raw json.RawMessage) error {
	validate, limit := proposalmcp.ValidateProposal, proposalmcp.MaxProposalBytes
	switch name {
	case "marshal_proposal_submit":
	case "marshal_project_contract_submit":
		validate, limit = proposalmcp.ValidateContractSubmission, proposalmcp.MaxContractProposalBytes
	default:
		return nil
	}
	var args struct {
		SubmissionID string          `json:"submission_id"`
		Proposal     json.RawMessage `json:"proposal"`
	}
	if err := proposalmcp.DecodeStrict(raw, &args); err != nil {
		return fmt.Errorf("%w: arguments must be exactly {submission_id, proposal}: %v", model.ErrInvalid, err)
	}
	if len(args.Proposal) == 0 || len(args.Proposal) > limit {
		return nil
	}
	if err := validate(args.Proposal); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}
	return nil
}

// submitSchema is the exact outer shape of both submit tools.
func submitSchema(proposal map[string]any) map[string]any {
	schema := objectSchema(map[string]any{"submission_id": stringSchema(), "proposal": proposal}, "submission_id", "proposal")
	schema["additionalProperties"] = false
	return schema
}

func liveProposalTool(name string) bool {
	switch name {
	case "marshal_design_review", "marshal_proposal_submit", "marshal_proposal_receipt", "marshal_memory_candidate_submit", "marshal_memory_receipt", "memory_remember", "marshal_architecture_request_answer", "marshal_architecture_at", "marshal_architecture_history", "marshal_architecture_conformance", "marshal_project_contract_submit", "marshal_project_contract_receipt", "marshal_project_contract_inventory", "marshal_project_contract_read", "marshal_project_contract_context":
		return true
	default:
		return false
	}
}

func projectContractTool(name string) bool {
	switch name {
	case "marshal_project_contract_submit", "marshal_project_contract_receipt", "marshal_project_contract_inventory", "marshal_project_contract_read", "marshal_project_contract_context", "marshal_architecture_at", "marshal_architecture_history", "marshal_architecture_conformance":
		return true
	default:
		return false
	}
}
func (s *Server) proposalTools() []Tool {
	if s.role != "marshal" || s.proposalCall == nil {
		return nil
	}
	return []Tool{
		{Name: "marshal_design_review", Description: "Start/poll runtime design review; repeat identical arguments", InputSchema: objectSchema(map[string]any{"provider": stringSchema(), "draft": stringSchema(), "reading_list": map[string]any{"type": "array", "items": stringSchema()}}, "provider", "draft")},
		{Name: "marshal_project_contract_context", Description: "Read project contract, goal, and memories; page with read_id", InputSchema: objectSchema(map[string]any{"read_id": stringSchema(), "page": map[string]any{"type": "integer", "minimum": 2}})},
		{Name: "marshal_project_contract_submit", Description: "Submit project contract; contract.project may be left out; owner accepts in browser", InputSchema: submitSchema(proposalmcp.ContractSubmissionSchema())},
		{Name: "marshal_project_contract_receipt", Description: "Contract submission receipt", InputSchema: objectSchema(map[string]any{"submission_id": stringSchema()}, "submission_id")},
		{Name: "marshal_project_contract_inventory", Description: "Page memories to classify in contract (after=next_after_id)", InputSchema: objectSchema(map[string]any{"after": stringSchema(), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 8}})},
		{Name: "marshal_project_contract_read", Description: "Read accepted contract and reference page (after=next_after_id)", InputSchema: objectSchema(map[string]any{"after": stringSchema(), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 8}})},
		{Name: "marshal_memory_candidate_submit", Description: "Submit memory candidate (title<=256B, body<=4096B)", InputSchema: objectSchema(map[string]any{"submission_id": stringSchema(), "title": stringSchema(), "body": stringSchema(), "sources": memorySourceSchema()}, "submission_id", "title", "body")},
		{Name: "marshal_memory_receipt", Description: "Memory submission receipt", InputSchema: objectSchema(map[string]any{"submission_id": stringSchema()}, "submission_id")},
		{Name: "marshal_proposal_submit", Description: "Submit proposal; keys per action in oneOf", InputSchema: submitSchema(proposalmcp.ProposalSchema())},
		{Name: "marshal_architecture_request_answer", Description: "Answer architecture request (submit contract first for proposed_change)", InputSchema: objectSchema(map[string]any{"request_id": stringSchema(), "status": map[string]any{"type": "string", "enum": []string{"answered", "needs_clarification", "proposed_change"}}, "reply": stringSchema(), "questions": map[string]any{"type": "array", "maxItems": 8, "items": stringSchema()}}, "request_id", "status", "reply")},
		{Name: "marshal_architecture_at", Description: "Recorded architecture at commit (default HEAD)", InputSchema: objectSchema(map[string]any{"commit": stringSchema(), "component": stringSchema()})},
		{Name: "marshal_architecture_history", Description: "Recorded architecture bindings newest first", InputSchema: objectSchema(map[string]any{"component": stringSchema(), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200}})},
		{Name: "marshal_architecture_conformance", Description: "Check commit (default HEAD) against accepted architecture", InputSchema: objectSchema(map[string]any{"commit": stringSchema()})},
		{Name: "marshal_proposal_receipt", Description: "Proposal submission receipt", InputSchema: objectSchema(map[string]any{"submission_id": stringSchema()}, "submission_id")}}
}

func (s *Server) rememberViaProposalMCP(ctx context.Context, args map[string]any) (string, bool, error) {
	if s.role != "marshal" || s.proposalCall == nil {
		return "", true, errors.New("memory retention requires a bound live Marshal; no candidate was persisted")
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return "", true, err
	}
	out, err := s.proposalCall(ctx, "memory_remember", raw)
	return string(out), true, err
}

func memoryRememberTool() Tool {
	return Tool{Name: "memory_remember", Description: "Retain memory (title<=256B, body<=4096B)", InputSchema: objectSchema(map[string]any{"project_id": stringSchema(), "submission_id": stringSchema(), "title": stringSchema(), "body": stringSchema(), "sources": memorySourceSchema()}, "title", "body")}
}
func memorySourceSchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 8, "items": objectSchema(map[string]any{"path": stringSchema(), "commit": stringSchema(), "session": stringSchema(), "observed_at": stringSchema(), "sha256": stringSchema()})}
}
