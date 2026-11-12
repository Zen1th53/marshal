package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/api"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// heldElsewhereLine is said when a command needs the background service but
// another Marshal window (the TUI) holds the project: that window is the
// runtime, so the user is pointed at it, never told to start a daemon.
const heldElsewhereLine = "Marshal is open for this project in another window; use that window for this. This command only works through Marshal's background service, which isn't running."

// localView is what `marshal status` and `marshal tasks` can read straight
// from the project's store when no background service answers: the same
// store the Marshal window reads and writes.
type localView struct {
	status app.Status
	tasks  []model.Task
	// held is set when another Marshal window holds the project.
	held bool
	// run is the latest Marshal run, when there is one.
	run    marshal.Run
	hasRun bool
}

// readLocal opens the project read side directly. It is only used after the
// background service did not answer; it starts nothing and changes no work.
func (c command) readLocal(ctx context.Context) (localView, error) {
	runtime, err := app.Open(ctx, c.root)
	if err != nil {
		return localView{}, err
	}
	defer runtime.Close()
	view := localView{held: !runtime.IsLifecycleOwner()}
	if view.status, err = runtime.Status(ctx); err != nil {
		return localView{}, err
	}
	if view.tasks, err = runtime.Tasks(ctx); err != nil {
		return localView{}, err
	}
	_, record, err := runtime.Store().LatestMarshalRun(ctx, view.status.Project.ID)
	switch {
	case err == nil:
		view.run, view.hasRun = record.Value, true
	case !errors.Is(err, model.ErrNotFound):
		return localView{}, err
	}
	return view, nil
}

// notRunning reports whether err is the background service not answering.
func notRunning(err error) bool { return errors.Is(err, api.ErrRuntimeNotRunning) }

// localHeader is the plain lines that say where the figures came from and
// what Marshal is doing, for the human form of status and tasks.
func (v localView) header() []string {
	var lines []string
	if v.held {
		lines = append(lines, "Marshal is open for this project in another window; this is what it has saved so far.")
	}
	if v.hasRun {
		lines = append(lines, "Marshal: "+runStateWords(v.run.State)+" · "+taskTally(v.run.Tasks))
	}
	return lines
}

// runStateWords is a run's state in the Board's words (board_stop.go's
// runWord), so `marshal status` and the Board say the same thing.
func runStateWords(state marshal.RunState) string {
	switch state {
	case marshal.Drafting:
		return "planning"
	case marshal.Approved, marshal.Dispatching:
		return "running"
	case marshal.Reviewing:
		return "reviewing"
	case marshal.Merging:
		return "merging"
	case marshal.Verifying:
		return "final check"
	case marshal.Closed:
		return "done"
	case marshal.AwaitingUser:
		return "waiting for you"
	}
	return "running"
}

func taskStateWords(state marshal.TaskState) string {
	switch state {
	case marshal.Queued:
		return "waiting"
	case marshal.Dispatched:
		return "in progress"
	case marshal.HandedIn:
		return "being reviewed"
	case marshal.Accepted:
		return "accepted"
	case marshal.Returned:
		return "sent back"
	case marshal.Reassigned:
		return "reassigned"
	case marshal.Escalated:
		return "needs you"
	case marshal.Merged:
		return "done"
	case marshal.Cancelled:
		return "cancelled"
	}
	return "waiting"
}

func taskTally(tasks []marshal.Task) string {
	done := 0
	for _, t := range tasks {
		if t.State == marshal.Merged {
			done++
		}
	}
	noun := "tasks"
	if len(tasks) == 1 {
		noun = "task"
	}
	return fmt.Sprintf("%d %s, %d done", len(tasks), noun, done)
}

// statusLocal is `marshal status` without a background service.
func (c command) statusLocal(ctx context.Context) error {
	view, err := c.readLocal(ctx)
	if err != nil {
		return err
	}
	return c.print(view.status, view.statusText())
}

// tasksLocal is `marshal tasks` without a background service: the imported
// tasks, and the Marshal run's own tasks as the Board shows them.
func (c command) tasksLocal(ctx context.Context) error {
	view, err := c.readLocal(ctx)
	if err != nil {
		return err
	}
	lines := view.header()
	if view.hasRun {
		for _, t := range view.run.Tasks {
			title := strings.TrimSpace(t.Title)
			if title == "" {
				title = "(untitled task)"
			}
			lines = append(lines, "  "+title+" — "+taskStateWords(t.State))
		}
	}
	if view.hasRun {
		lines = append(lines, fmt.Sprintf("Other tasks: %d", len(view.tasks)))
	} else {
		lines = append(lines, fmt.Sprintf("%d tasks", len(view.tasks)))
	}
	return c.print(view.tasks, strings.Join(lines, "\n"))
}

// statusText is `marshal status` in plain words, as the Board says them:
// the project, the run's state and tasks done, the Marshal provider and
// whether anonymous telemetry is on.
func (v localView) statusText() string {
	var lines []string
	if v.held {
		lines = append(lines, "Marshal is open for this project in another window; this is what it has saved so far.")
	}
	root := v.status.Project.Repository
	project := filepath.Base(root)
	if branch := v.status.Project.DefaultBranch; branch != "" {
		project += " (branch " + branch + ")"
	}
	lines = append(lines, "Project: "+project)
	if v.hasRun {
		done := 0
		for _, t := range v.run.Tasks {
			if t.State == marshal.Merged {
				done++
			}
		}
		lines = append(lines, fmt.Sprintf("Run: %s · %d of %d tasks done", runStateWords(v.run.State), done, len(v.run.Tasks)))
	} else {
		lines = append(lines, "Run: none yet. Run marshal here and tell the Marshal what to build.")
	}
	provider := app.MarshalProviderName(root)
	if provider == "" {
		provider = "not chosen yet (Marshal asks when it opens)"
	}
	lines = append(lines, "Marshal provider: "+provider)
	lines = append(lines, app.TelemetryLine(root))
	return strings.Join(lines, "\n")
}

// statusOnlyText is the plain status when only the background service's
// counts could be read.
func statusOnlyText(value app.Status) string {
	return localView{status: value}.statusText()
}

// heldByWindow reports whether another Marshal window holds the project, for
// the plain line a daemon-only command prints when the service is not there.
func heldByWindow(root string) bool { return app.ProjectHeldElsewhere(root) }
