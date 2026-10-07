//go:build linux

package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestProviderCleanupResetsShortcutBeforeRemoval(t *testing.T) {
	for _, entry := range []struct {
		name, id, provider string
		reset, fail        bool
	}{
		{"codex", "codex", "codex", true, false},
		{"claude", "claude", "claude", true, false},
		{"opencode", "opencode", "opencode", true, false},
		{"agy", "agy", "agy", true, false},
		{"reset failure", "opencode", "opencode", true, true},
		{"task", "task-a", "opencode", false, false},
		{"chat", "marshal-chat", "opencode", false, false},
	} {
		t.Run(entry.name, func(t *testing.T) {
			binary, log := setupFakeTmux(t)
			if entry.fail {
				script, err := os.ReadFile(binary)
				if err != nil {
					t.Fatal(err)
				}
				script = []byte(strings.Replace(string(script), `case "$1" in`, "case \"$1\" in\n  bind-key) exit 1 ;;", 1))
				if err := os.WriteFile(binary, script, 0700); err != nil {
					t.Fatal(err)
				}
			}
			w := NewWorkspace(nil, "project", "session")
			root := t.TempDir()
			w.tmuxSession = "test-session"
			a := &activeTmuxAgent{id: entry.id, provider: entry.provider, paneID: "%129", windowID: "@129"}
			w.tmuxActiveWins[a.id] = a
			err := w.retainAndCloseAgent(t.Context(), a, root, "completed")
			if (err != nil) != entry.fail {
				t.Fatalf("cleanup error = %v, expected failure %v", err, entry.fail)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			calls := string(data)
			binding, removal := strings.Index(calls, "bind-key -T"), strings.Index(calls, "kill-pane")
			if (binding >= 0) != entry.reset {
				t.Fatalf("shortcut reset = %v, want %v: %s", binding >= 0, entry.reset, calls)
			}
			if entry.reset && (!strings.Contains(calls, "display-message") || !strings.Contains(calls, "session ended; use /"+entry.provider+" to reopen.")) {
				t.Fatalf("missing reopening guidance: %s", calls)
			}
			for _, call := range strings.Split(calls, "\n") {
				if strings.HasPrefix(call, "bind-key ") && (strings.Contains(call, a.paneID) || strings.Contains(call, a.windowID)) {
					t.Fatalf("replacement shortcut targets removed provider: %s", call)
				}
			}
			if entry.fail {
				if removal >= 0 || a.cleaned || w.tmuxActiveWins[a.id] == nil {
					t.Fatalf("removed provider after failed shortcut reset: %s", calls)
				}
			} else if removal < 0 || (entry.reset && binding > removal) || !a.cleaned || w.tmuxActiveWins[a.id] != nil {
				t.Fatalf("cleanup did not reset before removal: %s", calls)
			}
		})
	}
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

func TestProviderKeyAfterWindowClosesHasNoDeadTarget(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode", "agy"} {
		t.Run(provider, func(t *testing.T) {
			w := realTmuxWorkspace(t)
			ctx := t.Context()
			if err := tmux.NewWindow(ctx, w.tmuxSession, provider, w.workDir, nil, []string{"sleep", "600"}); err != nil {
				t.Fatal(err)
			}
			panes, err := tmux.ListPanes(ctx, w.tmuxSession)
			if err != nil {
				t.Fatal(err)
			}
			var a *activeTmuxAgent
			for _, pane := range panes {
				if pane.WindowName == provider {
					a = &activeTmuxAgent{id: provider, provider: provider, label: provider, role: "worker", paneID: pane.PaneID, windowID: pane.WindowID, window: provider}
				}
			}
			if a == nil {
				t.Fatal("provider pane absent")
			}
			w.tmuxActiveWins[a.id] = a
			table, key := "marshal-keys-"+tmux.ProjectHash(w.workDir), providerFKey(provider)
			if err := tmux.BindWindowKey(ctx, a.paneID, table, key, "select-window", "-t", a.paneID); err != nil {
				t.Fatal(err)
			}
			before, err := tableKeyBinding(ctx, table, key)
			if err != nil || !strings.Contains(string(before), w.tmuxSession+":"+a.windowID) {
				t.Fatalf("initial binding: %s %v", before, err)
			}
			if err := w.retainAndCloseAgent(ctx, a, w.workDir, "completed"); err != nil {
				t.Fatal(err)
			}
			after, err := tableKeyBinding(ctx, table, key)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(after), a.windowID) || strings.Contains(string(after), a.paneID) {
				t.Fatalf("binding targets removed provider: %s", after)
			}
			if !strings.Contains(string(after), "display-message") || !strings.Contains(string(after), "session ended") {
				t.Fatalf("operator receives no reopening guidance: %s", after)
			}
			panes, err = tmux.ListPanes(ctx, w.tmuxSession)
			if err != nil {
				t.Fatal(err)
			}
			for _, pane := range panes {
				if pane.PaneID == a.paneID {
					t.Fatal("provider pane was not removed")
				}
			}
		})
	}
}

// tableKeyBinding returns the binding of one key in one table. tmux 3.7
// prints nothing for "list-keys -T table key", so the full listing is
// filtered instead.
func tableKeyBinding(ctx context.Context, table, key string) ([]byte, error) {
	out, err := tmux.RunCommand(ctx, "list-keys")
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 && f[1] == "-T" && f[2] == table && f[3] == key {
			lines = append(lines, line)
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}
