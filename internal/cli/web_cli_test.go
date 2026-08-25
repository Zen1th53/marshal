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
	if code := Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("init code=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	// Context with immediate timeout so web serve terminates gracefully
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_ = Execute(ctx, repo.Path(), []string{"web", "serve", "--listen", "127.0.0.1", "--port", "18787"}, strings.NewReader(""), &stdout, &stderr)

	outStr := stdout.String()
	if !strings.Contains(outStr, "listening on http://127.0.0.1:18787") {
		t.Fatalf("expected listening message in stdout, got: %s (stderr: %s)", outStr, stderr.String())
	}
}

func TestWebServeRejectsUninitializedRuntime(t *testing.T) {
	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer

	code := Execute(context.Background(), repo.Path(), []string{"web", "serve", "--listen", "127.0.0.1", "--port", "18788"}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected uninitialized runtime to be rejected, stdout=%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "open canonical runtime") {
		t.Fatalf("expected canonical runtime error, stderr=%s", stderr.String())
	}
}
