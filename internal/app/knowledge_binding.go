package app

import (
	"context"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectknowledge"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
	"github.com/Zen1th53/marshal/internal/store"
)

// KnowledgeBinding freezes the canonical worker lease and its exact checkout.
// It is runtime data; public tool arguments cannot choose these fields.
type KnowledgeBinding struct {
	ProjectIdentity, ProjectID, TaskID, LeaseID, AgentID, SessionID, Worktree string
}

func (r *Runtime) BindWorkerKnowledge(ctx context.Context, taskID string) (KnowledgeBinding, error) {
	if r == nil || r.store == nil || taskID == "" {
		return KnowledgeBinding{}, model.ErrPolicyDenied
	}
	project, err := r.store.Project(ctx)
	if err != nil {
		return KnowledgeBinding{}, err
	}
	active, err := r.store.ActiveLease(ctx, taskID)
	if err != nil {
		return KnowledgeBinding{}, err
	}
	if !active.Lease.ExpiresAt.After(time.Now().UTC()) {
		return KnowledgeBinding{}, model.ErrPolicyDenied
	}
	session, err := r.store.GetSession(ctx, active.Lease.SessionID)
	if err != nil {
		return KnowledgeBinding{}, err
	}
	task, err := r.store.GetTask(ctx, taskID)
	if err != nil {
		return KnowledgeBinding{}, err
	}
	agent, err := r.store.GetAgent(ctx, active.AgentID)
	if err != nil {
		return KnowledgeBinding{}, err
	}
	if session.Status != model.SessionActive || session.ProjectID != project.ID || session.AgentID != active.AgentID || agent.ProjectID != project.ID || agent.Status == model.AgentDisabled || task.Status != model.TaskWorking || task.ControlState != "" || task.Worktree == nil || task.OwnerAgentID == nil || *task.OwnerAgentID != active.AgentID {
		return KnowledgeBinding{}, model.ErrPolicyDenied
	}
	root := *task.Worktree
	if _, err = projectknowledge.OpenWorktree(root, r.layout.Root, r.layout.RuntimeDir); err != nil {
		return KnowledgeBinding{}, err
	}
	return KnowledgeBinding{ProjectIdentity: r.ProjectIdentity(), ProjectID: project.ID, TaskID: taskID, LeaseID: active.Lease.ID, AgentID: active.AgentID, SessionID: session.ID, Worktree: root}, nil
}

func (r *Runtime) ValidateKnowledgeBinding(ctx context.Context, b KnowledgeBinding) error {
	current, err := r.WorkerKnowledgeLease(ctx, b.TaskID, b.LeaseID)
	if err != nil {
		return err
	}
	if current != b {
		return model.ErrPolicyDenied
	}
	return nil
}

type knowledgeBindingKey struct{}

func withKnowledgeBinding(ctx context.Context, b KnowledgeBinding) context.Context {
	return context.WithValue(ctx, knowledgeBindingKey{}, b)
}

// KnowledgeContext revalidates a server's immutable binding before query delivery.
func (r *Runtime) KnowledgeContext(ctx context.Context, b KnowledgeBinding) (context.Context, error) {
	if err := r.ValidateKnowledgeBinding(ctx, b); err != nil {
		return nil, err
	}
	return withKnowledgeBinding(ctx, b), nil
}

func (r *Runtime) knowledgeReadBinding(ctx context.Context, id string) (KnowledgeBinding, error) {
	l, err := r.store.KnowledgeReadLease(ctx, id)
	if err != nil {
		return KnowledgeBinding{}, err
	}
	project, err := r.store.Project(ctx)
	if err != nil {
		return KnowledgeBinding{}, err
	}
	if l.ProjectID != project.ID || l.ProjectIdentity != r.ProjectIdentity() {
		return KnowledgeBinding{}, model.ErrPolicyDenied
	}
	if _, err := projectknowledge.OpenWorktree(l.Worktree, r.layout.Root, r.layout.RuntimeDir); err != nil {
		return KnowledgeBinding{}, err
	}
	return KnowledgeBinding{ProjectIdentity: l.ProjectIdentity, ProjectID: l.ProjectID, TaskID: l.TaskID, LeaseID: l.ID, AgentID: l.PrincipalID, Worktree: l.Worktree, SessionID: l.RuntimeID}, nil
}

