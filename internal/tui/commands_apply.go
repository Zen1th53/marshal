package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/hostgit"
)

// applyCodexCloudTask applies a Codex task diff to the project through the
// native `codex apply`, which takes Codex's own task ID. MARSHAL never infers
// that ID from its own runs: governed work is delivered through /diff and
// Marshal acceptance instead. The project is snapshotted first, and success is
// reported from the files that actually changed, not from the exit status.
func (h *CommandHandler) applyCodexCloudTask(ctx context.Context, args []string) (string, error) {
	const governed = "Governed MARSHAL work is reviewed with /diff and delivered with /marshal accept, not /apply."
	if len(args) != 1 {
		return "Usage: /apply <codex_task_id>  (a Codex task ID from Codex itself; MARSHAL does not guess one)\n" + governed, nil
	}
	id := args[0]
	if strings.HasPrefix(strings.ToUpper(id), "TASK-") {
		return fmt.Sprintf("Apply was NOT performed: %s is a MARSHAL task ID, not a Codex task ID. %s", id, governed), nil
	}
	a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.runtime == nil {
		return "Apply was NOT performed: no runtime is attached to snapshot the project first.", nil
	}
	root := a.runtime.ProjectRoot()
	before, err := gitChanges(ctx, root)
	if err != nil {
		return fmt.Sprintf("Apply was NOT performed: the project's changes could not be read first: %v", err), nil
	}
	cp, err := a.runtime.Execution().Engine().CaptureOperatorCheckpoint(ctx, "automatic snapshot before /apply "+id)
	if err != nil {
		return fmt.Sprintf("Apply was NOT performed: could not snapshot the project first: %v", err), nil
	}
	out, applyErr := runGovernedCodexCmd(ctx, []string{"apply", id})
	after, err := gitChanges(ctx, root)
	if err != nil {
		return fmt.Sprintf("Codex apply ran, but the result could not be read back: %v. Undo with /rollback %s.", err, cp.CheckpointID), nil
	}
	var changed []string
	for path, state := range after {
		if before[path] != state {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	if applyErr != nil {
		note := "No project file changed."
		if len(changed) > 0 {
			note = fmt.Sprintf("%d file(s) changed anyway: %s. Undo with /rollback %s.", len(changed), strings.Join(changed, ", "), cp.CheckpointID)
		}
		return fmt.Sprintf("Codex apply failed: %v\n%s", applyErr, note), nil
	}
	if len(changed) == 0 {
		return fmt.Sprintf("Codex apply exited successfully, but no project file changed:\n%s", RedactContent(out, nil)), nil
	}
	return fmt.Sprintf("Codex task %s applied; %d file(s) changed: %s\nSnapshot before applying: /rollback %s",
		id, len(changed), strings.Join(changed, ", "), cp.CheckpointID), nil
}

// gitChanges maps each changed path in the project to its porcelain status
// plus a content stamp, so a second change to an already-modified file is
// still seen.
func gitChanges(ctx context.Context, root string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd, err := hostgit.Command(ctx, root, "status", "--porcelain=v1", "--untracked-files=all", "-z")
	if err != nil {
		return nil, err
	}
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	changes := map[string]string{}
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		status, path := entry[:2], entry[3:]
		if status[0] == 'R' || status[0] == 'C' {
			i++
		}
		if strings.HasPrefix(path, ".marshal/") {
			continue
		}
		stamp := status
		hash, err := hostgit.Command(ctx, root, "hash-object", "--", path)
		if err != nil {
			return nil, err
		}
		hash.Dir = root
		if digest, err := hash.Output(); err == nil {
			stamp += ":" + strings.TrimSpace(string(digest))
		}
		changes[path] = stamp
	}
	return changes, nil
}
