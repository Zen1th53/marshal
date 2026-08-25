package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

const DefaultTaskMemoryChangePage = 50

type TaskMemoryEventType string

const (
	TaskEventSlotCreated      TaskMemoryEventType = "SLOT_CREATED"
	TaskEventSlotUpdated      TaskMemoryEventType = "SLOT_UPDATED"
	TaskEventSlotTombstoned   TaskMemoryEventType = "SLOT_TOMBSTONED"
	TaskEventCandidateCreated TaskMemoryEventType = "CANDIDATE_CREATED"
	TaskEventMemoryPromoted   TaskMemoryEventType = "MEMORY_PROMOTED"
	TaskEventMemorySuperseded TaskMemoryEventType = "MEMORY_SUPERSEDED"
	TaskEventConflictCreated  TaskMemoryEventType = "CONFLICT_CREATED"
	TaskEventConflictResolved TaskMemoryEventType = "CONFLICT_RESOLVED"
	TaskEventGrantRevoked     TaskMemoryEventType = "GRANT_REVOKED"
)

type TaskMemoryEventPriority string

const (
	TaskEventLow      TaskMemoryEventPriority = "LOW"
	TaskEventMedium   TaskMemoryEventPriority = "MEDIUM"
	TaskEventHigh     TaskMemoryEventPriority = "HIGH"
	TaskEventCritical TaskMemoryEventPriority = "CRITICAL"
)

type TaskMemoryChange struct {
	Sequence       int64                   `json:"sequence"`
	Type           TaskMemoryEventType     `json:"type"`
	Priority       TaskMemoryEventPriority `json:"priority"`
	MemoryID       string                  `json:"memory_id,omitempty"`
	CanonicalState model.MemoryLifecycle   `json:"canonical_state,omitempty"`
	Slot           *working.WorkingSlot    `json:"slot,omitempty"`
	CreatedAt      time.Time               `json:"created_at"`
}

type TaskMemoryChanges struct {
	TaskID     string             `json:"task_id"`
	After      int64              `json:"after"`
	NextCursor int64              `json:"next_cursor"`
	HasMore    bool               `json:"has_more"`
	Changes    []TaskMemoryChange `json:"changes"`
}

// RefreshTaskMemory returns a bounded page of authorized task changes. The
// durable event log is only a cursor: each referenced record is reloaded from
// canonical SQLite before content is disclosed.
func (s *MemoryService) RefreshTaskMemory(ctx context.Context, principal authz.Principal, projectID, taskID string, after int64, limit int) (TaskMemoryChanges, error) {
	response := TaskMemoryChanges{TaskID: taskID, After: after, NextCursor: after, Changes: []TaskMemoryChange{}}
	if err := ctx.Err(); err != nil {
		return response, err
	}
	if s == nil || s.store == nil {
		return response, fmt.Errorf("%w: memory store unavailable", model.ErrUnavailable)
	}
	projectID, taskID = strings.TrimSpace(projectID), strings.TrimSpace(taskID)
	if projectID == "" || taskID == "" || after < 0 {
		return response, fmt.Errorf("%w: project_id, task_id, and a non-negative cursor are required", model.ErrInvalid)
	}
	if limit == 0 {
		limit = DefaultTaskMemoryChangePage
	}
	if limit < 1 || limit > store.MaxTaskMemoryEventPage {
		return response, fmt.Errorf("%w: event page limit must be between 1 and %d", model.ErrInvalid, store.MaxTaskMemoryEventPage)
	}
	// Authorization happens before querying event counts, heads, or rows.
	if err := s.authorizeTaskScope(ctx, principal, authz.ActionMemoryRecall, taskID); err != nil {
		return response, err
	}
	events, next, hasMore, err := s.store.ListTaskMemoryEvents(ctx, projectID, taskID, after, limit)
	if err != nil {
		return response, err
	}
	response.NextCursor, response.HasMore = next, hasMore
	for _, event := range events {
		change := TaskMemoryChange{
			Sequence: event.Sequence, Type: TaskMemoryEventType(event.EventType),
			Priority: TaskMemoryEventPriority(event.Priority), MemoryID: event.MemoryID, CreatedAt: event.CreatedAt,
		}
		if event.MemoryID != "" {
			record, getErr := s.store.GetMemoryV2(ctx, projectID, event.MemoryID)
			if getErr != nil && !errors.Is(getErr, model.ErrNotFound) {
				return response, getErr
			}
			if getErr == nil {
				// Fail closed if a malformed event ever points outside the shared task.
				if record.Scope != string(model.ScopeTask) || record.ScopeID != taskID || record.ACLScope != "" {
					return response, fmt.Errorf("%w: task event canonical scope mismatch", model.ErrInvalid)
				}
				change.CanonicalState = record.Lifecycle
				if record.Lifecycle != model.MemoryTombstoned && record.Lifecycle != model.MemorySuperseded && record.Lifecycle != model.MemoryRejected && isWorkingRecord(&record, "task_slot") {
					slot := workingSlotFromRecord(record)
					change.Slot = &slot
				}
			}
		}
		response.Changes = append(response.Changes, change)
	}
	return response, nil
}
