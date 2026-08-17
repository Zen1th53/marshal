package capability

import (
	"context"
	"time"
)

// AuditEvent is a secret-free capability decision record.
type AuditEvent struct {
	ID        string
	Type      string
	GrantID   GrantID
	Subject   SubjectID
	TaskID    TaskID
	Kind      CapabilityKind
	Resource  string
	Reason    ErrorCode
	Timestamp time.Time
}

type AuditSink interface {
	AppendCapabilityEvent(context.Context, AuditEvent) error
}
