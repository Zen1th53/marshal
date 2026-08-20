package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

func TestT131MemoryCLIAndOperatorWorkflows(t *testing.T) {
	repo := cliRepo(t)
	ctx := context.Background()
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}

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

	// 3. Write memory then recall it through the same CLI surface.
	var rememberBuf bytes.Buffer
	var rememberErr bytes.Buffer
	code = Execute(ctx, repo.Path(), []string{"--json", "memory", "remember", "SQLite WAL", "journal_mode=WAL"}, strings.NewReader(""), &rememberBuf, &rememberErr)
	if code != 0 {
		t.Fatalf("memory remember failed, code=%d err=%s", code, rememberErr.String())
	}
	if !strings.Contains(rememberBuf.String(), `"REMEMBERED"`) && !strings.Contains(rememberBuf.String(), "REMEMBERED") {
		t.Fatalf("unexpected remember output: %s", rememberBuf.String())
	}

	var recallBuf bytes.Buffer
	var recallErr bytes.Buffer
	code = Execute(ctx, repo.Path(), []string{"--json", "memory", "recall", "SQLite", "WAL"}, strings.NewReader(""), &recallBuf, &recallErr)
	if code != 0 {
		t.Fatalf("memory recall failed, code=%d err=%s", code, recallErr.String())
	}
	if !strings.Contains(recallBuf.String(), `"query"`) {
		t.Fatalf("unexpected recall output: %s", recallBuf.String())
	}
	if !strings.Contains(recallBuf.String(), "SQLite WAL") {
		t.Fatalf("recall did not return the persisted record: %s", recallBuf.String())
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
