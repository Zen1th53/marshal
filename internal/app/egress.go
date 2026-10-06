package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

type runEgress struct {
	broker                                     *netpolicy.CredentialBroker
	id, parent, task, worker, provider, socket string
	allowlist                                  *netpolicy.RunAllowlist
	proxy                                      *netpolicy.EgressProxy
	mu                                         sync.Mutex
	pending                                    map[string]bool
}

type EgressStatus struct {
	RunID, ParentRunID, TaskID, Worker, Provider string
	Allowed, Pending                             []string
}

type EgressAlert struct {
	RunID, ParentRunID, TaskID, Worker, Endpoint, Message, Kind, State string
}

// SetEgressAlertSink is trusted workspace composition, with no grant authority.
// Errors keep the originating request refused; decisions remain in evidence.
func (r *Runtime) SetEgressAlertSink(sink func(EgressAlert) error) {
	r.egressMu.Lock()
	r.egressAlert = sink
	r.egressMu.Unlock()
}

func providerEndpoint(provider, modelName string) (string, error) {
	switch provider {
	case "codex":
		return "api.openai.com:443", nil
	case "claude":
		return "api.anthropic.com:443", nil
	case "gemini":
		return "generativelanguage.googleapis.com:443", nil
	case "opencode":
		if modelName == "" {
			modelName = strings.TrimSpace(os.Getenv("MARSHAL_OPENCODE_MODEL"))
		}
		switch strings.SplitN(modelName, "/", 2)[0] {
		case "opencode":
			return "opencode.ai:443", nil
		case "openai":
			return "api.openai.com:443", nil
		case "anthropic":
			return "api.anthropic.com:443", nil
		case "deepseek":
			return "api.deepseek.com:443", nil
		case "google":
			return "generativelanguage.googleapis.com:443", nil
		case "ollama":
			// A name grant may not resolve to a local address. Use an exact IP
			// endpoint, and require approval for an operator's alternative.
			return "127.0.0.1:11434", nil
		}
	}
	return "", fmt.Errorf("%w: no default API endpoint for provider %s model %s", model.ErrPolicyDenied, provider, modelName)
}

func (r *Runtime) startProviderEgress(ctx context.Context, id, parent, provider, modelName string, task model.Task, worker, session string, role model.Role) (string, func(), error) {
	// Injected adapters are trusted in-process test implementations, with no
	// subprocess envelope. They cannot be admitted as network-governed workers.
	if r.adapters[provider] != nil {
		return "", func() {}, nil
	}
	endpoint, err := providerEndpoint(provider, modelName)
	if err != nil {
		return "", nil, err
	}
	if err := authorizeNetworkAccess(r.policy, worker, session, task.ID, role, task.Risk, true); err != nil {
		return "", nil, err
	}
	if !r.egressEnforcementAvailable() {
		return "", nil, netpolicy.ErrEnforcementUnavailable
	}
	broker, err := r.providerBroker(ctx, provider, modelName)
	if err != nil {
		return "", nil, err
	}
	if provider == "codex" && broker != nil && broker.Subscription() {
		endpoint = "chatgpt.com:443"
	}
	return r.startRunEgress(ctx, id, parent, provider, task.ID, worker, []string{endpoint}, broker)
}

