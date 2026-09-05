package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestTUICLIInvocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer

	// 1. Initialize project so runtime exists
	code := Execute(ctx, repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("init failed: %s", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	// 2. Invoke `tui` subcommand with /quit
	in := strings.NewReader("/quit\n")
	code = Execute(ctx, repo.Path(), []string{"tui"}, in, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("tui subcommand returned non-zero code %d: %s", code, stderr.String())
	}
	outStr := stdout.String()
	if !strings.Contains(outStr, "MARSHAL v1.5.0 CONTROL PLANE") {
		t.Fatalf("expected header in tui output:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Exiting MARSHAL terminal workspace") {
		t.Fatalf("expected exit in tui output:\n%s", outStr)
	}

	// 3. Invoke empty args with initialized project - opens TUI directly!
	stdout.Reset()
	stderr.Reset()
	in = strings.NewReader("/quit\n")
	code = Execute(ctx, repo.Path(), []string{}, in, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("empty-arg invocation returned non-zero code %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "MARSHAL v1.5.0 CONTROL PLANE") {
		t.Fatalf("expected empty args to open TUI in initialized project:\n%s", stdout.String())
	}
}
