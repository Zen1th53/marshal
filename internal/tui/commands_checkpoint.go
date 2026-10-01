package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/google/uuid"
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
		records, err := service.Engine().ListCheckpoints()
		if err != nil {
			return "", fmt.Errorf("list checkpoints: %w", err)
		}
		if len(records) == 0 {
			return "No execution snapshots in this project.", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "EXECUTION SNAPSHOTS (%d in this project, newest first; project files, excluding .git and .marshal):\n", len(records))
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

// handleCheckpointCreate snapshots the project's files as the local owner.
func (h *CommandHandler) handleCheckpointCreate(ctx context.Context, reason string) (string, error) {
	a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.runtime == nil || a.localControl == nil {
		return "Checkpoint creation is unavailable in TUI: authenticated runtime authorization is required.", nil
	}
	e := app.CommandEnvelope{ProjectID: a.runtime.ProjectIdentity(), SessionID: a.sessionID, TargetID: "checkpoint:new", IdempotencyKey: uuid.NewString()}
	cp, err := a.runtime.CommandCaptureCheckpoint(a.localControl.Context(ctx), e, reason)
	if err != nil {
		return fmt.Sprintf("Checkpoint was NOT created: %v", err), nil
	}
	return fmt.Sprintf("Snapshot %s captured (digest %s). Restore it with /rollback %s.", cp.CheckpointID, cp.SnapshotDigest[:12], cp.CheckpointID), nil
}

// handleRollback previews a restore, or performs it once the operator
// confirms the snapshot's digest. Restoring rewrites project files, so the
// preview says exactly what would change and nothing happens without the
// confirmation.
func (h *CommandHandler) handleRollback(ctx context.Context, args []string) (string, error) {
	id := strings.TrimPrefix(args[0], "#")
	a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.runtime == nil || a.localControl == nil {
		return fmt.Sprintf("Rollback to %s was NOT performed: authenticated runtime authorization is required.", id), nil
	}
	engine := a.runtime.Execution().Engine()
	check, diff, err := engine.PreviewRestore(ctx, id)
	if err != nil {
		return fmt.Sprintf("Rollback to %s was NOT performed: %v", id, err), nil
	}
	if check.State != execution.SnapshotIntact {
		return fmt.Sprintf("Rollback to %s was NOT performed: the snapshot is %s.", id, check.State), nil
	}
	if len(args) == 1 {
		var b strings.Builder
		fmt.Fprintf(&b, "ROLLBACK PREVIEW for %s (nothing has changed yet):\n", id)
		fmt.Fprintf(&b, "  Restores project files (not .git or .marshal) to %s, captured %s.\n", id, check.Record.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Fprintf(&b, "  %d files would be restored, %d deleted, %d overwritten:\n", len(diff.Added), len(diff.Removed), len(diff.Changed))
		for _, group := range []struct {
			mark  string
			paths []string
		}{{"restore", diff.Added}, {"delete", diff.Removed}, {"overwrite", diff.Changed}} {
			for _, p := range group.paths {
				fmt.Fprintf(&b, "    %s %s\n", group.mark, p)
			}
		}
		if diff.Truncated {
			b.WriteString("    ... more than 200 paths differ\n")
		}
		fmt.Fprintf(&b, "  A recovery checkpoint of the current state is captured first, and no run may be active.\n")
		fmt.Fprintf(&b, "  To restore: /rollback %s confirm %s", id, check.Record.SnapshotDigest[:12])
		return b.String(), nil
	}
	confirmed := args[2]
	if len(confirmed) < 12 || !strings.HasPrefix(check.Record.SnapshotDigest, confirmed) {
		return fmt.Sprintf("Rollback to %s was NOT performed: %q does not match the snapshot's digest; run /rollback %s to see it again.", id, confirmed, id), nil
	}
	e := app.CommandEnvelope{ProjectID: a.runtime.ProjectIdentity(), SessionID: a.sessionID, TargetID: "checkpoint:" + id, IdempotencyKey: uuid.NewString()}
	result, err := a.runtime.CommandRestoreCheckpoint(a.localControl.Context(ctx), e, check.Record.SnapshotDigest)
	if err != nil {
		recovery := ""
		if result.Recovery.CheckpointID != "" {
			recovery = fmt.Sprintf(" A recovery point was captured: /rollback %s.", result.Recovery.CheckpointID)
		}
		return fmt.Sprintf("Rollback to %s was NOT completed: %v.%s", id, err, recovery), nil
	}
	return fmt.Sprintf("Rolled back to %s; every restored file was re-read and matches the snapshot. To undo: /rollback %s (recovery point).",
		id, result.Recovery.CheckpointID), nil
}
