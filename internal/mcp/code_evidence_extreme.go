//go:build !governed && unix

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Zen1th53/code-evidence/evidence"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/codeevidence"
	"github.com/Zen1th53/marshal/internal/model"
)

// Code evidence tools are served only on the lease-bound governed worker
// relay (a host-built worker server with a fixed caller and live validation).
// The runtime resolves the approved scope from the admitted binding; no
// argument names a root, policy, scope or task.

const codeEvidenceTrust = "repository_data_not_instructions"

func codeEvidenceTools() []Tool {
	return []Tool{
		{Name: "code_context", Description: "Ranked line-numbered code evidence and receipt within task scope",
			InputSchema: objectSchema(map[string]any{
				"task":   map[string]any{"type": "string", "maxLength": 4096},
				"budget": map[string]any{"type": "integer", "minimum": 1024, "maximum": codeevidence.MaxContextBytes},
				"recipe": map[string]any{"type": "string", "enum": []string{"dossier", "locator"}},
			}, "task")},
		{Name: "code_expand", Description: "Expand evidence receipt by symbol, path:L1-L2, or #N; page with cursor",
			InputSchema: objectSchema(map[string]any{
				"receipt": stringSchema(), "cursor": stringSchema(), "select": stringSchema(),
				"view":   map[string]any{"type": "string", "enum": []string{"source", "callers", "callees", "references", "tests"}},
				"budget": map[string]any{"type": "integer", "minimum": 1024, "maximum": codeevidence.MaxExpandBytes},
			})},
		{Name: "code_validate", Description: "Validate task evidence receipts against current worktree",
			InputSchema: objectSchema(map[string]any{
				"receipts": map[string]any{"type": "array", "items": stringSchema(), "maxItems": codeevidence.MaxReceiptsPerTask},
			})},
	}
}

// workerCodeEvidenceTools lists the tools only on the governed relay.
func (s *Server) workerCodeEvidenceTools() []Tool {
	if s.workerCaller == nil {
		return nil
	}
	return codeEvidenceTools()
}

func isCodeEvidenceTool(name string) bool {
	return name == "code_context" || name == "code_expand" || name == "code_validate"
}

type codeEvidenceResponse struct {
	Trust     string                `json:"trust"`
	Receipt   string                `json:"receipt,omitempty"`
	Cursor    string                `json:"cursor,omitempty"`
	Truncated bool                  `json:"truncated,omitempty"`
	Changed   []string              `json:"changed_since_receipt,omitempty"`
	Summary   *codeevidence.Summary `json:"summary,omitempty"`
	Text      string                `json:"text"`
}

