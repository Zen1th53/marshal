package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// launchHostProcesses observes this host's worker processes. Tests replace
// it to model a platform with or without held launches.
var launchHostProcesses = driver.HostProcesses

// launchSupervisionState holds the runtime's one launch supervisor. Its
// per-key launch locks are in memory, so every Marshal service the runtime
// builds shares it.
type launchSupervisionState struct {
	once       sync.Once
	supervisor *driver.LaunchSupervisor
}

// marshalLaunchSupervisor returns the runtime's launch supervisor, or nil
// where this host cannot hold a worker before it starts. Without one, Marshal
// keeps launching workers directly and dispatches in batches.
func (r *Runtime) marshalLaunchSupervisor() *driver.LaunchSupervisor {
	state := &r.launchSupervision
	state.once.Do(func() {
		state.supervisor = newMarshalLaunchSupervisor(r.layout.RuntimeDir, launchHostProcesses())
	})
	return state.supervisor
}

func newMarshalLaunchSupervisor(stateDir string, processes driver.ProcessAdapter) *driver.LaunchSupervisor {
	if stateDir == "" || !filepath.IsAbs(stateDir) || processes == nil {
		slog.Debug("marshal: launching workers without launch supervision", "reason", "no project state directory")
		return nil
	}
	if _, err := processes.BootID(); err != nil {
		slog.Debug("marshal: launching workers without launch supervision", "reason", err.Error())
		return nil
	}
	// Receipts live in the project state directory, never in a task worktree,
	// so a worker cannot rewrite the record of its own launch.
	return &driver.LaunchSupervisor{Journal: driver.FileLaunchJournal{Dir: filepath.Join(stateDir, "launches")}, Processes: processes}
}

// supervisesLaunch reports whether d's launch can be held behind a durable
// receipt. Other drivers, such as governed workers, launch directly.
func (s *MarshalService) supervisesLaunch(d driver.Driver) bool {
	if s.LaunchSupervisor == nil {
		return false
	}
	_, ok := d.(driver.SupervisedLauncher)
	return ok
}

// verifyMarshalWorkerExit reports a worker as still running while this
// runtime tracks a governed attempt for the task. Supervised launches are
// proved from their receipts before this is asked.
func (r *Runtime) verifyMarshalWorkerExit(_ context.Context, runID, taskID string) (bool, error) {
	for _, beat := range r.WorkerBeats(runID) {
		if beat.TaskID == taskID {
			return false, nil
		}
	}
	return true, nil
}
