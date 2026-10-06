//go:build linux

package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/testutil/processcheck"
	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/worker"
)

func TestInteractiveTmuxNormalCompletionReapsDescendants(t *testing.T) {
	w := realTmuxWorkspace(t)
	index := 0
	processcheck.Completion(t, func(script string) error {
		_, err := w.runNativeAgentInTmux(context.Background(), fmt.Sprintf("test-%d", index), "Test", w.workDir, "/bin/sh", []string{"-c", script}, nil, nil, nil, nil, nil, nil, nil)
		if err != nil {
			return err
		}
		w.tmuxMu.Lock()
		a := w.tmuxActiveWins[fmt.Sprintf("test-%d", index)]
		index++
		w.tmuxMu.Unlock()
		if a == nil || a.handle == nil {
			return fmt.Errorf("interactive window has no driver lifecycle handle")
		}
		select {
		case <-a.handle.Done():
		case <-time.After(time.Second):
			return fmt.Errorf("worker did not complete")
		}
		return nil
	})
}

func TestTaskTmuxNormalCompletionReapsDescendants(t *testing.T) {
	for _, mode := range []string{"native", "governed-runner"} {
		t.Run(mode, func(t *testing.T) {
			w := realTmuxWorkspace(t)
			processcheck.Completion(t, func(script string) error {
				var inner driver.Driver = driver.Native{Provider: "test", Binary: "/bin/sh", Args: func(driver.Request) []string { return []string{"-c", script} }, Parse: func([]byte) []marshal.CommandRecord { return nil }}
				if mode == "governed-runner" {
					inner = driver.Governed{Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
						result, err := worker.New(time.Second, 50*time.Millisecond, 4096).Run(ctx, adapter.Command{Path: "/bin/sh", Args: []string{"-c", script}, Dir: req.Worktree})
						if err == nil && result.ExitCode != 0 {
							err = fmt.Errorf("exit %d", result.ExitCode)
						}
						return nil, err
					}}
				}
				d := &tmuxTaskDriver{w: w, inner: inner}
				h, err := d.Launch(context.Background(), realTaskRequest(t, w, "completion"))
				if err != nil {
					return err
				}
				_, err = d.Wait(context.Background(), h)
				return err
			})
		})
	}
}

func TestImportedTaskCompletionPreservesWorkspaceAndMarshalPanes(t *testing.T) {
	w := realTmuxWorkspace(t)
	ctx := context.Background()
	if err := tmux.NewWindow(ctx, w.tmuxSession, "marshal-chat", w.workDir, nil, []string{"sleep", "600"}); err != nil {
		t.Fatal(err)
	}
	before, err := tmux.ListPanes(ctx, w.tmuxSession)
	if err != nil || len(before) != 2 {
		t.Fatalf("panes before: %+v %v", before, err)
	}
	req := realTaskRequest(t, w, "imported")
	req.Task.ImportedResult = &marshal.ImportedResult{TaskID: "TASK-cli", BaseCommit: req.Task.BaseCommit, ResultCommit: req.Task.BaseCommit}
	d := &tmuxTaskDriver{w: w, inner: driver.Governed{Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) { return nil, nil }}}
	h, err := d.Launch(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	w.tmuxMu.Lock()
	count := len(w.tmuxActiveWins)
	w.tmuxMu.Unlock()
	if count != 0 {
		t.Errorf("task with no terminal host created %d active pane records", count)
	}
	if _, err = d.Wait(ctx, h); err != nil {
		t.Errorf("import completion: %v", err)
	}
	after, err := tmux.ListPanes(ctx, w.tmuxSession)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("protected panes changed: before=%+v after=%+v err=%v", before, after, err)
	}
	if files, _ := filepath.Glob(filepath.Join(w.workDir, ".marshal", "tmux-panes", "*.json")); len(files) != 0 {
		t.Fatalf("pane records for unhosted task: %v", files)
	}
}
