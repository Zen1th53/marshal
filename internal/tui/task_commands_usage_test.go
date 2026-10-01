package tui

import (
	"context"
	"strings"
	"testing"
)

// Malformed task mutations answer with usage and never index missing
// arguments, whether or not the workspace is attached to a runtime.
func TestTaskMutationMalformedArgumentsShowUsage(t *testing.T) {
	h := &CommandHandler{ws: NewWorkspace(nil, "proj", "sess-1")}
	for _, args := range [][]string{
		{"create"}, {"create", "  "}, {"assign"}, {"assign", "TASK-1"},
		{"assign", "TASK-1", "agent", "extra"}, {"pause"}, {"resume"},
		{"cancel"}, {"retry"}, {"pause", "TASK-1", "extra"},
	} {
		got, err := h.handleTaskMutation(context.Background(), args)
		if err != nil || !strings.HasPrefix(got, "Usage: /task "+args[0]) {
			t.Errorf("/task %s = %q, %v; want usage", strings.Join(args, " "), got, err)
		}
	}
}
