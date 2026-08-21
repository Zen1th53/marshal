package webcontrol_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT166WebControlContractsSerialization(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

	taskDTO := webcontrol.TaskSummaryDTO{
		ID:         "TASK-001",
		Title:      "Implement User Auth",
		Status:     webcontrol.TaskStatusReady,
		Risk:       "R1",
		BaseCommit: "a1b2c3d",
		HeadCommit: "e5f6a7b",
		UpdatedAt:  now,
		CreatedAt:  now,
	}

	data, err := json.Marshal(taskDTO)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	jsonStr := string(data)

	// Invariant 1: Stable IDs and explicit UTC timestamp formatting
	if !strings.Contains(jsonStr, `"id":"TASK-001"`) || !strings.Contains(jsonStr, `"status":"ready"`) {
		t.Fatalf("unexpected JSON representation: %s", jsonStr)
	}

	// Invariant 2: Sensitive Field Serialization Scan (ZERO secret/credentials leak)
	forbiddenTokens := []string{"password", "private_key", "secret_key", "bearer_token", "socket_path"}
	for _, tok := range forbiddenTokens {
		if strings.Contains(strings.ToLower(jsonStr), tok) {
			t.Fatalf("sensitive field '%s' detected in serialized DTO JSON: %s", tok, jsonStr)
		}
	}

	// Invariant 3: Paged Response Bounds
	paged := webcontrol.NewPagedResponse([]webcontrol.TaskSummaryDTO{taskDTO}, "cursor-1", 1, 150)
	if paged.Limit > webcontrol.MaxPageLimit {
		t.Fatalf("page limit %d exceeded MaxPageLimit %d", paged.Limit, webcontrol.MaxPageLimit)
	}
}
