//go:build governed

package mcp

import (
	"context"
	"github.com/Zen1th53/marshal/internal/auth"
	"strings"
	"testing"
)

func TestDefaultMCPStatusAliasUnchanged(t *testing.T) {
	server := NewServer(nil)
	_, err := server.callTool(context.Background(), auth.Principal{}, "status", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown tool status") {
		t.Fatalf("default status alias behavior changed: %v", err)
	}
	if len(server.listTools()) <= 3 {
		t.Fatal("default tool list narrowed")
	}
}
