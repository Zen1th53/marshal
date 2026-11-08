package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

func TestPermissionCLIGrantAuthorizesOneShotCapturePath(t *testing.T) {
	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("init code=%d stderr=%q", code, stderr.String())
	}
	folder := t.TempDir()
	stdout.Reset()
	stderr.Reset()
	if code := Execute(context.Background(), repo.Path(), []string{"permission", "read", "allow", folder}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("grant code=%d stderr=%q", code, stderr.String())
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	// Native one-shot capture asks this exact runtime gate before reading each
	// provider folder. The fake makes that gate result explicit in the CLI test.
	fake := &fakeOneShotRuntime{allow: runtime.HasReadGrant(folder)}
	if !fake.HasReadGrant(folder) {
		t.Fatal("CLI grant did not authorize the one-shot history capture path")
	}
}
