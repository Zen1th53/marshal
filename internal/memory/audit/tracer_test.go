package audit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/audit"
)

func TestT127MemoryAuditAndUsageTrace(t *testing.T) {
	tracer := audit.NewTracer()
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Record mutation event
	err := tracer.RecordMutation(ctx, audit.MutationEvent{
		MemoryID:    "MEM-DEC-100",
		Revision:    1,
		Action:      "memory.remember",
		PrincipalID: "agent-007",
		ProjectID:   "PROJ-1",
		Timestamp:   now,
	})
	if err != nil {
		t.Fatalf("RecordMutation: %v", err)
	}

	// 2. Record injection event
	err = tracer.RecordInjection(ctx, audit.InjectionEvent{
		ContextID:         "CTX-999",
		TaskID:            "TASK-101",
		AgentID:           "agent-007",
		InjectedMemoryIDs: []string{"MEM-DEC-100"},
		Timestamp:         now,
	})
	if err != nil {
		t.Fatalf("RecordInjection: %v", err)
	}

	// 3. Trace task: Reconstructs exact memory inputs for TASK-101
	events, err := tracer.TraceTask(ctx, "TASK-101", "operator-admin")
	if err != nil {
		t.Fatalf("TraceTask: %v", err)
	}
	if len(events) != 1 || len(events[0].InjectedMemoryIDs) != 1 || events[0].InjectedMemoryIDs[0] != "MEM-DEC-100" {
		t.Fatalf("expected injected MEM-DEC-100 in task trace, got: %+v", events)
	}

	// 4. Unauthorized audit query must be denied
	_, err = tracer.TraceTask(ctx, "TASK-101", "unauthorized-viewer")
	if !errors.Is(err, audit.ErrAuditAccessDenied) {
		t.Fatalf("expected ErrAuditAccessDenied for unauthorized viewer, got: %v", err)
	}
}
