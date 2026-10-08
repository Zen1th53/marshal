package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
	"strings"
	"testing"
	"time"
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

func TestCredentialRevocationReportsPendingUntilOwnerAcknowledges(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-credential-status", false)
	if _, err := runtime.Store().Append(t.Context(), events.Event{ID: "scope-status", Type: events.EventTypeNetworkEgressRequested, RunID: "RUN-daemon", At: time.Now().UTC(), Data: map[string]any{"source": "run scope", "scope_socket": "daemon-incarnation", "provider": "codex", "brokered": true}}); err != nil {
		t.Fatal(err)
	}
	out, err := ws.cmd.Handle(t.Context(), "/permission credential revoke codex")
	if err != nil || !strings.Contains(out, "pending owner acknowledgement") || strings.Contains(out, "connections closed") {
		t.Fatalf("revoke: %s %v", out, err)
	}
	out, err = ws.cmd.Handle(t.Context(), "/permission credential status codex")
	if err != nil || !strings.Contains(out, "pending owner acknowledgement") {
		t.Fatalf("pending: %s %v", out, err)
	}
	if err := runtime.Store().AppendEvent(t.Context(), nil, model.Event{ID: "owner-closed", Type: "BROKER_SCOPE_CLOSED", ProjectID: runtime.ProjectID(), Timestamp: time.Now().UTC(), Data: map[string]any{"scope_socket": "daemon-incarnation", "provider": "codex", "decision_id": ""}}); err != nil {
		t.Fatal(err)
	}
	out, err = ws.cmd.Handle(t.Context(), "/permission credential status codex")
	if err != nil || !strings.Contains(out, "connections closed (owners acknowledged)") {
		t.Fatalf("closed: %s %v", out, err)
	}
}
