package tui

import (
	"context"
	"strings"
	"testing"
)

func TestBrokerCommandQueuesPromptWithoutGranting(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	out, err := w.cmd.Handle(context.Background(), "/permission credential request codex")
	if err != nil || !strings.Contains(out, "permission queued") {
		t.Fatalf("request: %s %v", out, err)
	}
	requests := w.permissions.queue.Take(false)
	if len(requests) != 1 || requests[0].Kind != "credential" || requests[0].Object != "codex" {
		t.Fatal("fixed provider prompt not queued")
	}
	if _, err = w.cmd.Handle(context.Background(), "/permission credential revoke codex"); err == nil {
		t.Fatal("revoke bypassed authenticated control")
	}
	if _, err = w.cmd.Handle(context.Background(), "/permission credential request evil"); err == nil {
		t.Fatal("unknown profile queued")
	}
	out, err = w.cmd.Handle(context.Background(), "/permission credential allow codex")
	if err != nil || !strings.Contains(out, "Usage:") {
		t.Fatal("textual allow bypassed prompt")
	}
}
