package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestT167WebCLIServeHelpAndExecution(t *testing.T) {
	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer

	// Context with immediate timeout so web serve terminates gracefully
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_ = Execute(ctx, repo.Path(), []string{"web", "serve", "--listen", "127.0.0.1", "--port", "18787"}, strings.NewReader(""), &stdout, &stderr)

	outStr := stdout.String()
	if !strings.Contains(outStr, "listening on http://127.0.0.1:18787") {
		t.Fatalf("expected listening message in stdout, got: %s (stderr: %s)", outStr, stderr.String())
	}
}
