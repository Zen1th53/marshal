//go:build governed

package mcp

import (
	"context"
	"encoding/json"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
)

func withMCPResponseID(ctx context.Context, _ any) context.Context { return ctx }

func memoryRecallTool() Tool {
	return Tool{
		Name: "memory_recall", Description: "Search memory using progressive hybrid retrieval",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"project_id": stringSchema(), "query": stringSchema()}, "required": []string{"project_id", "query"}},
	}
}

func (s *Server) recallMemory(ctx context.Context, principal authz.Principal, args map[string]any) (string, error) {
	project, _ := args["project_id"].(string)
	query, _ := args["query"].(string)
	if project == "" {
		if p, err := s.runtime.Store().Project(ctx); err == nil {
			project = p.ID
		}
	}
	res, err := s.runtime.Memory().Recall(ctx, principal, app.RecallRequest{ProjectID: project, Query: query})
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(res)
	return string(data), err
}
