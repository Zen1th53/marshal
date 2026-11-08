package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

// A bounded credential decision authorizes one parent-owned verifier or task
// attempt. It is deliberately unrelated to the standing project credential
// grant and cannot be reconstructed from the event journal after owner restart.
type boundCredentialScope struct {
	Project, StorageProject, Provider, Model, Purpose string
	Probe, Run, Task, CanonicalTask, Plan, Approval   string
	PlanVersion                                       int64
	Attempt                                           int
}

type boundCredentialLease struct {
	id                                 string
	scope                              boundCredentialScope
	request                            permission.Request
	created, expires                   time.Time
	deciding, decided, allowed, closed bool
	decisionID                         string
	egressID, socket                   string
	// decisionVerified records that decisionID was found as the exact
	// recorded allow; that event is immutable, so it is read only once.
	decisionVerified bool
}

type boundCredentialState struct {
	mu      sync.Mutex
	byID    map[string]*boundCredentialLease
	byScope map[string]*boundCredentialLease
}

type boundCredentialContextKey struct{}

func withBoundCredentialScope(ctx context.Context, r *Runtime, scope boundCredentialScope) context.Context {
	return context.WithValue(ctx, boundCredentialContextKey{}, struct {
		runtime *Runtime
		scope   boundCredentialScope
	}{r, scope})
}

func (r *Runtime) boundScopeFromContext(ctx context.Context, provider, modelName string) (boundCredentialScope, bool) {
	v, ok := ctx.Value(boundCredentialContextKey{}).(struct {
		runtime *Runtime
		scope   boundCredentialScope
	})
	if !ok || v.runtime != r || v.scope.Provider != provider || v.scope.Model != modelName || !r.validBoundCredentialScope(v.scope) {
		return boundCredentialScope{}, false
	}
	return v.scope, true
}

func (r *Runtime) validBoundCredentialScope(scope boundCredentialScope) bool {
	if r == nil || scope.Project == "" || scope.Project != string(r.admittedProjectIdentity) || scope.StorageProject != r.ProjectID() || !credentialProvider(scope.Provider) || scope.Model == "" || scope.Model != strings.TrimSpace(scope.Model) {
		return false
	}
	for _, value := range []string{scope.Project, scope.StorageProject, scope.Provider, scope.Model, scope.Probe, scope.Run, scope.Task, scope.CanonicalTask, scope.Plan, scope.Approval} {
		if len(value) > 256 || strings.ContainsFunc(value, func(c rune) bool { return unicode.IsControl(c) || unicode.Is(unicode.Cf, c) }) {
			return false
		}
	}
	if scope.Purpose == "probe" {
		return strings.HasPrefix(scope.Probe, "PROBE-") && scope.Run == "" && scope.Task == "" && scope.CanonicalTask == "" && scope.Plan == "" && scope.Attempt == 0
	}
	return scope.Purpose == "task" && scope.Probe == "" && scope.Run != "" && scope.Task != "" && scope.CanonicalTask != "" && scope.Plan != "" && scope.PlanVersion > 0 && scope.Attempt > 0
}

