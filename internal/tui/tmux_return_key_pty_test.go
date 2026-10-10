//go:build linux

package tui

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestRealClientF11ClosesControlCentreOverlays(t *testing.T) {
	t.Setenv("MARSHAL_NO_UPDATE_CHECK", "1")
	fakeProviderCLIs(t, "codex", "claude", "opencode", "agy")
	w := realTmuxWorkspace(t)
	bin := buildMarshalBinary(t)
	project := initProject(t, bin)
	t.Setenv("PATH", filepath.Dir(w.tmuxPath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx := context.Background()
	panes, err := tmux.ListPanes(ctx, w.tmuxSession)
	if err != nil || len(panes) != 1 {
		t.Fatalf("control pane: %+v %v", panes, err)
	}
	pane := panes[0].PaneID
	w.tmuxMarshalPaneID = pane
	if _, err := tmux.RunCommand(ctx, "respawn-pane", "-k", "-t", pane, "-c", project, bin, "tui"); err != nil {
		t.Fatal(err)
	}
	master, slave := openPTY(t)
	setWinsize(slave, 30, 120)
	client := exec.Command(w.tmuxPath, "attach-session", "-t", w.tmuxSession)
	client.Stdin, client.Stdout, client.Stderr = slave, slave, slave
	client.Env = append(os.Environ(), "TERM=xterm-256color")
	client.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Process.Kill(); _ = client.Wait() }()
	go io.Copy(io.Discard, master)
	waitForTmuxOutput(t, pane, "MARSHAL")
	if err := tmux.NewWindow(ctx, w.tmuxSession, "worker", project, nil, []string{"sleep", "600"}); err != nil {
		t.Fatal(err)
	}
	if err := w.bindWorkspaceKeys(ctx, w.tmuxSession+":worker", project); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		diff    bool
		palette bool
		worker  bool
	}{
		{name: "diff/control", diff: true},
		{name: "palette/control", palette: true},
		{name: "diff/worker", diff: true, worker: true},
		{name: "palette/worker", palette: true, worker: true},
		{name: "diff-and-palette/control", diff: true, palette: true},
		{name: "diff-and-palette/worker", diff: true, palette: true, worker: true},
	} {
		if !t.Run(tc.name, func(t *testing.T) {
			if err := tmux.SelectWindow(ctx, pane); err != nil {
				t.Fatal(err)
			}
			if tc.diff {
				if _, err := master.Write([]byte("\x1bOR")); err != nil {
					t.Fatal(err)
				}
				waitForTmuxOutput(t, pane, "Git Diff")
			}
			if tc.palette {
				// The diff owns the screen even with a palette open behind it.
				// Observe the palette independently in the palette-only cases.
				if tc.diff {
					// Enqueue directly in the control pane before switching windows:
					// the hidden palette cannot acknowledge attached-client input
					// with a visible frame, unlike the palette-only cases below.
					if _, err := tmux.RunCommand(ctx, "send-keys", "-t", pane, "C-p"); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := master.Write([]byte("\x10")); err != nil {
						t.Fatal(err)
					}
					waitForTmuxOutput(t, pane, "Command Palette")
				}
			}
			if tc.worker {
				if err := tmux.SelectWindow(ctx, w.tmuxSession+":worker"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := master.Write([]byte("\x1b[23~")); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				out, err := tmux.CapturePane(ctx, pane)
				if err != nil {
					t.Fatal(err)
				}
				selected, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", w.tmuxSession, "#{pane_id}")
				if err != nil {
					t.Fatal(err)
				}
				if strings.TrimSpace(string(selected)) == pane && strings.Contains(out, "MARSHAL") && !strings.Contains(out, "Git Diff") && !strings.Contains(out, "Command Palette") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("attached-client F11 failed to return to the control centre (selected=%s): %s", selected, out)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}) {
			return
		}
	}
}
