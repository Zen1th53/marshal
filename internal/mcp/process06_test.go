package mcp

import (
	"github.com/Zen1th53/marshal/internal/auth"
	"testing"
)

func TestProcess06ToolIsScopedAndRequiresVerificationCapability(t *testing.T) {
	found := false
	for _, tool := range NewServer(nil).listTools() {
		if tool.Name == "process06_status" {
			found = true
		}
	}
	if !found {
		t.Fatal("Process 06 status absent from MCP")
	}
	if requiredCapabilityForTool("process06_status") != auth.CapVerifyRun {
		t.Fatal("Process 06 MCP tool lacks verification capability")
	}
}
