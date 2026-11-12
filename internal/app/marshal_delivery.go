package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// DeliveryStopError is a close that cannot move the target branch for a
// reason the person can fix: Reason says what is in the way and Next what to
// do, both in plain words. The run is left exactly as it was (verified, not
// closed), so pressing Enter tries the delivery again.
type DeliveryStopError struct {
	Reason string
	Next   string
	Cause  error
}

func (e *DeliveryStopError) Error() string {
	text := strings.TrimSpace(e.Reason + " " + e.Next)
	if e.Cause != nil {
		text += " (" + e.Cause.Error() + ")"
	}
	return text
}

func (e *DeliveryStopError) Unwrap() error { return e.Cause }

// StopReason is the stop as the Board shows it.
func (e *DeliveryStopError) StopReason() (string, string) { return e.Reason, e.Next }

// maxDirtyNames bounds the file names a delivery stop lists.
const maxDirtyNames = 6

// deliveryCheckout is where the target branch is checked out ("" when no
// worktree has it), once it is safe for Git to fast-forward that checkout: the
// main worktree, on the target at the approved base, with no uncommitted
// source change. Anything else is a DeliveryStopError that touches nothing:
// the person's uncommitted work is never stashed, overwritten or merged into.
func (s *MarshalService) deliveryCheckout(ctx context.Context, branch, target, old string) (string, error) {
	checkedOut, err := marshalTargetWorktree(ctx, s.Repository, target)
	if err != nil || checkedOut == "" {
		return "", err
	}
	// Only the main worktree has the same Git and common directories.
	dirs, err := gitMarshal(ctx, checkedOut, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return "", &DeliveryStopError{Reason: fmt.Sprintf("MARSHAL could not read your checkout of %s.", branch), Next: "Press Enter to try again.", Cause: err}
	}
	if paths := strings.Split(dirs, "\n"); len(paths) != 2 || paths[0] != paths[1] {
		return "", &DeliveryStopError{Reason: fmt.Sprintf("%s is checked out in another working folder, %s.", branch, checkedOut), Next: "Switch that folder to a different branch, then press Enter to finish delivering."}
	}
	status, err := gitMarshalStdout(ctx, checkedOut, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return "", &DeliveryStopError{Reason: fmt.Sprintf("MARSHAL could not read your checkout of %s.", branch), Next: "Press Enter to try again.", Cause: err}
	}
	if changes := hostgit.SourceStatusChanges(status); len(changes) > 0 {
		return "", &DeliveryStopError{
			Reason: fmt.Sprintf("Your checkout of %s has uncommitted changes (%s).", branch, dirtyNames(changes)),
			Next:   "Stash them (git stash -u) or undo them, then press Enter to finish delivering.",
		}
	}
	head, branchErr := gitMarshal(ctx, checkedOut, "symbolic-ref", "HEAD")
	current, currentErr := gitMarshal(ctx, checkedOut, "rev-parse", "HEAD")
	if branchErr != nil || head != target || currentErr != nil || current != old {
		return "", &DeliveryStopError{Reason: fmt.Sprintf("%s changed while the run was finishing.", branch), Next: "Press Enter to try again."}
	}
	return checkedOut, nil
}

// dirtyNames lists the paths of porcelain v1 status lines, at most
// maxDirtyNames of them.
func dirtyNames(lines []string) string {
	var names []string
	for _, line := range lines {
		if len(line) < 4 {
			continue
		}
		name := line[3:]
		if _, renamed, ok := strings.Cut(name, " -> "); ok {
			name = renamed
		}
		names = append(names, strings.TrimSuffix(name, "/"))
	}
	if len(names) == 0 {
		return "files MARSHAL could not name"
	}
	if len(names) > maxDirtyNames {
		return strings.Join(names[:maxDirtyNames], ", ") + fmt.Sprintf(" and %d more", len(names)-maxDirtyNames)
	}
	return strings.Join(names, ", ")
}

