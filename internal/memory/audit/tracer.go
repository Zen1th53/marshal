package audit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrAuditAccessDenied = errors.New("audit trace query permission denied")
)

type MutationEvent struct {
	MemoryID    string    `json:"memory_id"`
	Revision    int       `json:"revision"`
	Action      string    `json:"action"`
	PrincipalID string    `json:"principal_id"`
	ProjectID   string    `json:"project_id"`
	Timestamp   time.Time `json:"timestamp"`
}

type InjectionEvent struct {
	ContextID         string    `json:"context_id"`
	TaskID            string    `json:"task_id"`
	AgentID           string    `json:"agent_id"`
	InjectedMemoryIDs []string  `json:"injected_memory_ids"`
	Timestamp         time.Time `json:"timestamp"`
}

type Tracer struct {
	mu         sync.RWMutex
	mutations  []MutationEvent
	injections []InjectionEvent
}

func NewTracer() *Tracer {
	return &Tracer{}
}

// RecordMutation logs a memory lifecycle mutation.
func (t *Tracer) RecordMutation(ctx context.Context, ev MutationEvent) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	t.mutations = append(t.mutations, ev)
	return nil
}

// RecordInjection logs which memory records were compiled and injected into an agent prompt.
func (t *Tracer) RecordInjection(ctx context.Context, ev InjectionEvent) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	t.injections = append(t.injections, ev)
	return nil
}

// TraceTask reconstructs all memory injection events for a given task ID.
func (t *Tracer) TraceTask(ctx context.Context, taskID, callerPrincipalID string) ([]InjectionEvent, error) {
	if !strings.Contains(callerPrincipalID, "admin") && !strings.Contains(callerPrincipalID, "operator") {
		return nil, ErrAuditAccessDenied
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	var matched []InjectionEvent
	for _, ev := range t.injections {
		if ev.TaskID == taskID {
			matched = append(matched, ev)
		}
	}
	return matched, nil
}

// TraceMemory reconstructs all mutations and usage injections for a memory record.
func (t *Tracer) TraceMemory(ctx context.Context, memoryID, callerPrincipalID string) ([]MutationEvent, error) {
	if !strings.Contains(callerPrincipalID, "admin") && !strings.Contains(callerPrincipalID, "operator") {
		return nil, ErrAuditAccessDenied
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	var matched []MutationEvent
	for _, ev := range t.mutations {
		if ev.MemoryID == memoryID {
			matched = append(matched, ev)
		}
	}
	return matched, nil
}
