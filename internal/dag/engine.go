package dag

import (
	"context"
	"errors"
	"sort"
	"sync/atomic"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

// Backend is the narrow persistence contract consumed by the T29 service.
// Store implementations remain authoritative; the engine keeps no graph state
// in process memory.
type Backend interface {
	PutDAGNode(context.Context, Node) (Node, error)
	GetDAGNode(context.Context, TaskID) (Node, error)
	PutDAGEdge(context.Context, Edge) (Edge, error)
	DAGEdgesFrom(context.Context, TaskID) ([]Edge, error)
	DAGEdgesTo(context.Context, TaskID) ([]Edge, error)
	DAGNodes(context.Context) ([]Node, error)
	TransitionDAGNode(context.Context, TaskID, NodeStatus, NodeStatus) (Node, error)
}

// Engine implements canonical DAG service logic over durable state. Mutation
// methods fail closed unless an authenticated identity source and authorizer
// are explicitly injected. Read-only graph queries remain available.
type Engine struct {
	backend    Backend
	identity   IdentityProvider
	authorizer Authorizer
	freshness  FreshnessValidator
	now        func() time.Time
	eventStore events.Store
	metrics    dagMetrics
}

type dagMetrics struct {
	success atomic.Uint64
	denied  atomic.Uint64
	invalid atomic.Uint64
	totalNs atomic.Uint64
}

// MetricsSnapshot is a bounded operational projection; it is never used for
// readiness, authorization, or graph correctness.
type MetricsSnapshot struct {
	Success uint64
	Denied  uint64
	Invalid uint64
	TotalNs uint64
}

func (e *Engine) Metrics() MetricsSnapshot {
	return MetricsSnapshot{Success: e.metrics.success.Load(), Denied: e.metrics.denied.Load(), Invalid: e.metrics.invalid.Load(), TotalNs: e.metrics.totalNs.Load()}
}

// NewEngineWithEvents enables the canonical durable event hook. Events are
// appended only after the corresponding backend mutation is durable.
func NewEngineWithEvents(backend Backend, eventStore events.Store) (*Engine, error) {
	engine, err := NewEngine(backend)
	if err != nil {
		return nil, err
	}
	if eventStore == nil {
		return nil, ErrInvalidRequest
	}
	engine.eventStore = eventStore
	return engine, nil
}

func NewEngine(backend Backend) (*Engine, error) {
	if backend == nil {
		return nil, ErrInvalidRequest
	}
	return &Engine{backend: backend, now: func() time.Time { return time.Now().UTC() }}, nil
}

func NewAuthorizedEngine(backend Backend, identity IdentityProvider, authorizer Authorizer, freshness FreshnessValidator) (*Engine, error) {
	engine, err := NewEngine(backend)
	if err != nil {
		return nil, err
	}
	if identity == nil || authorizer == nil || freshness == nil {
		return nil, ErrAuthorizationUnavailable
	}
	engine.identity = identity
	engine.authorizer = authorizer
	engine.freshness = freshness
	return engine, nil
}

// NewAuthorizedEngineWithEvents combines the A04 authority boundary with the
// canonical A05 durable event hook.
func NewAuthorizedEngineWithEvents(backend Backend, identity IdentityProvider, authorizer Authorizer, freshness FreshnessValidator, eventStore events.Store) (*Engine, error) {
	engine, err := NewAuthorizedEngine(backend, identity, authorizer, freshness)
	if err != nil {
		return nil, err
	}
	if eventStore == nil {
		return nil, ErrInvalidRequest
	}
	engine.eventStore = eventStore
	return engine, nil
}

func (e *Engine) AddNode(ctx context.Context, request AddNodeRequest) (Node, error) {
	started := time.Now()
	defer func() { e.metrics.totalNs.Add(uint64(time.Since(started).Nanoseconds())) }()
	if err := request.Validate(); err != nil {
		e.metrics.invalid.Add(1)
		return Node{}, err
	}
	if err := e.authorize(ctx, AuthorizationRequest{RequestID: request.RequestID, Action: ActionAddNode, Resource: nodeResource(request.Node.TaskID)}); err != nil {
		e.metrics.denied.Add(1)
		return Node{}, err
	}
	// New graph history always starts pending. Later lifecycle movement must use
	// the explicit transition state machine rather than insertion as a setter.
	if request.Node.Status != StatusPending {
		return Node{}, ErrInvalidNode
	}
	node, err := e.backend.PutDAGNode(ctx, request.Node)
	if err != nil {
		return Node{}, err
	}
	if err := e.record(ctx, events.EventTypeDAGNodeAdded, string(node.TaskID), request.RequestID, map[string]any{
		"task_id": string(node.TaskID), "kind": string(node.Kind), "status": string(node.Status),
	}); err != nil {
		return node, err
	}
	e.metrics.success.Add(1)
	return node, nil
}

func (e *Engine) AddEdge(ctx context.Context, request AddEdgeRequest) (Edge, error) {
	started := time.Now()
	defer func() { e.metrics.totalNs.Add(uint64(time.Since(started).Nanoseconds())) }()
	if err := request.Validate(); err != nil {
		e.metrics.invalid.Add(1)
		return Edge{}, err
	}
	if err := e.authorize(ctx, AuthorizationRequest{RequestID: request.RequestID, Action: ActionAddEdge, Resource: edgeResource(request.Edge)}); err != nil {
		e.metrics.denied.Add(1)
		return Edge{}, err
	}
	// Reconcile an exact semantic retry before applying new-edge constraints.
	existing, err := e.backend.DAGEdgesFrom(ctx, request.Edge.From)
	if err != nil {
		return Edge{}, err
	}
	for _, edge := range existing {
		if edge.To == request.Edge.To {
			if edge.Condition == request.Edge.Condition {
				return edge, nil
			}
			return Edge{}, ErrDuplicateEdge
		}
	}

	target, err := e.backend.GetDAGNode(ctx, request.Edge.To)
	if err != nil {
		return Edge{}, err
	}
	if target.Status != StatusPending {
		return Edge{}, ErrInvalidNode
	}
	if cycle, err := e.wouldCycle(ctx, request.Edge); err != nil {
		return Edge{}, err
	} else if cycle {
		return Edge{}, ErrCycle
	}
	edge, err := e.backend.PutDAGEdge(ctx, request.Edge)
	if errors.Is(err, ErrDuplicateEdge) {
		// A concurrent/exact insertion can race the pre-read. Reconcile only if
		// the canonical edge is semantically identical.
		after, readErr := e.backend.DAGEdgesFrom(ctx, request.Edge.From)
		if readErr != nil {
			return Edge{}, readErr
		}
		for _, existing := range after {
			if existing.To == request.Edge.To && existing.Condition == request.Edge.Condition {
				return existing, nil
			}
		}
	}
	if err != nil {
		return Edge{}, err
	}
	if err := e.record(ctx, events.EventTypeDAGEdgeAdded, string(edge.To), request.RequestID, map[string]any{
		"from_task": string(edge.From), "to_task": string(edge.To), "condition": string(edge.Condition),
	}); err != nil {
		return edge, err
	}
	e.metrics.success.Add(1)
	return edge, nil
}

func (e *Engine) Ready(ctx context.Context, id TaskID) (Readiness, error) {
	node, err := e.backend.GetDAGNode(ctx, id)
	if err != nil {
		return Readiness{}, err
	}
	if node.Status != StatusPending && node.Status != StatusReady {
		return Readiness{TaskID: id, Ready: false}, nil
	}
	inbound, err := e.backend.DAGEdgesTo(ctx, id)
	if err != nil {
		return Readiness{}, err
	}
	blocked := make([]TaskID, 0)
	for _, edge := range inbound {
		parent, err := e.backend.GetDAGNode(ctx, edge.From)
		if err != nil {
			return Readiness{}, err
		}
		if !conditionSatisfied(parent.Status, edge.Condition) {
			blocked = append(blocked, edge.From)
		}
	}
	sort.Slice(blocked, func(i, j int) bool { return blocked[i] < blocked[j] })
	return Readiness{TaskID: id, Ready: len(blocked) == 0, BlockedBy: blocked}, nil
}

// Transition applies one explicit lifecycle edge. Moving to ready is allowed
// only when canonical predecessor state currently satisfies every dependency.
func (e *Engine) Transition(ctx context.Context, id TaskID, expected, target NodeStatus) (Node, error) {
	started := time.Now()
	defer func() { e.metrics.totalNs.Add(uint64(time.Since(started).Nanoseconds())) }()
	if !CanTransition(expected, target) {
		e.metrics.invalid.Add(1)
		return Node{}, ErrInvalidNode
	}
	if err := e.authorize(ctx, AuthorizationRequest{Action: ActionTransition, Resource: nodeResource(id), ExpectedState: expected, TargetState: target}); err != nil {
		e.metrics.denied.Add(1)
		return Node{}, err
	}
	if target == StatusReady {
		readiness, err := e.Ready(ctx, id)
		if err != nil {
			return Node{}, err
		}
		if !readiness.Ready {
			return Node{}, ErrInvalidNode
		}
	}
	node, err := e.backend.TransitionDAGNode(ctx, id, expected, target)
	if err != nil {
		return Node{}, err
	}
	eventType := events.EventTypeDAGNodeBlocked
	if target == StatusReady {
		eventType = events.EventTypeDAGNodeReady
	}
	if err := e.record(ctx, eventType, string(id), RequestID(string(id)+":"+string(expected)+":"+string(target)), map[string]any{
		"task_id": string(id), "previous_state": string(expected), "target_state": string(target),
	}); err != nil {
		return node, err
	}
	e.metrics.success.Add(1)
	return node, nil
}

func (e *Engine) record(ctx context.Context, eventType events.EventType, resource string, requestID RequestID, data map[string]any) error {
	if e.eventStore == nil {
		return nil
	}
	at := e.now().UTC()
	event := events.Event{ID: "dag:" + string(eventType) + ":" + resource + ":" + string(requestID), Type: eventType,
		TaskID: resource, ResourceID: resource, At: at, Data: data, IdempotencyKey: "dag:" + string(eventType) + ":" + resource + ":" + string(requestID)}
	_, err := e.eventStore.Append(ctx, event)
	return err
}

func (e *Engine) authorize(ctx context.Context, request AuthorizationRequest) error {
	if err := ctx.Err(); err != nil {
		return NewError(CodeAuthorizationUnavailable, err)
	}
	if e.identity == nil || e.authorizer == nil || e.freshness == nil {
		return ErrAuthorizationUnavailable
	}
	identity, err := e.identity.Identity(ctx)
	if err != nil {
		return NewError(CodeAuthorizationUnavailable, err)
	}
	request.Identity = identity
	if !request.valid() {
		return ErrAuthorizationDenied
	}
	decision, err := e.authorizer.Authorize(ctx, request)
	if err != nil {
		return NewError(CodeAuthorizationUnavailable, err)
	}
	if err := decision.validateFor(request, e.now()); err != nil {
		return err
	}
	if err := e.freshness.ValidateFreshness(ctx, request, decision); err != nil {
		return NewError(CodeAuthorizationStale, err)
	}
	if err := ctx.Err(); err != nil {
		return NewError(CodeAuthorizationUnavailable, err)
	}
	return nil
}

func (e *Engine) Topological(ctx context.Context) ([]Node, error) {
	nodes, err := e.backend.DAGNodes(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[TaskID]Node, len(nodes))
	indegree := make(map[TaskID]int, len(nodes))
	children := make(map[TaskID][]TaskID, len(nodes))
	for _, node := range nodes {
		byID[node.TaskID] = node
		indegree[node.TaskID] = 0
	}
	for _, node := range nodes {
		edges, err := e.backend.DAGEdgesFrom(ctx, node.TaskID)
		if err != nil {
			return nil, err
		}
		for _, edge := range edges {
			if _, ok := byID[edge.To]; !ok {
				return nil, ErrNodeNotFound
			}
			indegree[edge.To]++
			children[edge.From] = append(children[edge.From], edge.To)
		}
	}
	available := make([]Node, 0)
	for _, node := range nodes {
		if indegree[node.TaskID] == 0 {
			available = append(available, node)
		}
	}
	orderAvailable(available)
	result := make([]Node, 0, len(nodes))
	for len(available) > 0 {
		node := available[0]
		available = available[1:]
		result = append(result, node)
		for _, child := range children[node.TaskID] {
			indegree[child]--
			if indegree[child] == 0 {
				available = append(available, byID[child])
				orderAvailable(available)
			}
		}
	}
	if len(result) != len(nodes) {
		return nil, ErrCycle
	}
	return result, nil
}

func (e *Engine) wouldCycle(ctx context.Context, edge Edge) (bool, error) {
	seen := map[TaskID]bool{}
	stack := []TaskID{edge.To}
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		if current == edge.From {
			return true, nil
		}
		if seen[current] {
			continue
		}
		seen[current] = true
		edges, err := e.backend.DAGEdgesFrom(ctx, current)
		if err != nil {
			return false, err
		}
		for _, next := range edges {
			stack = append(stack, next.To)
		}
	}
	return false, nil
}

func conditionSatisfied(status NodeStatus, condition EdgeCondition) bool {
	switch condition {
	case ConditionCompleted:
		return status == StatusCompleted
	case ConditionFailed:
		return status == StatusFailed
	case ConditionBlocked:
		return status == StatusBlocked
	case ConditionSkipped:
		return status == StatusSkipped
	default:
		return false
	}
}

func orderAvailable(nodes []Node) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Priority != nodes[j].Priority {
			return nodes[i].Priority > nodes[j].Priority
		}
		return nodes[i].TaskID < nodes[j].TaskID
	})
}

var _ Graph = (*Engine)(nil)