func (r *Runtime) boundCredentialLease(ctx context.Context, scope boundCredentialScope) (*boundCredentialLease, bool, error) {
	if !r.validBoundCredentialScope(scope) {
		return nil, false, errors.New("invalid bounded credential scope")
	}
	key, err := boundCredentialScopeKey(scope)
	if err != nil {
		return nil, false, err
	}
	r.boundCredentials.mu.Lock()
	defer r.boundCredentials.mu.Unlock()
	if old := r.boundCredentials.byScope[key]; old != nil {
		// A pause may outlive the 300-second credential window. A later
		// explicit invocation must obtain a new Decision, never revive the old
		// one. Denials and already-consumed task attempts remain terminal.
		// Extreme does not keep a denied lease terminal: the next invocation
		// gets a fresh lease that the runtime grants automatically.
		denied := old.decided && !old.allowed
		if old.egressID != "" || denied && !automaticCredentialDecision || !denied && time.Now().Before(old.expires) && !old.closed {
			return old, false, nil
		}
		delete(r.boundCredentials.byScope, key)
	}
	id, err := model.NewID("CRED-BOUND-")
	if err != nil {
		return nil, false, err
	}
	duration := boundTaskCredentialWindow
	if scope.Purpose == "probe" {
		duration = 90 * time.Second
	}
	created := time.Now().UTC()
	expires := created.Add(duration)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(expires) {
		expires = deadline.UTC()
	}
	if !expires.After(time.Now()) {
		return nil, false, context.DeadlineExceeded
	}
	textScope := fmt.Sprintf("one %s invocation for %s %s, expires %s UTC", scope.Purpose, scope.Provider, scope.Model, expires.Format(time.RFC3339))
	if scope.Purpose == "probe" {
		textScope += "; probe " + scope.Probe
	} else {
		textScope += fmt.Sprintf("; run %s; task %s (canonical %s); attempt %d; plan %s v%d", scope.Run, scope.Task, scope.CanonicalTask, scope.Attempt, scope.Plan, scope.PlanVersion)
	}
	reason := fmt.Sprintf("Use %s sign-in through the credential broker for this exact %s only", scope.Provider, scope.Purpose)
	req := permission.Request{Kind: "credential-bound", Object: scope.Provider, Scope: textScope, Who: "MARSHAL", Reason: reason, RunID: scope.Run, TaskID: scope.Task, ApprovalBinding: id}
	if scope.Purpose == "probe" {
		req.RunID = scope.Probe
	}
	lease := &boundCredentialLease{id: id, scope: scope, request: req, created: created, expires: expires}
	if r.boundCredentials.byID == nil {
		r.boundCredentials.byID = map[string]*boundCredentialLease{}
		r.boundCredentials.byScope = map[string]*boundCredentialLease{}
	}
	r.boundCredentials.byID[id] = lease
	r.boundCredentials.byScope[key] = lease
	return lease, true, nil
}