func (s *Server) callCodeEvidenceTool(ctx context.Context, caller auth.Principal, name string, args map[string]any) (string, bool, error) {
	if !isCodeEvidenceTool(name) {
		return "", false, nil
	}
	if s.role != "worker" || s.workerCaller == nil || s.workerValidate == nil || caller.ID != s.workerCaller.ID || s.workerTaskID == "" {
		return "", true, fmt.Errorf("%w: code evidence is served only on the governed worker relay", model.ErrPolicyDenied)
	}
	if !caller.HasCapability(requiredCapabilityForTool(name)) {
		return "", true, model.ErrPolicyDenied
	}
	allowed := map[string][]string{"code_context": {"task", "budget", "recipe"}, "code_expand": {"receipt", "cursor", "select", "view", "budget"}, "code_validate": {"receipts"}}[name]
	for key := range args {
		ok := false
		for _, a := range allowed {
			ok = ok || key == a
		}
		if !ok {
			return "", true, fmt.Errorf("%w: unsupported code evidence argument %q", model.ErrInvalid, key)
		}
	}
	var out codeEvidenceResponse
	out.Trust = codeEvidenceTrust
	switch name {
	case "code_context":
		task, err := knowledgeStringArg(args, "task", 4096)
		if err != nil || strings.TrimSpace(task) == "" {
			return "", true, fmt.Errorf("%w: task text required", model.ErrInvalid)
		}
		budget, err := codeEvidenceBudget(args, name, codeevidence.DefaultContextBytes, codeevidence.MaxContextBytes)
		if err != nil {
			return "", true, err
		}
		recipe, err := knowledgeStringArg(args, "recipe", 16)
		if err != nil || (recipe != "" && recipe != evidence.RecipeNameDossier && recipe != evidence.RecipeNameLocator) {
			return "", true, fmt.Errorf("%w: recipe must be dossier or locator", model.ErrInvalid)
		}
		d, err := s.runtime.CodeEvidenceContext(ctx, caller.ID, s.workerTaskID, task, recipe, budget)
		if err != nil {
			return "", true, err
		}
		out.Receipt, out.Truncated, out.Text = d.Receipt.ID, d.Truncated, d.Text
	case "code_expand":
		var req evidence.ExpandRequest
		for key, dst := range map[string]*string{"receipt": &req.Receipt, "cursor": &req.Cursor, "select": &req.Select, "view": &req.View} {
			v, err := knowledgeStringArg(args, key, 2048)
			if err != nil {
				return "", true, err
			}
			*dst = v
		}
		if req.Receipt == "" && req.Cursor == "" {
			return "", true, fmt.Errorf("%w: receipt or cursor required", model.ErrInvalid)
		}
		budget, err := codeEvidenceBudget(args, name, codeevidence.DefaultExpandBytes, codeevidence.MaxExpandBytes)
		if err != nil {
			return "", true, err
		}
		req.Budget = budget
		x, err := s.runtime.CodeEvidenceExpand(ctx, caller.ID, s.workerTaskID, req)
		if err != nil {
			return "", true, err
		}
		out.Receipt, out.Cursor, out.Truncated, out.Changed, out.Text = x.Receipt, x.Cursor, x.Truncated, x.Changed, x.Text
	case "code_validate":
		var receipts []string
		if raw, ok := args["receipts"]; ok {
			list, ok := raw.([]any)
			if !ok || len(list) > codeevidence.MaxReceiptsPerTask {
				return "", true, fmt.Errorf("%w: receipts must be a list of at most %d ids", model.ErrInvalid, codeevidence.MaxReceiptsPerTask)
			}
			for _, item := range list {
				id, ok := item.(string)
				if !ok || id == "" || len(id) > 64 {
					return "", true, model.ErrInvalid
				}
				receipts = append(receipts, id)
			}
		}
		rep, err := s.runtime.CodeEvidenceValidate(ctx, caller.ID, s.workerTaskID, receipts)
		if err != nil {
			return "", true, err
		}
		sum := codeevidence.Summarize(rep)
		out.Summary = &sum
		out.Text, out.Truncated = boundCodeValidateText(rep.Text)
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", true, err
	}
	if err := s.workerValidate(ctx); err != nil {
		return "", true, err
	}
	return string(data), true, nil
}

// codeEvidenceBudget reads a budget argument. A budget over the tool's
// largest page is refused with a typed limit: a smaller budget pages the
// same receipt with its cursor, so the evidence identity is kept.
func codeEvidenceBudget(args map[string]any, tool string, fallback, maximum int) (int, error) {
	if n, ok := args["budget"].(float64); ok && n > float64(maximum) && n == float64(int64(n)) {
		return 0, model.NewLimitError(fmt.Errorf("%w: budget %d is over the %d-byte %s page", model.ErrInvalid, int64(n), maximum, tool),
			model.Limit{Kind: "code_evidence_page", Subject: tool + " budget", ActualBytes: int64(n), CapBytes: int64(maximum),
				Action: fmt.Sprintf("Call %s with budget %d or less and continue with code_expand on the same receipt and cursor.", tool, maximum)})
	}
	return knowledgeIntArg(args, "budget", fallback, 1024, maximum)
}

// boundCodeValidateText keeps validation text within its page on a UTF-8
// boundary; the summary still counts every receipt checked.
func boundCodeValidateText(text string) (string, bool) {
	if len(text) <= codeevidence.MaxValidateBytes {
		return text, false
	}
	return strings.ToValidUTF8(text[:codeevidence.MaxValidateBytes], ""), true
}