func (r *Runtime) startRunEgress(ctx context.Context, id, parent, provider, taskID, worker string, endpoints []string, brokers ...*netpolicy.CredentialBroker) (string, func(), error) {
	if r.store == nil {
		return "", nil, model.ErrUnavailable
	}
	var broker *netpolicy.CredentialBroker
	if len(brokers) > 0 {
		broker = brokers[0]
	}
	allowlist, err := netpolicy.NewRunAllowlist(endpoints)
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "marshal-egress-")
	if err != nil {
		return "", nil, fmt.Errorf("%w: create proxy directory", netpolicy.ErrEnforcementUnavailable)
	}
	socket := filepath.Join(dir, "proxy.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("%w: listen Unix proxy: %v", netpolicy.ErrEnforcementUnavailable, err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		listener.Close()
		os.RemoveAll(dir)
		return "", nil, err
	}
	scope := &runEgress{broker: broker, id: id, parent: parent, task: taskID, worker: worker, provider: provider, socket: socket, allowlist: allowlist, pending: map[string]bool{}}
	proxy, err := netpolicy.NewEgressProxy(netpolicy.ProxyConfig{Broker: broker, CredentialAllowed: func(ctx context.Context) bool { return r.HasCredentialGrant(ctx, provider) }, Evaluator: allowlist, Store: r.store, SubjectID: worker, TaskID: taskID, RunID: id, Listener: listener, Attempt: func(ctx context.Context, host string, port int, d netpolicy.Decision) error {
		return r.recordEgressAttempt(ctx, scope, host, port, d)
	}})
	if err != nil {
		listener.Close()
		os.RemoveAll(dir)
		return "", nil, err
	}
	scope.proxy = proxy
	if err := r.recordEgress(ctx, scope, events.EventTypeNetworkEgressRequested, map[string]any{"source": "run scope", "allowed_endpoints": endpoints}); err != nil {
		proxy.Close()
		os.RemoveAll(dir)
		return "", nil, err
	}
	for _, endpoint := range endpoints {
		if err := r.recordEgress(ctx, scope, events.EventTypeNetworkEgressGranted, map[string]any{"endpoint": endpoint, "actor": "runtime", "source": "provider default"}); err != nil {
			proxy.Close()
			os.RemoveAll(dir)
			return "", nil, err
		}
	}
	r.egressMu.Lock()
	if r.egressRuns == nil {
		r.egressRuns = map[string]*runEgress{}
	}
	if r.egressRuns[id] != nil {
		r.egressMu.Unlock()
		proxy.Close()
		os.RemoveAll(dir)
		return "", nil, model.ErrConflict
	}
	r.egressRuns[id] = scope
	r.egressMu.Unlock()
	proxy.Start()
	cleanup := func() {
		r.egressMu.Lock()
		delete(r.egressRuns, id)
		r.egressMu.Unlock()
		_ = proxy.Close()
		_ = os.RemoveAll(dir)
	}
	return socket, cleanup, nil
}

func (r *Runtime) recordEgress(ctx context.Context, scope *runEgress, kind events.EventType, data map[string]any) error {
	if r.store == nil {
		return model.ErrUnavailable
	}
	id, err := model.NewID("EVENT-EGRESS-")
	if err != nil {
		return err
	}
	data["provider"] = scope.provider
	data["worker"] = scope.worker
	data["parent_run_id"] = scope.parent
	_, err = r.store.Append(ctx, events.Event{ID: id, Type: kind, Subject: scope.worker, RunID: scope.id, TaskID: scope.task, At: time.Now().UTC(), Data: data})
	return err
}

func (r *Runtime) recordEgressAttempt(ctx context.Context, scope *runEgress, host string, port int, d netpolicy.Decision) error {
	// Invalid authorities can include userinfo or terminal escapes. Record the
	// refused attempt without copying such bytes into evidence or chat.
	if _, err := netpolicy.Endpoint(net.JoinHostPort(host, strconv.Itoa(port))); err != nil {
		host = "invalid-destination"
	}

	kind := events.EventTypeNetworkEgressAttempt
	endpoint := net.JoinHostPort(host, strconv.Itoa(port))
	if err := r.recordEgress(ctx, scope, kind, map[string]any{"endpoint": endpoint, "allowed": d.Allowed, "reason": string(d.Reason), "rule_id": string(d.RuleID), "source": "proxy attempt"}); err != nil {
		return err
	}
	if d.Allowed {
		return nil
	}
	if d.Reason == netpolicy.ReasonClaudeSignInExpired {
		return r.notifyEgressRefusal(ctx, scope, endpoint, netpolicy.ClaudeSignInExpiredMessage)
	}
	return r.notifyEgressRefusal(ctx, scope, endpoint, fmt.Sprintf("%s wants to reach %s. Allow?", scope.worker, endpoint))
}

func (r *Runtime) notifyEgressRefusal(ctx context.Context, scope *runEgress, endpoint, message string) error {
	scope.mu.Lock()
	scope.pending[endpoint] = true
	scope.mu.Unlock()
	r.egressMu.Lock()
	sink := r.egressAlert
	r.egressMu.Unlock()
	// This event stream is the durable operator notification queue, available
	// without an attached TUI. Persist before attempting live delivery.
	if err := r.recordEgress(ctx, scope, events.EventTypeNetworkEgressNotification, map[string]any{"endpoint": endpoint, "message": message}); err != nil {
		return err
	}
	if sink != nil {
		return sink(EgressAlert{RunID: scope.id, ParentRunID: scope.parent, TaskID: scope.task, Worker: scope.worker, Endpoint: endpoint, Message: message, Kind: "egress refused", State: "waiting"})
	}

	return nil
}

func (r *Runtime) EgressStatus() []EgressStatus {
	r.egressMu.Lock()
	defer r.egressMu.Unlock()
	var rows []EgressStatus
	for _, scope := range r.egressRuns {
		row := EgressStatus{RunID: scope.id, ParentRunID: scope.parent, TaskID: scope.task, Worker: scope.worker, Provider: scope.provider, Allowed: scope.allowlist.Endpoints()}
		scope.mu.Lock()
		for endpoint := range scope.pending {
			row.Pending = append(row.Pending, endpoint)
		}
		scope.mu.Unlock()
		sort.Strings(row.Allowed)
		sort.Strings(row.Pending)
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].RunID < rows[j].RunID })
	return rows
}

