package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/model"
)

func designRecordTool() Tool {
	return Tool{Name: "design_record", Description: "Record design stage artifact", InputSchema: objectSchema(map[string]any{
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
	if content, _ := args["content"].(string); len(content) > app.MaxDesignArtifactBytes {
		// Refused whole before anything is stored: a design is never kept in part.
		name := strings.ToValidUTF8(stringArg(args, "path"), "")
		if len(name) > 256 {
			name = strings.ToValidUTF8(name[:256], "") + "…"
		}
		return "", model.NewLimitError(fmt.Errorf("%w: design artifact %s is %d bytes, over the %d-byte design record cap", model.ErrInvalid, name, len(content), app.MaxDesignArtifactBytes),
			model.Limit{Kind: "design_record", Subject: "design artifact " + name, ActualBytes: int64(len(content)), CapBytes: app.MaxDesignArtifactBytes,
				Action: "Record a smaller artifact: split the design into separate stage artifacts or move long appendices into project files and reference them."})
	}
	rec, err := s.runtime.Memory().RecordDesign(ctx, memoryPrincipal(caller), s.runtime.ProjectID(), app.DesignRecordRequest{Stage: stringArg(args, "stage"), Provider: stringArg(args, "provider"), Version: stringArg(args, "egm_version"), Path: stringArg(args, "path"), Content: func() string { content, _ := args["content"].(string); return content }(), Digest: stringArg(args, "digest")})
	return marshalToolResult(rec, err)
}