func boundCredentialScopeKey(scope boundCredentialScope) (string, error) {
	encoded, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func (r *Runtime) existingBoundCredential(scope boundCredentialScope) *boundCredentialLease {
	key, err := boundCredentialScopeKey(scope)
	if err != nil {
		return nil
	}
	r.boundCredentials.mu.Lock()
	defer r.boundCredentials.mu.Unlock()
	return r.boundCredentials.byScope[key]
}

func (r *Runtime) boundCredentialForContext(ctx context.Context, provider string) (*boundCredentialLease, bool) {
	v, ok := ctx.Value(boundCredentialContextKey{}).(struct {
		runtime *Runtime
		scope   boundCredentialScope
	})
	if !ok {
		return nil, false
	}
	scope, valid := r.boundScopeFromContext(ctx, provider, v.scope.Model)
	if !valid {
		return nil, true
	}
	return r.existingBoundCredential(scope), true
}

// The local control boundary may only decide a request actually minted by this
// Runtime. A copied or synthetic request cannot grant a credential capability.
func (r *Runtime) checkBoundCredentialDecision(ctx context.Context, req permission.Request, allow bool) (*boundCredentialLease, error) {
	r.boundCredentials.mu.Lock()
	defer r.boundCredentials.mu.Unlock()
	lease := r.boundCredentials.byID[req.ApprovalBinding]
	if lease == nil || lease.closed || lease.deciding || lease.decided || !time.Now().Before(lease.expires) {
		return nil, errors.New("bound credential request is absent, changed, answered or expired")
	}
	expected := lease.request
	expected.SourceLaunch, expected.SourceOccurrence = req.SourceLaunch, req.SourceOccurrence
	if req != expected || r.checkBoundCredentialSource(ctx, req, allow) != nil {
		return nil, errors.New("bound credential request is absent, changed, answered or expired")
	}
	lease.deciding = true
	return lease, nil
}

func (r *Runtime) abortBoundCredentialDecision(lease *boundCredentialLease) {
	r.boundCredentials.mu.Lock()
	defer r.boundCredentials.mu.Unlock()
	if r.boundCredentials.byID[lease.id] == lease {
		lease.deciding = false
	}
}

func (r *Runtime) finishBoundCredentialDecision(lease *boundCredentialLease, allow bool, decisionID string) {
	r.boundCredentials.mu.Lock()
	defer r.boundCredentials.mu.Unlock()
	if r.boundCredentials.byID[lease.id] == lease {
		lease.deciding, lease.decided, lease.allowed, lease.decisionID = false, true, allow, decisionID
	}
}

func (r *Runtime) boundCredentialApproved(ctx context.Context, lease *boundCredentialLease, egressID, socket string) bool {
	if r == nil || lease == nil {
		return false
	}
	r.boundCredentials.mu.Lock()
	live := r.boundCredentials.byID[lease.id] == lease && lease.decided && lease.allowed && !lease.closed && time.Now().Before(lease.expires) && (lease.egressID == "" || lease.egressID == egressID && lease.socket == socket)
	decisionID := lease.decisionID
	created := lease.created
	scope := lease.scope
	expires := lease.expires
	verified := lease.decisionVerified
	r.boundCredentials.mu.Unlock()
	if !live || decisionID == "" {
		return false
	}
	// The egress watcher asks every 100 ms per open egress, and the store has
	// a single connection: read the full history only until the exact allow is
	// found, then check revocations through the narrow decision query.
	if !verified {
		events, err := r.store.ListEvents(ctx)
		if err != nil {
			return false
		}
		scopeJSON, _ := json.Marshal(scope)
		for _, event := range events {
			if event.Type == "PERMISSION_DECIDED" && event.ProjectID == scope.StorageProject && event.ID == decisionID {
				verified = event.Data["kind"] == "credential-bound" && event.Data["object"] == scope.Provider && event.Data["allow"] == true && event.Data["approval_binding"] == lease.id && event.Data["bound_scope"] == string(scopeJSON) && event.Data["bound_expires"] == expires.Format(time.RFC3339Nano)
			}
		}
		if !verified {
			return false
		}
		r.boundCredentials.mu.Lock()
		lease.decisionVerified = true
		r.boundCredentials.mu.Unlock()
	}
	decisions, err := r.store.CredentialDecisionEvents(ctx, scope.StorageProject, scope.Provider, created)
	if err != nil {
		return false
	}
	for _, event := range decisions {
		// An explicit broad revoke after this lease was created wins even if
		// another standing grant is recorded later.
		if source, _ := event.Data["source"].(string); event.Data["allow"] == false && bindingCredentialDenial(source) {
			return false
		}
	}
	return true
}

func (r *Runtime) waitBoundCredential(ctx context.Context, scope boundCredentialScope) (*boundCredentialLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lease, fresh, err := r.boundCredentialLease(ctx, scope)
	if err != nil {
		return nil, err
	}
	if fresh && automaticCredentialDecision {
		// No sink, popup or Marshal turn: the runtime applies this exact
		// minted lease itself and records the bound evidence.
		if err := r.applyAutomaticBoundCredential(ctx, lease); err != nil {
			r.boundCredentials.mu.Lock()
			lease.closed = true
			r.boundCredentials.mu.Unlock()
			return nil, err
		}
	} else if fresh {
		r.permissionMu.Lock()
		sink := r.permissionSink
		r.permissionMu.Unlock()
		if sink == nil {
			r.boundCredentials.mu.Lock()
			lease.closed = true
			r.boundCredentials.mu.Unlock()
			return nil, errors.New("no credential decision surface")
		}
		if err := ctx.Err(); err != nil {
			r.boundCredentials.mu.Lock()
			lease.closed = true
			r.boundCredentials.mu.Unlock()
			return nil, err
		}
		sink(lease.request)
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		r.boundCredentials.mu.Lock()
		decided, allowed := lease.decided, lease.allowed
		r.boundCredentials.mu.Unlock()
		if decided {
			if allowed && r.boundCredentialApproved(ctx, lease, "", "") {
				return lease, nil
			}
			return nil, errors.New("bounded credential use denied or expired")
		}
		select {
		case <-ctx.Done():
			r.boundCredentials.mu.Lock()
			if !lease.decided && lease.egressID == "" {
				lease.closed = true
			}
			r.boundCredentials.mu.Unlock()
			return nil, ctx.Err()
		case <-timer.C:
			r.boundCredentials.mu.Lock()
			if !lease.decided && lease.egressID == "" {
				lease.closed = true
			}
			r.boundCredentials.mu.Unlock()
			return nil, errors.New("bounded credential decision timed out")
		case <-ticker.C:
		}
	}
}

func (r *Runtime) applyAutomaticBoundCredential(ctx context.Context, lease *boundCredentialLease) error {
	// The operator's explicit revoke command is the one standing denial the
	// runtime keeps honoring; it is never produced by a popup keypress.
	if revoked, err := r.explicitlyRevokedCredential(ctx, lease.scope.Provider); err != nil {
		return err
	} else if revoked {
		return fmt.Errorf("%s credential explicitly revoked by the operator; /permission credential request %s restores it", lease.scope.Provider, lease.scope.Provider)
	}
	if _, err := r.checkBoundCredentialDecision(ctx, lease.request, true); err != nil {
		return err
	}
	id, err := r.recordAutomaticCredentialDecision(ctx, lease.request, lease)
	if err != nil {
		r.abortBoundCredentialDecision(lease)
		return err
	}
	r.finishBoundCredentialDecision(lease, true, id)
	return nil
}

func (r *Runtime) bindBoundCredentialEgress(ctx context.Context, lease *boundCredentialLease, egressID, socket string) error {
	if lease == nil || egressID == "" || socket == "" || !r.boundCredentialApproved(ctx, lease, "", "") {
		return errors.New("bounded credential grant unavailable")
	}
	r.boundCredentials.mu.Lock()
	defer r.boundCredentials.mu.Unlock()
	if lease.closed || lease.egressID != "" || !time.Now().Before(lease.expires) {
		return errors.New("bounded credential invocation already consumed or expired")
	}
	lease.egressID, lease.socket = egressID, socket
	return nil
}

// A host invocation consumes the same one-use lease without publishing a proxy.
func (r *Runtime) bindBoundCredentialHost(ctx context.Context, lease *boundCredentialLease, id string) error {
	if lease == nil || id == "" || !r.boundCredentialApproved(ctx, lease, "", "") {
		return errors.New("bounded credential grant unavailable")
	}
	r.boundCredentials.mu.Lock()
	defer r.boundCredentials.mu.Unlock()
	if lease.closed || lease.egressID != "" || !time.Now().Before(lease.expires) {
		return errors.New("bounded credential invocation already consumed or expired")
	}
	lease.egressID = id
	return nil
}

func (r *Runtime) closeBoundCredentialEgress(lease *boundCredentialLease, egressID, socket string) {
	if lease == nil {
		return
	}
	r.boundCredentials.mu.Lock()
	defer r.boundCredentials.mu.Unlock()
	if lease.egressID == egressID && lease.socket == socket {
		lease.closed = true
	}
}

func (r *Runtime) credentialAllowedForEgress(ctx context.Context, scope *runEgress) bool {
	if scope == nil || scope.broker == nil {
		return false
	}
	if scope.boundCredential != nil {
		return r.boundCredentialApproved(ctx, scope.boundCredential, scope.id, scope.socket)
	}
	return r.HasCredentialGrant(ctx, scope.provider)
}
