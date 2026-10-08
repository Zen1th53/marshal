//go:build linux

package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskRelayShowsOutcomeAndRetainsRawOutput(t *testing.T) {
	for _, code := range []int{0, 7} {
		root := t.TempDir()
		id := "task-result"
		if err := saveAgentEvidence(root, id+"-relay", ""); err != nil {
			t.Fatal(err)
		}
		relay := filepath.Join(root, "fake relay")
		if err := os.WriteFile(relay, []byte("#!/bin/sh\nprintf 'JSON # Using skills\\\\n instruction payload\\n'\nprintf 'socat noise\\n' >&2\n"), 0700); err != nil {
			t.Fatal(err)
		}
		args := taskRelayCommand(relay, "unused", root, id)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		output, err := os.CreateTemp(root, "screen-")
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout, cmd.Stderr = output, output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		if err := agentCompletion(root, id)(code); err != nil {
			cancel()
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		var screen string
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(output.Name())
			screen = string(data)
			if strings.Contains(screen, "Full output:") {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		// The result view stays alive: tmux has no dead-pane footer to append.
		select {
		case err := <-done:
			cancel()
			t.Fatalf("result view exited: %v", err)
		default:
		}
		cancel()
		<-done
		output.Close()
		status := "completed"
		if code != 0 {
			status = "failed"
		}
		if !strings.Contains(screen, "Status: "+status) || !strings.Contains(screen, "Summary:") || !strings.Contains(screen, "Full output:") || strings.Contains(screen, "instruction payload") || strings.Contains(screen, "socat noise") {
			t.Fatalf("screen: %s", screen)
		}
		raw, err := os.ReadFile(filepath.Join(root, ".marshal", "evidence", id+"-relay-latest.txt"))
		if err != nil || !strings.Contains(string(raw), "instruction payload") || !strings.Contains(string(raw), "socat noise") {
			t.Fatalf("raw evidence: %s (%v)", raw, err)
		}
	}
}

func TestTaskRelayHostPreservesEarlierProcessEvidence(t *testing.T) {
	_, log := setupFakeTmux(t)
	root := t.TempDir()
	id := "task-multiple-processes"
	w := NewWorkspace(nil, "project", "session")
	w.workDir, w.tmuxSession = root, "test-session"
	if err := saveAgentEvidence(root, id+"-relay", "earlier process output\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.hostNativeTmuxWindow(withTaskRelayView(context.Background(), root, id), "task-view", root, "unused", nativeLaunchAutomated); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".marshal", "evidence", id+"-relay-latest.txt"))
	if err != nil || string(raw) != "earlier process output\n" {
		t.Fatalf("earlier output lost: %s (%v)", raw, err)
	}
	data, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(data), "marshal-task-view") || !strings.Contains(string(data), agentOutcomePath(root, id)) {
		t.Fatalf("task host bypassed result view: %s (%v)", data, err)
	}
}

func TestTaskRelayCleanupRetainsFullOutput(t *testing.T) {
	_, _ = setupFakeTmux(t)
	root := t.TempDir()
	w := NewWorkspace(nil, "project", "session")
	w.workDir, w.tmuxSession = root, "test-session"
	id := "task-retained"
	var pane string
	if err := w.hostNativeTmuxWindow(withTaskRelayView(context.Background(), root, id), "task-view", root, "unused", nativeLaunchAutomated, &pane); err != nil {
		t.Fatal(err)
	}
	const raw = "escaped instruction payload\\n\nsocat stderr\n"
	if err := saveAgentEvidence(root, id+"-relay", raw); err != nil {
		t.Fatal(err)
	}
	a := &activeTmuxAgent{id: id, role: "task", paneID: pane}
	w.tmuxActiveWins[id] = a
	if err := w.retainAndCloseAgent(context.Background(), a, root, "completed"); err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(filepath.Join(root, ".marshal", "evidence", id+"-latest.txt"))
	if err != nil || !strings.Contains(string(retained), raw) {
		t.Fatalf("canonical evidence lost raw output: %s (%v)", retained, err)
	}
}

func TestTaskRelayRealSocatSupportsRedirectedOutput(t *testing.T) {
	socat, err := exec.LookPath("socat")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	id := "task-stdio"
	if err := saveAgentEvidence(root, id+"-relay", ""); err != nil {
		t.Fatal(err)
	}
	// Use the production view's exact STDIO argument with a local file target,
	// so this checks socat's terminal options without opening a network socket.
	relay := filepath.Join(root, "local-relay")
	quoted := "'" + strings.ReplaceAll(socat, "'", "'\"'\"'") + "'"
	script := "#!/bin/sh\nif " + quoted + " \"$1\" OPEN:/dev/null; then echo RELAY_STDIO_OK; else echo RELAY_STDIO_FAILED; fi\n"
	if err := os.WriteFile(relay, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	_, slave := openPTY(t)
	args := taskRelayCommand(relay, "unused", root, id)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = slave
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	path := filepath.Join(root, ".marshal", "evidence", id+"-relay-latest.txt")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "RELAY_STDIO_OK") {
			return
		}
		if strings.Contains(string(raw), "RELAY_STDIO_FAILED") {
			t.Fatalf("socat cannot redirect output: %s", raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("socat did not complete the local file relay")
}
