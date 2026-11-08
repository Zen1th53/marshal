//go:build !governed

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestExtremeMCPCLIRejectsInvalidScope(t *testing.T) {
	for _, scope := range [][]string{{"--role", "admin"}, {"--role", "worker"}, {"--role", "marshal", "--task", "TASK-1"}, {"--role", "worker", "--task", "TASK-1", "unexpected"}} {
		var stdout, stderr bytes.Buffer
		args := append([]string{"mcp", "serve", "--stdio"}, scope...)
		if code := Execute(context.Background(), t.TempDir(), args, strings.NewReader(""), &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "invalid MCP role/task scope") {
			t.Fatalf("args=%v code=%d stderr=%s", args, code, stderr.String())
		}
	}
}
