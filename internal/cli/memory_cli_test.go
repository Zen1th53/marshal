package cli

import (
	"bytes"
	"context"
	"encoding/json"
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

	// 3. Remember survives a process boundary as an inactive candidate.
	var rememberBuf bytes.Buffer
	var rememberErr bytes.Buffer
	code = Execute(ctx, repo.Path(), []string{"--json", "memory", "remember", "SQLite WAL", "journal_mode=WAL"}, strings.NewReader(""), &rememberBuf, &rememberErr)
	if code != 0 {
		t.Fatalf("memory remember failed, code=%d err=%s", code, rememberErr.String())
	}
	if !strings.Contains(rememberBuf.String(), `"REMEMBERED"`) && !strings.Contains(rememberBuf.String(), "REMEMBERED") {
		t.Fatalf("unexpected remember output: %s", rememberBuf.String())
	}
	var remembered struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		Lifecycle string `json:"lifecycle"`
	}
	if err := json.Unmarshal(rememberBuf.Bytes(), &remembered); err != nil || remembered.ID == "" || remembered.Status != "REMEMBERED" {
		t.Fatalf("invalid remember receipt: %+v, %v, output=%s", remembered, err, rememberBuf.String())
	}
	// Execute closes its runtime on return. A new invocation must still find
	// the exact record that the previous invocation claimed to remember.
	var showBuf bytes.Buffer
	var showErr bytes.Buffer
	code = Execute(ctx, repo.Path(), []string{"--json", "memory", "show", remembered.ID}, strings.NewReader(""), &showBuf, &showErr)
	if code != 0 || !strings.Contains(showBuf.String(), "journal_mode=WAL") {
		t.Fatalf("remember receipt did not survive runtime reopen, code=%d output=%s err=%s", code, showBuf.String(), showErr.String())
	}
	var persisted struct {
		ID        string `json:"id"`
		Lifecycle string `json:"lifecycle"`
		Title     string `json:"title"`
		Body      string `json:"body"`
	}
	if err := json.Unmarshal(showBuf.Bytes(), &persisted); err != nil || persisted.ID != remembered.ID || persisted.Lifecycle != remembered.Lifecycle || persisted.Title != "SQLite WAL" || persisted.Body != "journal_mode=WAL" {
		t.Fatalf("remembered record mismatch after reopen: %+v, %v, output=%s", persisted, err, showBuf.String())
	}
	outBuf.Reset()
	errBuf.Reset()
	code = Execute(ctx, repo.Path(), []string{"--json", "memory", "status"}, strings.NewReader(""), &outBuf, &errBuf)
	var status struct {
		Records int `json:"records"`
	}
	if code != 0 || json.Unmarshal(outBuf.Bytes(), &status) != nil || status.Records != 1 {
		t.Fatalf("memory status lost remembered record, code=%d output=%s err=%s", code, outBuf.String(), errBuf.String())
	}
	runtime, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	events, err := runtime.Store().ListEvents(ctx)
	if err != nil {
		runtime.Close()
		t.Fatal(err)
	}
	runtime.Close()
	approved := false
	for _, event := range events {
		if event.Type == "PERMISSION_DECIDED" && event.Data["kind"] == "memory" && event.Data["object"] == remembered.ID && event.Data["allow"] == true && event.Data["source"] == "operator memory review" {
			approved = true
		}
	}
	if !approved {
		t.Fatal("remembered record lacks a durable operator review decision")
	}

	var recallBuf bytes.Buffer
	var recallErr bytes.Buffer
	code = Execute(ctx, repo.Path(), []string{"--json", "memory", "recall", "SQLite", "WAL"}, strings.NewReader(""), &recallBuf, &recallErr)
	if code != 0 {
		t.Fatalf("memory recall failed, code=%d err=%s", code, recallErr.String())
	}
	var recalled struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(recallBuf.Bytes(), &recalled); err != nil || len(recalled.Results) != 1 || recalled.Results[0].ID != remembered.ID {
		t.Fatalf("recall did not return the exact persisted record: %+v, %v, output=%s", recalled, err, recallBuf.String())
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
