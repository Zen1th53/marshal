package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/execution"
)

// checkpointDiffLimit bounds the paths a snapshot comparison reports.
const checkpointDiffLimit = 200

// handleCheckpointRead lists, inspects and compares this session's execution
// snapshots. It reads only: nothing is captured, restored or repaired, and a
// snapshot whose files no longer match its digest is reported, never used.
func (h *CommandHandler) handleCheckpointRead(ctx context.Context, args []string) (string, error) {
	a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.runtime == nil {
		return "Checkpoints are unavailable in TUI: no runtime is attached.", nil
	}
	service, err := a.execution()
	if err != nil {
		return "Checkpoints are unavailable: " + err.Error(), nil
	}
	switch strings.ToLower(args[0]) {
	case "list":
		records, err := a.Checkpoints(ctx)
		if err != nil {
			return "", fmt.Errorf("list checkpoints: %w", err)
		}
		if len(records) == 0 {
			return "No execution snapshots in this session.", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "EXECUTION SNAPSHOTS (%d, newest first; project files, excluding .git and .marshal):\n", len(records))
		for _, r := range records {
			restored := ""
			if r.RestoredAt != nil {
				restored = " · restored " + r.RestoredAt.Format("2006-01-02 15:04")
			}
			fmt.Fprintf(&b, "  %s  task %s  %s  %s%s\n", r.CheckpointID, r.TaskID, r.CreatedAt.Format("2006-01-02 15:04:05"), RedactContent(r.Reason, nil), restored)
		}
		b.WriteString("Collaboration handoff checkpoints are separate records (/inspect checkpoint <id>) and hold no files to restore.")
		return b.String(), nil
	case "inspect":
		check, err := service.VerifyCheckpoint(ctx, strings.TrimPrefix(args[1], "#"))
		if err != nil {
			return "", fmt.Errorf("inspect checkpoint: %w", err)
		}
		r := check.Record
		var b strings.Builder
		fmt.Fprintf(&b, "Snapshot %s\n", r.CheckpointID)
		fmt.Fprintf(&b, "  Run / task:  %s / %s\n", r.RunID, r.TaskID)
		commit := strings.TrimSpace(r.GitCommit)
		if commit == "" {
			commit = "none"
		}
		fmt.Fprintf(&b, "  Git commit:  %s\n", commit)
		fmt.Fprintf(&b, "  Created:     %s\n", r.CreatedAt.Format("2006-01-02 15:04:05Z07:00"))
		fmt.Fprintf(&b, "  Reason:      %s\n", RedactContent(r.Reason, nil))
		fmt.Fprintf(&b, "  Scope:       project files, excluding .git and .marshal\n")
		fmt.Fprintf(&b, "  Files:       %s", check.State)
		switch check.State {
		case execution.SnapshotIntact:
			b.WriteString(" (re-read and match the digest bound at capture)")
		case execution.SnapshotUnreadable:
			fmt.Fprintf(&b, " (%s); not usable", RedactContent(check.Detail, nil))
		default:
			b.WriteString("; not usable for comparison or restore")
		}
		if r.RestoredAt != nil {
			fmt.Fprintf(&b, "\n  Restored:    %s", r.RestoredAt.Format("2006-01-02 15:04:05Z07:00"))
		}
		return b.String(), nil
	case "diff":
		diff, err := service.DiffCheckpoints(ctx, strings.TrimPrefix(args[1], "#"), strings.TrimPrefix(args[2], "#"), checkpointDiffLimit)
		if err != nil {
			return "", fmt.Errorf("compare checkpoints: %w", err)
		}
		total := len(diff.Added) + len(diff.Removed) + len(diff.Changed)
		if total == 0 {
			return fmt.Sprintf("Snapshots %s and %s hold identical files.", diff.From, diff.To), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Snapshot %s -> %s: %d added, %d removed, %d changed\n", diff.From, diff.To, len(diff.Added), len(diff.Removed), len(diff.Changed))
		for _, group := range []struct {
			mark  string
			paths []string
		}{{"+", diff.Added}, {"-", diff.Removed}, {"~", diff.Changed}} {
			for _, p := range group.paths {
				fmt.Fprintf(&b, "  %s %s\n", group.mark, p)
			}
		}
		if diff.Truncated {
			fmt.Fprintf(&b, "  ... more than %d paths differ; the list is truncated\n", checkpointDiffLimit)
		}
		return strings.TrimRight(b.String(), "\n"), nil
	}
	return "Usage: /checkpoint list | inspect <id> | diff <from> <to>", nil
}
