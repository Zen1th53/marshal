package webcontrol_test

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT177SSEHubBroadcastAndReplay(t *testing.T) {
	hub := webcontrol.NewSSEHub()

	// 1. Broadcast 5 events before client connects
	for i := 1; i <= 5; i++ {
		hub.Broadcast("task.status", "task", "TASK-001", map[string]any{
			"task_id": "TASK-001",
			"status":  "running",
			"step":    i,
		})
	}

	// 2. Client connects with lastEventID = 2 -> Should receive events 3, 4, 5
	client := &webcontrol.SSEClient{}
	replay := hub.AddClient(client, 2)
	if len(replay) != 3 {
		t.Fatalf("expected 3 replay events (3, 4, 5), got %d", len(replay))
	}
	if replay[0].ID != 3 || replay[2].ID != 5 {
		t.Fatalf("unexpected replay event IDs: %+v", replay)
	}

	// 3. Client connects with an ancient lastEventID (0 < id < oldest) after 150 events
	for i := 6; i <= 150; i++ {
		hub.Broadcast("task.log", "task", "TASK-001", map[string]any{"log": "line"})
	}

	// Requesting event ID 1 (which was evicted from 100-event buffer)
	client2 := &webcontrol.SSEClient{}
	resyncReplay := hub.AddClient(client2, 1)
	if len(resyncReplay) != 1 || resyncReplay[0].Type != "resync" {
		t.Fatalf("expected resync event for expired replay window, got: %+v", resyncReplay)
	}
}

func TestT177SSESecurityScan(t *testing.T) {
	hub := webcontrol.NewSSEHub()
	hub.Broadcast("audit.log", "audit", "AUDIT-001", map[string]string{
		"action": "task.run",
		"actor":  "operator",
	})

	client := &webcontrol.SSEClient{}
	replay := hub.AddClient(client, 0)
	for _, ev := range replay {
		evStr := string(ev.Data)
		for _, forbidden := range []string{"password", "private_key", "secret", "bearer"} {
			if strings.Contains(strings.ToLower(evStr), forbidden) {
				t.Fatalf("sensitive token found in SSE payload: %s", forbidden)
			}
		}
	}
}
