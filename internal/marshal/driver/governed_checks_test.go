package driver

import (
	"context"
	"testing"
	"time"
)

func TestGovernedCheckRequiresSandboxRunner(t *testing.T) {
	ctx := WithCheckRunner(context.Background(), nil)
	r := runCheck(ctx, "unused", "unused", "touch /tmp/marshal-check-host-escape", time.Second)
	if r.ExitCode == 0 || r.Output != "governed check sandbox runner unavailable" {
		t.Fatalf("check did not fail closed: %+v", r)
	}
}
