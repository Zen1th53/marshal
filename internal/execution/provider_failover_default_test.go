//go:build governed

package execution

import (
	"context"
	"errors"
	"testing"
)

func TestDefaultProviderFailoverCannotRebindHarness(t *testing.T) {
	engine, err := NewEngine(EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if _, err = engine.RebindProviderFailover(context.Background(), "run", 1, "task", "codex", "agy", "base", "receipt", func(ExecutionRun, TaskExecution) error { called = true; return nil }); !errors.Is(err, ErrUnauthorizedWorker) {
		t.Fatalf("default failover was authorized: %v", err)
	}
	if called {
		t.Fatal("default build invoked extreme failover authority")
	}
}
