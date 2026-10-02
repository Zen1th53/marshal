//go:build darwin

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

func TestDarwinGovernedAdapterRefusesUnverifiedBackend(t *testing.T) {
	runtime := &Runtime{}
	capability := sandbox.ProbeForOS(context.Background(), "darwin")
	for _, name := range []string{"codex", "claude", "gemini", "opencode"} {
		candidate, grant, err := runtime.resolveAdapter(context.Background(), name, model.Task{}, "", "", false, "", "")
		if !capability.Available && (candidate != nil || grant != "" || !errors.Is(err, model.ErrUnavailable)) {
			t.Fatalf("%s: candidate=%v grant=%q error=%v", name, candidate, grant, err)
		}
		if capability.Available && err != nil && !errors.Is(err, model.ErrUnavailable) {
			t.Fatalf("provider lookup: %v", err)
		}
	}
}

func TestDarwinNativeLaunchersRequireWrappedRunner(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "must-not-launch")
	binary := filepath.Join(root, "provider")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n/usr/bin/touch '"+marker+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{}
	task := execution.TaskExecution{TaskID: "task", RunID: "run", RunRevision: 1}
	_, handled, err := runtime.executeProcess05CodexAppServer(context.Background(), task, execution.ConstraintPackage{}, root, codex.New(binary, nil), adapter.Request{}, "")
	if handled || !errors.Is(err, model.ErrUnavailable) || !strings.Contains(err.Error(), "does not apply the platform sandbox") {
		t.Fatalf("Codex: handled=%v error=%v", handled, err)
	}
	_, handled, err = runtime.executeProcess05ClaudeStream(context.Background(), task, execution.ConstraintPackage{}, root, claude.New(binary, nil), adapter.Request{}, "")
	if handled || !errors.Is(err, model.ErrUnavailable) || !strings.Contains(err.Error(), "does not apply the platform sandbox") {
		t.Fatalf("Claude: handled=%v error=%v", handled, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("native launcher executed: %v", err)
	}
}
