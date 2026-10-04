package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/processgroup"
	"github.com/Zen1th53/marshal/internal/tmux"
)

type agentRecord struct {
	ExecutionRunID  string                 `json:"execution_run_id"`
	CanonicalTaskID string                 `json:"canonical_task_id"`
	RunID           string                 `json:"run_id"`
	Project         string                 `json:"project"`
	Session         string                 `json:"session"`
	ID              string                 `json:"id"`
	Role            string                 `json:"role"`
	TaskID          string                 `json:"task_id"`
	Provider        string                 `json:"provider"`
	Label           string                 `json:"label"`
	Window          string                 `json:"window"`
	Pane            string                 `json:"pane"`
	Supervisor      processgroup.Reference `json:"supervisor"`
	Outcome         string                 `json:"outcome,omitempty"`
}

func agentRecordPath(root, pane string) string {
	return filepath.Join(root, ".marshal", "tmux-panes", strings.ReplaceAll(pane, "%", "pane-")+".json")
}
func loadAgentRecord(root, pane string) (agentRecord, error) {
	var record agentRecord
	data, err := os.ReadFile(agentRecordPath(root, pane))
	if err != nil {
		return record, err
	}
	err = json.Unmarshal(data, &record)
	if err == nil && record.Pane != pane {
		err = fmt.Errorf("pane identity mismatch")
	}
	return record, err
}
func saveAgentRecord(root, session string, a *activeTmuxAgent) error {
	if a.paneID == "" {
		return nil
	}
	record := agentRecord{ExecutionRunID: a.executionRunID, CanonicalTaskID: a.canonicalTaskID, RunID: a.runID, Project: tmux.ProjectHash(root), Session: session, ID: a.id, Role: a.role, TaskID: a.taskID, Provider: a.provider, Label: a.label, Window: a.window, Pane: a.paneID, Supervisor: a.supervisor, Outcome: a.state}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	path := agentRecordPath(root, a.paneID)
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".pane-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}
func (w *Workspace) stopAgent(a *activeTmuxAgent) error {
	w.tmuxMu.Lock()
	a = copyAgentLocked(a)
	w.tmuxMu.Unlock()
	if a.role == "marshal-chat" {
		return fmt.Errorf("Marshal chat cannot be stopped as a worker")
	}
	if a.driver != nil && a.handle != nil {
		return a.driver.Cancel(a.handle)
	}
	return driver.StopRecovered(a.supervisor)
}
func (w *Workspace) retainAndCloseAgent(ctx context.Context, a *activeTmuxAgent, root, outcome string) error {
	a.cleanupMu.Lock()
	defer a.cleanupMu.Unlock()
	if a.cleaned {
		return nil
	}
	w.tmuxMu.Lock()
	pane, id := a.paneID, a.id
	w.tmuxMu.Unlock()
	evidence, err := tmux.CapturePane(ctx, pane)
	if err != nil {
		return fmt.Errorf("capture evidence for %s: %w", id, err)
	}
	if err = saveAgentEvidence(root, id, evidence); err != nil {
		return fmt.Errorf("retain evidence for %s: %w", id, err)
	}
	w.tmuxMu.Lock()
	a.state = outcome
	err = saveAgentRecord(root, w.tmuxSession, a)
	w.tmuxMu.Unlock()
	if err != nil {
		return fmt.Errorf("record stop/completion for %s: %w", id, err)
	}
	if err = tmux.KillPane(ctx, pane); err != nil {
		return err
	}
	a.cleaned = true
	w.tmuxMu.Lock()
	delete(w.tmuxActiveWins, id)
	w.tmuxMu.Unlock()
	return nil
}
