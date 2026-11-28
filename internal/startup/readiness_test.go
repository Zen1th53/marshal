package startup_test

import (
	"context"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/startup"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupUsesSharedReadiness(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, statepath.DirName), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, statepath.DirName, "default-provider"), []byte("codex\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := healthyProject(root)
	p.onPath["codex"] = true
	r := startup.ObserveReadiness(context.Background(), p, root, marshal.ReviewPolicy{FinalModel: marshal.FinalModelOff})
	if r.PlannerRunnable || !strings.Contains(r.Blocker, "codex login") {
		t.Fatalf("%+v", r)
	}
	env := healthyEnv(root)
	env.Readiness = &r
	a := startup.Assess(context.Background(), p, env)
	c, ok := a.Check("execution.readiness")
	if !ok || c.Summary != r.Blocker || a.Phase == startup.PhaseReady || a.ExecutionPermitted() {
		t.Fatalf("%+v", a)
	}
}
