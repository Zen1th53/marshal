package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestMonitorOSC7501AndTitleIngestion(t *testing.T) {
	tempDir := t.TempDir()
	fakeTmuxPath := filepath.Join(tempDir, "tmux")
	paneOutput := filepath.Join(tempDir, "pane_output")
	paneTitle := filepath.Join(tempDir, "pane_title")
	logFile := filepath.Join(tempDir, "tmux.log")

	script := fmt.Sprintf(`#!/bin/sh
logFile=%q
paneOutput=%q
paneTitle=%q
echo "$@" >> "$logFile"
case "$1" in
  display-message)
    case "$*" in
      *"#{pane_id}"*)
        printf '%%%%1\n'
        exit 0
        ;;
      *"#{pane_title}"*)
        if [ -f "$paneTitle" ]; then cat "$paneTitle"; else echo ""; fi
        exit 0
        ;;
      *"#{pane_height} #{pane_width}"*)
        printf '40 120\n'
        exit 0
        ;;
      *)
        exit 0
        ;;
    esac
    ;;
  pipe-pane)
    # The pane's raw output, escape sequences included, goes to the
    # pipe command, as tmux pipe-pane -O does.
    for last; do :; done
    if [ "$2" = "-O" ] && [ -f "$paneOutput" ]; then sh -c "$last" < "$paneOutput" >/dev/null 2>&1 & fi
    exit 0
    ;;
  capture-pane)
    # capture-pane never carries OSC sequences: tmux consumes them.
    echo ""
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`, logFile, paneOutput, paneTitle)

	if err := os.WriteFile(fakeTmuxPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	tmux.SetBinaryPath(fakeTmuxPath)
	t.Cleanup(tmux.ResetBinaryPath)

	root := t.TempDir()
	w := NewWorkspace(nil, "project", "session")
	w.tmuxPath = fakeTmuxPath
	w.tmuxSession = "test-session"

	secretToken := "sk-live-secret-key-12345"
	msg := "Working on auth with " + secretToken
	encodedMsg := base64.StdEncoding.EncodeToString([]byte(msg))
	oscSequence := "\x1b]7501;state=blocked:kind=permission:app=codex:msg=" + encodedMsg + "\x07"

	if err := os.WriteFile(paneOutput, []byte("Some terminal noise\n"+oscSequence+"\nMore noise"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paneTitle, []byte("codex: idle waiting"), 0644); err != nil {
		t.Fatal(err)
	}

	agent := &activeTmuxAgent{
		provider: "codex",
		paneID:   "%1",
		doneChan: make(chan struct{}),
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["codex"] = agent
	w.tmuxMu.Unlock()

	agentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	view := &inboxView{}
	w.monitorAgent(agentCtx, agent, root, nil, nil, nil, nil, view)

	var st liveStatus
	var err error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st, err = readLiveStatus(root, "codex")
		if err == nil && st.State != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err != nil {
		t.Fatalf("failed to read live status: %v", err)
	}
	logBytes, _ := os.ReadFile(logFile)
	t.Logf("tmux log:\n%s", string(logBytes))
	t.Logf("read live status: %+v", st)

	if st.State != "blocked" {
		t.Fatalf("expected state 'blocked' from OSC 7501 (winning over title), got %q", st.State)
	}
	if strings.Contains(st.LastAction, secretToken) {
		t.Fatalf("secret leaked into LastAction: %q", st.LastAction)
	}
	if !strings.Contains(st.LastAction, "[REDACTED]") {
		t.Fatalf("expected redacted secret in LastAction, got %q", st.LastAction)
	}
}

func TestMonitorTitleHeuristicFallbackWhenOSC7501Absent(t *testing.T) {
	tempDir := t.TempDir()
	fakeTmuxPath := filepath.Join(tempDir, "tmux")
	paneOutput := filepath.Join(tempDir, "pane_output")
	paneTitle := filepath.Join(tempDir, "pane_title")

	script := fmt.Sprintf(`#!/bin/sh
paneOutput=%q
paneTitle=%q
case "$1" in
  display-message)
    case "$*" in
      *"#{pane_id}"*)
        printf '%%%%2\n'
        exit 0
        ;;
      *"#{pane_title}"*)
        if [ -f "$paneTitle" ]; then cat "$paneTitle"; else echo ""; fi
        exit 0
        ;;
      *"#{pane_height} #{pane_width}"*)
        printf '40 120\n'
        exit 0
        ;;
      *)
        exit 0
        ;;
    esac
    ;;
  pipe-pane)
    # The pane's raw output, escape sequences included, goes to the
    # pipe command, as tmux pipe-pane -O does.
    for last; do :; done
    if [ "$2" = "-O" ] && [ -f "$paneOutput" ]; then sh -c "$last" < "$paneOutput" >/dev/null 2>&1 & fi
    exit 0
    ;;
  capture-pane)
    # capture-pane never carries OSC sequences: tmux consumes them.
    echo ""
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`, paneOutput, paneTitle)

	if err := os.WriteFile(fakeTmuxPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	tmux.SetBinaryPath(fakeTmuxPath)
	t.Cleanup(tmux.ResetBinaryPath)

	root := t.TempDir()
	w := NewWorkspace(nil, "project", "session")
	w.tmuxPath = fakeTmuxPath
	w.tmuxSession = "test-session"

	if err := os.WriteFile(paneOutput, []byte("Just plain terminal output without OSC 7501"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paneTitle, []byte("claude: waiting for input"), 0644); err != nil {
		t.Fatal(err)
	}

	agent := &activeTmuxAgent{
		provider: "claude",
		paneID:   "%2",
		doneChan: make(chan struct{}),
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["claude"] = agent
	w.tmuxMu.Unlock()

	agentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	view := &inboxView{}
	w.monitorAgent(agentCtx, agent, root, nil, nil, nil, nil, view)

	var st liveStatus
	var err error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st, err = readLiveStatus(root, "claude")
		if err == nil && st.State != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err != nil {
		t.Fatalf("failed to read live status: %v", err)
	}
	if st.State != "blocked" {
		t.Fatalf("expected state 'blocked' inferred from title 'waiting', got %q", st.State)
	}
	if st.LastAction != "claude: waiting for input" {
		t.Fatalf("expected LastAction to match title, got %q", st.LastAction)
	}
}
