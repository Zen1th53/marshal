package tui

import (
	"context"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/Zen1th53/marshal/internal/doctor"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// launchDoctor is the project check the launch gate runs. It is a package
// variable so a test can replace it without a real project on disk.
var launchDoctor = doctor.Check

// launchPhase is where the per-start project check stands.
type launchPhase int

const (
	// launchChecking is the starting phase: the check is running and the
	// Board paints its normal body while it does.
	launchChecking launchPhase = iota
	// launchReady means the check finished with no remaining problem: the
	// Marshal chat may start.
	launchReady
	// launchBlocked means a problem the Marshal cannot fix safely remains:
	// the Board shows the Blocked screen and the chat never starts.
	launchBlocked
)

// launchState is the shared result of the per-start project check.
type launchState struct {
	mu       sync.Mutex
	phase    launchPhase
	blockers []string
	warnings []string
	started  time.Time
	scan     projectScan
	// pending holds afterLaunchCheck callbacks waiting for the check to
	// finish ready; they run once then, and a later retry that turns ready
	// runs any registered since.
	pending []func()
}

// runLaunchCheck runs the doctor once (twice when a safe fix applies), decides
// whether the start is ready, blocked, or ready with warnings, and releases the
// Marshal chat only when it is ready. It is meant to run in the background: it
// does not block the first paint.
func (w *Workspace) runLaunchCheck(ctx context.Context) {
	w.launch.mu.Lock()
	w.launch.phase = launchChecking
	w.launch.started = time.Now()
	w.launch.blockers, w.launch.warnings = nil, nil
	w.launch.mu.Unlock()
	w.requestRepaint()

	root := w.providerRoot()
	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	// The project scan runs beside the doctor and stores itself on the
	// Workspace; the check is ready only once it has finished.
	var scanWG sync.WaitGroup
	scanWG.Add(1)
	go func() {
		defer scanWG.Done()
		w.runLaunchScan(checkCtx, root)
	}()

	layout, err := project.Discover(root)
	if err != nil {
		// Discovery failing is the repository check failing: the project is
		// not a usable Git repository, so there is nothing else to report.
		scanWG.Wait()
		w.finishLaunchCheck([]string{blockLine("repository", "", project.Layout{})}, nil)
		return
	}

	report := launchDoctor(checkCtx, root, doctor.Options{})
	if fixed := launchSafeFix(layout, report); len(fixed) > 0 {
		report = launchDoctor(checkCtx, root, doctor.Options{})
		w.RecordActivity("Fixed: " + strings.Join(fixed, ", ") + ".")
	}

	if checkCtx.Err() == context.DeadlineExceeded {
		scanWG.Wait()
		w.finishLaunchCheck([]string{"I can't fix this safely: the project check did not finish in 20 seconds. Run marshal doctor, then press r."}, nil)
		return
	}

	blockers, warnings := launchLines(report, layout)
	for _, warning := range warnings {
		w.RecordActivity(warning)
	}
	scanWG.Wait()
	w.finishLaunchCheck(blockers, warnings)
}

// finishLaunchCheck records the result, moves to the ready or blocked phase,
// repaints, and runs the pending chat-start callbacks when ready. When ready it
// first decides the Marshal picker (protocol v2 step 1): if the picker opens,
// the callbacks are held until confirmPicker releases them.
func (w *Workspace) finishLaunchCheck(blockers, warnings []string) {
	w.launch.mu.Lock()
	w.launch.blockers = blockers
	w.launch.warnings = warnings
	ready := len(blockers) == 0
	if ready {
		w.launch.phase = launchReady
	} else {
		w.launch.phase = launchBlocked
	}
	w.launch.mu.Unlock()
	w.requestRepaint()
	if !ready {
		return
	}
	if w.decideMarshalPicker(context.Background()) {
		// The picker holds the chat start; confirmPicker releases it.
		return
	}
	w.releaseLaunchPending()
}

// releaseLaunchPending runs and clears the queued chat-start callbacks. It is
// called once the start is settled: the check turned ready with no picker, or
// the picker was answered.
func (w *Workspace) releaseLaunchPending() {
	w.launch.mu.Lock()
	ready := w.launch.pending
	w.launch.pending = nil
	w.launch.mu.Unlock()
	for _, fn := range ready {
		fn()
	}
}

// retryLaunchCheck re-runs the check after the owner fixed what blocked it.
func (w *Workspace) retryLaunchCheck() {
	w.launch.mu.Lock()
	w.launch.phase = launchChecking
	w.launch.mu.Unlock()
	w.requestRepaint()
	w.startBackground(context.Background(), w.runLaunchCheck)
}

// afterLaunchCheck runs fn once the check is ready: immediately if it already
// is, queued otherwise, and never while the start is blocked. A later retry
// that turns ready runs any callback queued since.
func (w *Workspace) afterLaunchCheck(fn func()) {
	if fn == nil {
		return
	}
	w.launch.mu.Lock()
	if w.launch.phase == launchReady && !w.pickerOpen() {
		w.launch.mu.Unlock()
		fn()
		return
	}
	w.launch.pending = append(w.launch.pending, fn)
	w.launch.mu.Unlock()
}

// launchWarnings is the warning lines from the last check, for the PROJECT SCAN
// card (P2-14) and Activity.
func (w *Workspace) launchWarnings() []string {
	w.launch.mu.Lock()
	defer w.launch.mu.Unlock()
	return append([]string(nil), w.launch.warnings...)
}

// launchBlocked reports whether the Board must show the Blocked screen.
func (w *Workspace) launchBlocked() bool {
	w.launch.mu.Lock()
	defer w.launch.mu.Unlock()
	return w.launch.phase == launchBlocked
}

// launchBlockers is the current Blocked-screen lines.
func (w *Workspace) launchBlockers() []string {
	w.launch.mu.Lock()
	defer w.launch.mu.Unlock()
	return append([]string(nil), w.launch.blockers...)
}

// launchSafeFix applies the only two safe fixes the launch gate is allowed to
// make: tightening the runtime and artifact directories to 0700 when the doctor
// reports them FAIL. Each is applied only to a real directory the current user
// owns, never a symlink or a directory owned by someone else. It returns a
// plain-English line per fix made, for Activity.
func launchSafeFix(layout project.Layout, report doctor.Report) (fixed []string) {
	tighten := func(name, dir, note string) {
		result := report.Check(name)
		if result == nil || result.Verdict != doctor.Fail {
			return
		}
		if !ownedRealDir(dir) {
			return
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return
		}
		fixed = append(fixed, note)
	}
	tighten("permissions", layout.RuntimeDir, "made the .marshal folder private to you")
	tighten("artifacts", layout.Artifacts, "made the artifacts folder private to you")
	return fixed
}

// ownedRealDir reports whether path is a real directory (not a symlink) owned
// by the current user, the only thing the launch gate will chmod.
func ownedRealDir(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

// launchLines turns a doctor report into the Blocked-screen lines and the
// warning lines. When git or the repository itself FAILs, only those lines are
// shown, because every later check is a consequence of them.
func launchLines(report doctor.Report, layout project.Layout) (blockers, warnings []string) {
	gitFail := failed(report, "git")
	repoFail := failed(report, "repository")
	if gitFail || repoFail {
		if gitFail {
			blockers = append(blockers, blockLine("git", "", layout))
		}
		if repoFail {
			blockers = append(blockers, blockLine("repository", "", layout))
		}
		return blockers, nil
	}
	for _, result := range report.Results {
		switch result.Verdict {
		case doctor.Fail:
			blockers = append(blockers, blockLine(result.Name, result.Detail, layout))
		case doctor.Degraded:
			warnings = append(warnings, warnLine(result))
		}
	}
	return blockers, warnings
}

func failed(report doctor.Report, name string) bool {
	result := report.Check(name)
	return result != nil && result.Verdict == doctor.Fail
}

// blockLine is the one Blocked-screen line for a FAILed check: what went wrong
// in plain words and the exact command that fixes it. A check not in the table
// falls back to its own detail, so a new doctor check never disappears.
func blockLine(name, detail string, layout project.Layout) string {
	const prefix = "I can't fix this safely: "
	switch name {
	case "sqlite", "security_subsystems":
		// A corrupt or unreadable state is not something a command restores;
		// the owner is asked only to capture it for a bug report, untouched.
		return prefix + "this project's MARSHAL state can't be read. Nothing was changed. Run marshal doctor --json > marshal-doctor.json and send it with a bug report, then press r."
	}
	if what, command, ok := launchTable(name, layout); ok {
		return prefix + what + ". Run " + command + ", then press r."
	}
	return prefix + detail + ". Run marshal doctor, then press r."
}

// launchTable maps a FAILing doctor check to its plain-English cause and the
// exact command that fixes it.
func launchTable(name string, layout project.Layout) (what, command string, ok bool) {
	switch name {
	case "git":
		return "Git is missing or not working", tmux.InstallCommandFor("git"), true
	case "repository":
		return "this folder is not a Git repository with a first commit", `git init && git add -A && git commit -m "first commit"`, true
	case "pack", "runtime_version", "policy":
		// marshal setup reports an initialized project as ready and will not
		// rewrite these files, but marshal init regenerates all of them.
		return "MARSHAL's project files are missing or damaged", "marshal init", true
	case "permissions":
		return "the .marshal folder must be private to you", "chmod 700 " + layout.RuntimeDir, true
	case "artifacts":
		return "the artifacts folder must be private to you", "chmod 700 " + layout.Artifacts, true
	case "socket":
		// marshal has no `daemon stop` subcommand, so the fix is removing the
		// leftover socket directly.
		return "a leftover MARSHAL socket has unsafe permissions", "rm " + layout.Socket, true
	case "worktree":
		return "Git worktrees don't work in this repository", "git worktree prune", true
	}
	return "", "", false
}

// warnLine is the one warning line for a DEGRADED check: the bubblewrap case
// names the install command; any other reuses the check's own detail with its
// first letter upper-cased and a full stop, and no check name in front.
func warnLine(result doctor.Result) string {
	if result.Name == "bwrap" {
		return "Workers run without isolation: bubblewrap is missing. Install it with " + tmux.InstallCommandFor("bubblewrap") + "."
	}
	detail := strings.TrimSpace(result.Detail)
	if detail == "" {
		return detail
	}
	runes := []rune(detail)
	runes[0] = unicode.ToUpper(runes[0])
	line := strings.TrimRight(string(runes), ".")
	return line + "."
}
