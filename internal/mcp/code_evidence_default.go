//go:build governed || !unix

package mcp

import (
	"context"

	"github.com/Zen1th53/marshal/internal/auth"
)

// Code evidence tools are an Extreme feature; Community lists and serves none.

func isCodeEvidenceTool(string) bool { return false }

func (s *Server) workerCodeEvidenceTools() []Tool { return nil }

func (s *Server) callCodeEvidenceTool(context.Context, auth.Principal, string, map[string]any) (string, bool, error) {
	return "", false, nil
}
