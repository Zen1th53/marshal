//go:build !governed

package mcp

import (
	"context"
	"encoding/json"
	"errors"
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
		}
		return proposalmcp.Call(ctx, cfg, method, raw)
	}
}

func liveProposalTool(name string) bool {
	switch name {
	case "marshal_proposal_submit", "marshal_proposal_receipt", "marshal_memory_candidate_submit", "marshal_memory_receipt", "memory_remember", "marshal_project_contract_submit", "marshal_project_contract_receipt", "marshal_project_contract_inventory", "marshal_project_contract_read", "marshal_project_contract_context":
		return true
	default:
		return false
	}
}

func projectContractTool(name string) bool {
	switch name {
	case "marshal_project_contract_submit", "marshal_project_contract_receipt", "marshal_project_contract_inventory", "marshal_project_contract_read", "marshal_project_contract_context":
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
		{Name: "marshal_project_contract_context", Description: "Read one complete current project purpose, architecture, active Goal and ALL mandatory memories. Full packet or refusal; inspection is data only and grants no execution authority.", InputSchema: objectSchema(map[string]any{})},
		{Name: "marshal_project_contract_submit", Description: "Submit a bounded project purpose, architecture and required-memory draft to the live parent. A separate Decision is mandatory; pending is never accepted. Read every marshal_project_contract_inventory page first. proposal = {\"action\":\"project-contract\",\"expected_version\":<inventory current_version>,\"contract\":{\"format_version\":1,\"project\":<inventory project>,\"id\":\"<id>\",\"revision\":<expected_version+1>,\"purpose\":\"...\",\"components\":[{\"id\":\"...\",\"responsibility\":\"...\",\"paths\":[\"<repo path>\"]}],\"invariants\":[\"...\"],\"required\":[],\"reviewed_inventory_digest\":\"<inventory completeness_digest>\"}}.", InputSchema: objectSchema(map[string]any{"submission_id": stringSchema(), "proposal": map[string]any{"type": "object"}}, "submission_id", "proposal")},
		{Name: "marshal_project_contract_receipt", Description: "Read durable contract decision and acceptance; inspection never grants execution.", InputSchema: objectSchema(map[string]any{"submission_id": stringSchema()}, "submission_id")},
		{Name: "marshal_project_contract_inventory", Description: "Inspect a bounded page of canonical accepted memories requiring explicit classification. Pages are inspection only and cannot omit required execution context.", InputSchema: objectSchema(map[string]any{"after": stringSchema(), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 8}})},
		{Name: "marshal_project_contract_read", Description: "Inspect the currently accepted project contract and a bounded reference page. Missing accepted architecture remains review pending.", InputSchema: objectSchema(map[string]any{"after": stringSchema(), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 8}})},
		{Name: "marshal_memory_candidate_submit", Description: "Submit a bounded source-labelled candidate in the live parent; the runtime applies the Marshal's own candidate without a decision turn. Confirm with marshal_memory_receipt", InputSchema: objectSchema(map[string]any{"submission_id": stringSchema(), "title": stringSchema(), "body": stringSchema(), "sources": memorySourceSchema()}, "submission_id", "title", "body")},
		{Name: "marshal_memory_receipt", Description: "Inspect candidate retention and indexing; pending is not persisted", InputSchema: objectSchema(map[string]any{"submission_id": stringSchema()}, "submission_id")},
		{Name: "marshal_proposal_submit", Description: "Submit an exact validated proposal from this live Marshal launch; admitted is not applied. Harness verification: {action:harness-verify,provider:codex|claude|opencode|agy,model:<optional explicit selector>}; verify only, never accept architecture. Operator proposals: {action:reassign,task_id:<task>,target_worker:<worker>}, {action:resume}, {action:retry,task_id:<task>,fresh_session:true|false}, {action:amend-approve}, {action:amend-deny}. The parent validates supported actions and requires the same owner acceptance as local controls; inspect the receipt for rejection, pending decision, failure or application. Decision proposals require exactly four fields: action=decide, nonempty id, boolean allow, and nonblank reason of at most 500 UTF-8 bytes with no control characters.", InputSchema: objectSchema(map[string]any{"submission_id": stringSchema(), "proposal": map[string]any{"type": "object"}}, "submission_id", "proposal")},
		{Name: "marshal_proposal_receipt", Description: "Read durable proposal acceptance and application outcome for this launch", InputSchema: objectSchema(map[string]any{"submission_id": stringSchema()}, "submission_id")}}
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
	return Tool{Name: "memory_remember", Description: "Retain private memory through the bound live parent; the runtime applies the Marshal's own write without a decision turn (pending briefly until applied). Title<=256 bytes, body<=4096 bytes. Returns pending/persisted/indexed truthfully; standalone retention is refused.", InputSchema: objectSchema(map[string]any{"project_id": stringSchema(), "submission_id": stringSchema(), "title": stringSchema(), "body": stringSchema(), "sources": memorySourceSchema()}, "title", "body")}
}
func memorySourceSchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 8, "items": objectSchema(map[string]any{"path": stringSchema(), "commit": stringSchema(), "session": stringSchema(), "observed_at": stringSchema(), "sha256": stringSchema()})}
}
