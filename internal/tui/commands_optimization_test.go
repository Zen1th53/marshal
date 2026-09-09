package tui

import (
	"context"
	"strings"
	"testing"
)

func TestOptimizationCommandIsReadOnlyAndFailClosed(t *testing.T) {
	ws := NewWorkspace(nil, "project", "session")
	h := NewCommandHandler(ws)
	got, err := h.Handle(context.Background(), "/optimization cycle-1")
	if err != nil || got != "Canonical optimization store unavailable." {
		t.Fatalf("optimization command = %q, %v", got, err)
	}
	got, err = h.Handle(context.Background(), "/optimization")
	if err != nil || !strings.Contains(got, "Usage:") {
		t.Fatalf("usage = %q, %v", got, err)
	}
}
