package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestOptimizationCommandIsRegisteredAndFailClosed(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), ".", []string{"optimization"}, strings.NewReader(""), &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "marshal optimization") {
		t.Fatalf("code=%d error=%q", code, errOut.String())
	}
}

func TestOptimizationRejectsUnknownAction(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), ".", []string{"optimization", "approve-everything"}, strings.NewReader(""), &out, &errOut)
	if code == 0 {
		t.Fatalf("unexpected success: %q", out.String())
	}
}
