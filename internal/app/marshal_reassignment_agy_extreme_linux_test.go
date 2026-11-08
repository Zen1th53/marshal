//go:build !governed && linux

package app

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/project"
)

func TestGovernedAGYCountsForReassignment(t *testing.T) {
	fixture, _ := marshalFixture(t, 1)
	runtime := &Runtime{store: fixture.Store, layout: project.Layout{Root: fixture.Repository, Worktrees: fixture.Worktrees}}
	service, err := runtime.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if service.GovernedDrivers["agy"] == nil {
		t.Fatal("private AGY governed driver is missing")
	}
	for name := range service.GovernedDrivers {
		if name != "agy" {
			delete(service.GovernedDrivers, name)
		}
	}
	if got := service.otherWorker("codex", marshal.Governed); got != "agy" {
		t.Fatalf("governed AGY replacement=%q", got)
	}
	if got := service.otherWorker("agy", marshal.Governed); got != "" {
		t.Fatalf("same AGY worker was reassigned to itself: %q", got)
	}
	if got := service.otherWorker("antigravity", marshal.Governed); got != "" {
		t.Fatalf("AGY alias was reassigned to itself: %q", got)
	}
}
