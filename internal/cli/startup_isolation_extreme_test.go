//go:build !governed

package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/startup"
)

func TestMarshalStartupEnvironmentSkipsIsolationProbes(t *testing.T) {
	c := &command{root: t.TempDir()}
	env := c.startupEnvironment()
	if env.SandboxEnforced || env.NetworkEnforced {
		t.Fatalf("Marshal startup performed isolation probes: sandbox=%t network=%t", env.SandboxEnforced, env.NetworkEnforced)
	}
}

func TestMarshalSandboxTextsAgree(t *testing.T) {
	assessment := startup.Assess(context.Background(), startup.NewSystemProber(), startup.Environment{WorkingDir: t.TempDir()})
	check, ok := assessment.Check("env.sandbox")
	if !ok {
		t.Fatal("startup assessment has no env.sandbox check")
	}
	if !strings.Contains(hostWorkersNotice, "without a sandbox") || !strings.Contains(check.Summary, "without a sandbox") {
		t.Fatalf("host notice and startup summary disagree: notice=%q summary=%q", hostWorkersNotice, check.Summary)
	}
}