// WorkerKnowledgeLease resolves an explicitly supplied lease. It never substitutes
// a newer claim for a provider whose original lease has ended.
func (r *Runtime) WorkerKnowledgeLease(ctx context.Context, taskID, leaseID string) (KnowledgeBinding, error) {
	if strings.HasPrefix(leaseID, "KLEASE-") {
		b, err := r.knowledgeReadBinding(ctx, leaseID)
		if err != nil {
			return b, err
		}
		if b.TaskID != taskID {
			return KnowledgeBinding{}, model.ErrPolicyDenied
		}
		return b, nil
	}
	b, err := r.BindWorkerKnowledge(ctx, taskID)
	if err != nil {
		return b, err
	}
	if b.LeaseID != leaseID {
		return KnowledgeBinding{}, model.ErrPolicyDenied
	}
	return b, nil
}

func (r *Runtime) openKnowledgeReadLease(ctx context.Context, taskID, root string) (KnowledgeBinding, func(), error) {
	noop := func() {}
	index, err := projectknowledge.OpenWorktree(root, r.layout.Root, r.layout.RuntimeDir)
	if err != nil {
		return KnowledgeBinding{}, noop, err
	}
	if err = index.Ensure(ctx); err != nil {
		return KnowledgeBinding{}, noop, err
	}
	id, err := model.NewID("KLEASE-")
	if err != nil {
		return KnowledgeBinding{}, noop, err
	}
	project, err := r.store.Project(ctx)
	if err != nil {
		return KnowledgeBinding{}, noop, err
	}
	l := store.KnowledgeReadLease{ID: id, ProjectID: project.ID, ProjectIdentity: r.ProjectIdentity(), TaskID: taskID, PrincipalID: id, Worktree: root, RuntimeID: r.runtimeInstanceID, ExpiresAt: time.Now().UTC().Add(15 * time.Minute)}
	if err = r.store.CreateKnowledgeReadLease(ctx, l); err != nil {
		return KnowledgeBinding{}, noop, err
	}
	b, err := r.knowledgeReadBinding(ctx, id)
	if err != nil {
		_ = r.store.RevokeKnowledgeReadLease(context.Background(), id)
		return b, noop, err
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer r.store.RevokeKnowledgeReadLease(context.Background(), id)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				if err := r.store.RenewKnowledgeReadLease(leaseCtx, id, r.runtimeInstanceID, time.Now().UTC().Add(15*time.Minute)); err != nil {
					return
				}
			}
		}
	}()
	cleanup := func() { cancel(); <-done }

	return b, cleanup, nil
}

func (r *Runtime) configureKnowledgeMCP(ctx context.Context, provider, root string, args, env []string) ([]string, []string, func(), error) {
	if root == r.layout.Root {
		return sessionmcp.ConfigureMarshal(provider, root, args, env)
	}
	binding, revoke, err := r.openKnowledgeReadLease(ctx, "marshal-review", root)
	if err != nil {
		return nil, nil, func() {}, err
	}
	args, env, closeTransport, err := sessionmcp.Configure(sessionmcp.WithWorker(ctx, r.layout.Root, binding.TaskID, binding.LeaseID), nativeKnowledgeProvider(provider), args, env)
	if err != nil {
		revoke()
		return nil, nil, func() {}, err
	}
	return args, env, func() { closeTransport(); revoke() }, nil
}

// leaseWorkerMCPContext supplies the read-only child transport when no supervised
// host server is installed. Process 05 gets a lease for its exact worktree;
// canonical Runtime workers retain their existing claim lease.
func (r *Runtime) leaseWorkerMCPContext(ctx context.Context, taskID string, attempt *execution.TaskExecution) (context.Context, func(), error) {
	noop := func() {}
	if attempt != nil {
		binding, closeLease, err := r.openKnowledgeReadLease(ctx, taskID, attempt.WorktreePath)
		if err != nil {
			return nil, noop, err
		}
		return sessionmcp.WithWorker(ctx, r.layout.Root, taskID, binding.LeaseID), closeLease, nil
	}
	binding, err := r.BindWorkerKnowledge(ctx, taskID)
	if err != nil {
		return nil, noop, err
	}
	index, err := projectknowledge.OpenWorktree(binding.Worktree, r.layout.Root, r.layout.RuntimeDir)
	if err != nil {
		return nil, noop, err
	}
	if err := index.Ensure(ctx); err != nil {
		return nil, noop, err
	}
	return sessionmcp.WithWorker(ctx, r.layout.Root, taskID, binding.LeaseID), noop, nil
}

func nativeKnowledgeProvider(provider string) string {
	if provider == "agy" {
		return "antigravity"
	}
	return provider
}
