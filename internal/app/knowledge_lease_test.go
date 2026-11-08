package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectknowledge"
	"github.com/Zen1th53/marshal/internal/worktree"
	"testing"
)

func TestKnowledgeReadLeaseRevocation(t *testing.T) {
	rt := browserContextRuntime(t)
	head, err := gitMarshal(t.Context(), rt.ProjectRoot(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := worktree.New(rt.layout.Root, rt.layout.Worktrees).Prepare(t.Context(), model.WorktreeRequest{TaskID: "TASK-read", Branch: "marshal/read", BaseCommit: head})
	if err != nil {
		t.Fatal(err)
	}
	binding, closeLease, err := rt.openKnowledgeReadLease(t.Context(), "TASK-read", tree.Path)
	if err != nil {
		t.Fatal(err)
	}
	if binding.LeaseID == "" {
		t.Fatal("missing read lease")
	}
	ctx := withKnowledgeBinding(context.Background(), binding)
	p := authz.Principal{ID: binding.AgentID, Role: authz.Role{Name: "developer", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	if _, err := rt.ProjectKnowledge(ctx, p, binding.ProjectID, binding.TaskID, false, projectknowledge.Query{Mode: "overview", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	closeLease()
	if _, err := rt.ProjectKnowledge(ctx, p, binding.ProjectID, binding.TaskID, false, projectknowledge.Query{Mode: "overview", Limit: 10}); err == nil {
		t.Fatal("revoked source lease remained usable")
	}
}
