// Package dag defines the provider-neutral contract for MARSHAL's dynamic
// task dependency graph. Persistence and authorization are added by later T29
// atomic units; this package keeps the contract closed and fail-closed.
package dag

import (
	"context"
	"regexp"
	"unicode"
	"unicode/utf8"
)

const maxIdentifierBytes = 256

var taskIDPattern = regexp.MustCompile(`^TASK-[A-Za-z0-9._-]+$`)

// TaskID keeps DAG task identity distinct from free-form caller text.
type TaskID string

// RequestID binds a retryable mutation to one logical request. It is not an
// authorization token and later security boundaries must bind it to authority.
type RequestID string

// NodeKind is a closed vocabulary. Gate nodes model machine-evaluated gates;
// task nodes model executable/decomposed work.
type NodeKind string

const (
	NodeKindTask NodeKind = "task"
	NodeKindGate NodeKind = "gate"
)

func (k NodeKind) Valid() bool { return k == NodeKindTask || k == NodeKindGate }

// NodeStatus is the canonical T29 lifecycle vocabulary.
type NodeStatus string

const (
	StatusPending   NodeStatus = "pending"
	StatusReady     NodeStatus = "ready"
	StatusRunning   NodeStatus = "running"
	StatusCompleted NodeStatus = "completed"
	StatusFailed    NodeStatus = "failed"
	StatusBlocked   NodeStatus = "blocked"
	StatusSkipped   NodeStatus = "skipped"
)

func (s NodeStatus) Valid() bool {
	switch s {
	case StatusPending, StatusReady, StatusRunning, StatusCompleted, StatusFailed, StatusBlocked, StatusSkipped:
		return true
	default:
		return false
	}
}

// EdgeCondition names the predecessor machine-state required by one inbound
// dependency. Natural-language agent claims are intentionally not accepted.
type EdgeCondition string

const (
	ConditionCompleted EdgeCondition = "completed"
	ConditionFailed    EdgeCondition = "failed"
	ConditionBlocked   EdgeCondition = "blocked"
	ConditionSkipped   EdgeCondition = "skipped"
)

func (c EdgeCondition) Valid() bool {
	switch c {
	case ConditionCompleted, ConditionFailed, ConditionBlocked, ConditionSkipped:
		return true
	default:
		return false
	}
}

// Priority is deliberately fixed-width so callers cannot feed unbounded
// integer values into persistence or ordering logic.
type Priority int32

// Node is one typed DAG vertex.
type Node struct {
	TaskID   TaskID
	Kind     NodeKind
	Status   NodeStatus
	Priority Priority
}

func (n Node) Validate() error {
	if !validTaskID(n.TaskID) || !n.Kind.Valid() || !n.Status.Valid() {
		return ErrInvalidNode
	}
	return nil
}

// Edge is one directed dependency. A self edge is an immediate cycle.
type Edge struct {
	From      TaskID
	To        TaskID
	Condition EdgeCondition
}

func (e Edge) Validate() error {
	if !validTaskID(e.From) || !validTaskID(e.To) {
		return ErrNodeNotFound
	}
	if e.From == e.To {
		return ErrCycle
	}
	if !e.Condition.Valid() {
		return ErrInvalidCondition
	}
	return nil
}

type AddNodeRequest struct {
	RequestID RequestID
	Node      Node
}

func (r AddNodeRequest) Validate() error {
	if !validRequestID(r.RequestID) {
		return ErrInvalidRequest
	}
	return r.Node.Validate()
}

type AddEdgeRequest struct {
	RequestID RequestID
	Edge      Edge
}

func (r AddEdgeRequest) Validate() error {
	if !validRequestID(r.RequestID) {
		return ErrInvalidRequest
	}
	return r.Edge.Validate()
}

// Readiness is a structured result rather than a security-ambiguous bare
// boolean. BlockedBy is detached by CloneReadiness when crossing boundaries.
type Readiness struct {
	TaskID    TaskID
	Ready     bool
	BlockedBy []TaskID
	Reason    Code
}

func CloneReadiness(value Readiness) Readiness {
	clone := value
	if value.BlockedBy != nil {
		clone.BlockedBy = append([]TaskID(nil), value.BlockedBy...)
	}
	return clone
}

// Graph is the narrow T29 service contract. Later atomic units provide the
// canonical persistent implementation and authorization/event boundaries.
type Graph interface {
	AddNode(context.Context, AddNodeRequest) (Node, error)
	AddEdge(context.Context, AddEdgeRequest) (Edge, error)
	Ready(context.Context, TaskID) (Readiness, error)
	Topological(context.Context) ([]Node, error)
}

func validTaskID(id TaskID) bool {
	value := string(id)
	return len(value) <= maxIdentifierBytes && taskIDPattern.MatchString(value) && validText(value)
}

func validRequestID(id RequestID) bool {
	value := string(id)
	return value != "" && len(value) <= maxIdentifierBytes && validText(value)
}

func validText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// CanTransition reports whether a node lifecycle edge is legal under the
// canonical T29 state machine. Terminal states are immutable.
func CanTransition(from, to NodeStatus) bool {
	if !from.Valid() || !to.Valid() {
		return false
	}
	switch from {
	case StatusPending:
		return to == StatusReady
	case StatusReady:
		return to == StatusRunning
	case StatusRunning:
		switch to {
		case StatusCompleted, StatusFailed, StatusBlocked, StatusSkipped:
			return true
		}
	}
	return false
}
