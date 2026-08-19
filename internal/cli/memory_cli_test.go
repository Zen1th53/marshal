package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestT131MemoryCLIAndOperatorWorkflows(t *testing.T) {
	repo := cliRepo(t)
	ctx := context.Background()

	// 1. Help output should document memory command
	var helpBuf bytes.Buffer
	var helpErr bytes.Buffer
	code := Execute(ctx, repo.Path(), []string{"--help"}, strings.NewReader(""), &helpBuf, &helpErr)
	if code != 0 || !strings.Contains(helpBuf.String(), "memory") {
		t.Fatalf("expected memory in help output, code=%d out=%s err=%s", code, helpBuf.String(), helpErr.String())
	}

	// 2. memory status command
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	code = Execute(ctx, repo.Path(), []string{"--json", "memory", "status"}, strings.NewReader(""), &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("memory status failed, code=%d err=%s", code, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), `"version"`) && !strings.Contains(outBuf.String(), `"healthy"`) {
		t.Fatalf("unexpected JSON status output: %s", outBuf.String())
	}

	// 3. memory recall command
	var recallBuf bytes.Buffer
	var recallErr bytes.Buffer
	code = Execute(ctx, repo.Path(), []string{"--json", "memory", "recall", "SQLite", "WAL"}, strings.NewReader(""), &recallBuf, &recallErr)
	if code != 0 {
		t.Fatalf("memory recall failed, code=%d err=%s", code, recallErr.String())
	}
	if !strings.Contains(recallBuf.String(), `"query"`) {
		t.Fatalf("unexpected recall output: %s", recallBuf.String())
	}

	// 4. memory promote dry-run
	var promoteBuf bytes.Buffer
	var promoteErr bytes.Buffer
	code = Execute(ctx, repo.Path(), []string{"--json", "memory", "promote", "MEM-123", "--dry-run"}, strings.NewReader(""), &promoteBuf, &promoteErr)
	if code != 0 {
		t.Fatalf("memory promote dry-run failed, code=%d err=%s", code, promoteErr.String())
	}
	if !strings.Contains(promoteBuf.String(), `"dry_run": true`) && !strings.Contains(promoteBuf.String(), `"dry_run":true`) {
		t.Fatalf("expected dry-run flag in promote output: %s", promoteBuf.String())
	}
}
