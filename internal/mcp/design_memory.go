package mcp

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/model"
)

func designRecordTool() Tool {
	return Tool{Name: "design_record", Description: "Record design artifact bytes as unverified candidates; path is provenance, never a filesystem read or approval.", InputSchema: objectSchema(map[string]any{
		"stage": stringSchema(), "provider": stringSchema(), "egm_version": stringSchema(), "path": stringSchema(), "content": map[string]any{"type": "string", "maxLength": app.MaxDesignArtifactBytes}, "digest": stringSchema(),
	}, "stage", "provider", "egm_version", "path", "content")}
}
func (s *Server) recordDesign(ctx context.Context, caller auth.Principal, args map[string]any) (string, error) {
	for key, value := range args {
		if _, ok := value.(string); !ok {
			return "", model.ErrInvalid
		}
		switch key {
		case "stage", "provider", "egm_version", "path", "content", "digest":
		default:
			return "", fmt.Errorf("%w: unknown design_record argument %s", model.ErrInvalid, key)
		}
	}
	rec, err := s.runtime.Memory().RecordDesign(ctx, memoryPrincipal(caller), s.runtime.ProjectID(), app.DesignRecordRequest{Stage: stringArg(args, "stage"), Provider: stringArg(args, "provider"), Version: stringArg(args, "egm_version"), Path: stringArg(args, "path"), Content: func() string { content, _ := args["content"].(string); return content }(), Digest: stringArg(args, "digest")})
	return marshalToolResult(rec, err)
}