// gitMarshalStdout is gitMarshal for output whose leading spaces matter
// (porcelain status): stdout only, trailing newlines trimmed.
func gitMarshalStdout(ctx context.Context, dir string, args ...string) (string, error) {
	cmd, err := hostgit.Command(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

// laneBranch is a Process05 lane's branch: marshal/lane-<id>/<task>.
var laneBranch = regexp.MustCompile(`^refs/heads/` + regexp.QuoteMeta(statepath.RefName) + `/lane-[0-9a-f]+/(.+)$`)

// cleanupDeliveredRun removes what a closed run leaves behind once its work is
// in the target: the run's own worktrees in MARSHAL's state folder and their
// marshal/* branches, but only those that are clean and whose commit the
// target branch already contains. Anything unmerged, dirty, outside the state
// folder or not on a MARSHAL branch is kept; the person's own branches and
// worktrees are never candidates. It is best effort: a failure keeps the
// item and is reported, never undoing the close.
func (s *MarshalService) cleanupDeliveredRun(ctx context.Context, runID string, run marshal.Run, target string) error {
	if run.State != marshal.Closed || target == "" || s.Repository == "" {
		return nil
	}
	ctx = context.WithoutCancel(ctx)
	runBranches := "refs/heads/" + statepath.RefName + "/" + runID + "/"
	results := map[string]bool{}
	tasks := map[string]bool{}
	for _, t := range run.Tasks {
		tasks[t.PlanTaskID] = true
		if t.ResultCommit != "" {
			results[t.ResultCommit] = true
		}
	}
	// A lane branch is this run's when it holds one of the run's results for
	// one of its tasks; a branch named for the run is this run's by its name.
	owned := func(branch, tip string) bool {
		if strings.HasPrefix(branch, runBranches) {
			return true
		}
		m := laneBranch.FindStringSubmatch(branch)
		return m != nil && tasks[m[1]] && results[tip]
	}
	merged := func(tip string) bool {
		_, err := gitMarshal(ctx, s.Repository, "merge-base", "--is-ancestor", tip, target)
		return err == nil
	}
	roots := []string{realDir(s.Worktrees), realDir(filepath.Join(s.Repository, statepath.DirName, "branches"))}
	list, err := gitMarshal(ctx, s.Repository, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	var failures []error
	checkedOut := map[string]bool{}
	for _, entry := range strings.Split(list, "\n\n") {
		var path, head, branch string
		for _, line := range strings.Split(entry, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				path = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "HEAD "):
				head = strings.TrimPrefix(line, "HEAD ")
			case strings.HasPrefix(line, "branch "):
				branch = strings.TrimPrefix(line, "branch ")
			}
		}
		if branch == "" {
			continue
		}
		checkedOut[branch] = true
		inState := false
		for _, root := range roots {
			if root != "" && filepath.Dir(realDir(path)) == root && strings.HasPrefix(filepath.Base(path), "TASK-") {
				inState = true
			}
		}
		if !inState || !owned(branch, head) || !merged(head) {
			continue
		}
		if strings.HasPrefix(branch, runBranches) && !strings.HasPrefix(filepath.Base(path), "TASK-"+runID+"-") {
			continue
		}
		status, err := gitMarshalStdout(ctx, path, "status", "--porcelain=v1", "--untracked-files=all")
		if err != nil || len(hostgit.SourceStatusChanges(status)) > 0 {
			continue
		}
		if _, err := gitMarshal(ctx, s.Repository, "worktree", "remove", "--force", path); err != nil {
			failures = append(failures, err)
			continue
		}
		checkedOut[branch] = false
	}
	refs, err := gitMarshal(ctx, s.Repository, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/"+statepath.RefName+"/")
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	for _, line := range strings.Split(refs, "\n") {
		branch, tip, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || checkedOut[branch] || !owned(branch, tip) || !merged(tip) {
			continue
		}
		// Compare-and-delete: a branch that moved since it was read is kept.
		if _, err := gitMarshal(ctx, s.Repository, "update-ref", "-d", branch, tip); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// realDir is path with symlinks resolved, or path itself when it cannot be.
func realDir(path string) string {
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// deliveryPrecheck is the checkout refusal a close would meet now, before
// the close verifies and records anything.
func (s *MarshalService) deliveryPrecheck(ctx context.Context, branch string) error {
	target := "refs/heads/" + branch
	current, err := gitMarshal(ctx, s.Repository, "rev-parse", target)
	if err != nil {
		return err
	}
	_, err = s.deliveryCheckout(ctx, branch, target, current)
	return err
}