// CommandEgress accepts authority only from the authenticated local operator
// context. No model, chat, native process, MCP or A2A text creates that context.
func (r *Runtime) CommandEgress(ctx context.Context, runID, operation, endpoint string) error {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != r.ProjectIdentity() {
		return authz.ErrDenied
	}
	if operation != "allow" && operation != "revoke" {
		return model.ErrInvalid
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(p.ProjectID()), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: "egress.decide"}
	if _, err := authz.CanWithCapability(ctx, principal, authz.AuthorityTaskPlan, r.layout.Database, query, capability.NewEngine(r.store, nil)); err != nil {
		return err
	}
	r.egressMu.Lock()
	defer r.egressMu.Unlock()
	scope := r.egressRuns[runID]
	if scope == nil {
		return fmt.Errorf("%w: no active egress run %s", model.ErrNotFound, runID)
	}
	allow := operation == "allow"
	kind := events.EventTypeNetworkEgressRevoked
	if allow {
		kind = events.EventTypeNetworkEgressGranted
	}
	err := scope.allowlist.Set(endpoint, allow, func(normalized string) error {
		if err := r.recordEgress(ctx, scope, kind, map[string]any{"endpoint": normalized, "actor": p.ID(), "source": "operator command"}); err != nil {
			return err
		}
		// Closing happens after Set returns, outside the allowlist lock.
		return nil
	})
	if err != nil {
		return err
	}
	normalized, _ := netpolicy.Endpoint(endpoint)
	if !allow && scope.proxy != nil {
		scope.proxy.CloseEndpoint(normalized)
	}
	scope.mu.Lock()
	delete(scope.pending, normalized)
	scope.mu.Unlock()
	return nil
}

func (r *Runtime) socketObserver(socket string) func(context.Context, sandbox.Refusal) error {
	return func(ctx context.Context, refusal sandbox.Refusal) error {
		r.egressMu.Lock()
		var scope *runEgress
		for _, candidate := range r.egressRuns {
			if candidate.socket == socket {
				scope = candidate
				break
			}
		}
		r.egressMu.Unlock()
		if scope == nil {
			return model.ErrUnavailable
		}
		if err := r.recordEgress(ctx, scope, events.EventTypeNetworkEgressAttempt, map[string]any{"source": "socket supervisor", "operation": refusal.Operation, "endpoint": net.JoinHostPort(refusal.Host, strconv.Itoa(refusal.Port)), "allowed": false}); err != nil {
			return err
		}
		endpoint := net.JoinHostPort(refusal.Host, strconv.Itoa(refusal.Port))
		return r.notifyEgressRefusal(ctx, scope, endpoint, fmt.Sprintf("%s direct socket attempt refused: %s (%s). Use the run proxy; grants apply only through the proxy.", scope.worker, endpoint, refusal.Operation))
	}
}

// EgressRequestPending is true only while the live run still awaits this exact
// endpoint. Durable refusal notifications are evidence, not grant requests.
func (r *Runtime) EgressRequestPending(runID, endpoint string) bool {
	normalized, err := netpolicy.Endpoint(endpoint)
	if err != nil {
		return false
	}
	r.egressMu.Lock()
	defer r.egressMu.Unlock()
	scope := r.egressRuns[runID]
	if scope == nil {
		return false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return scope.pending[normalized]
}

// EgressNotifications is the durable operator inbox, including completed runs.
// Attaching a live sink does not consume this queue or confer grant authority.
func (r *Runtime) EgressNotifications(ctx context.Context) ([]EgressAlert, error) {
	if r.store == nil {
		return nil, model.ErrUnavailable
	}
	history, err := r.store.Since(ctx, 0)
	if err != nil {
		return nil, err
	}
	var alerts []EgressAlert
	for _, event := range history {
		if event.Type == events.EventTypeNetworkEgressNotification {
			endpoint, _ := event.Data["endpoint"].(string)
			worker, _ := event.Data["worker"].(string)
			message, _ := event.Data["message"].(string)
			parent, _ := event.Data["parent_run_id"].(string)
			state := "expired"
			if r.EgressRequestPending(event.RunID, endpoint) {
				state = "waiting"
			}
			alerts = append(alerts, EgressAlert{RunID: event.RunID, ParentRunID: parent, TaskID: event.TaskID, Worker: worker, Endpoint: endpoint, Message: message, Kind: "egress refused", State: state})
		}
	}
	return alerts, nil
}
