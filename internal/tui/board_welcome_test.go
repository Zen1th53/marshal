package tui

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// reviewBoard is three finished tasks and one task in review whose scope is
// styles.css, the fixture the acceptance criteria name.
func reviewBoard() boardmodel.Board {
	return boardmodel.Board{
		Run: boardmodel.RunHead{ID: "RUN-deadbeef", Done: 3, Total: 4},
		Tasks: []boardmodel.TaskRow{
			{ID: "t1", Title: "first", State: boardmodel.TaskMerged},
			{ID: "t2", Title: "second", State: boardmodel.TaskMerged},
			{ID: "t3", Title: "third", State: boardmodel.TaskMerged},
			{ID: "t4", Title: "style the page", Scope: []string{"styles.css"}, State: boardmodel.TaskReview},
		},
	}
}

func TestWelcomeLineReview(t *testing.T) {
	got := welcomeLine(reviewBoard(), nil)
	want := "Welcome back. 3 of 4 tasks are done; styles.css is under review."
	if got != want {
		t.Fatalf("welcomeLine = %q, want %q", got, want)
	}
}

func TestWelcomeLineSinceYouLeft(t *testing.T) {
	seen := &boardSeen{Run: "RUN-deadbeef", Done: 1}
	got := welcomeLine(reviewBoard(), seen)
	want := "Welcome back. 2 more tasks finished since you left; 3 of 4 tasks are done; styles.css is under review."
	if got != want {
		t.Fatalf("welcomeLine = %q, want %q", got, want)
	}
}

// A seen record for a different run, or one whose Done did not grow, drops the
// "since you left" clause.
func TestWelcomeLineSinceYouLeftOnlyForSameRun(t *testing.T) {
	for _, seen := range []*boardSeen{
		{Run: "RUN-other", Done: 1},
		{Run: "RUN-deadbeef", Done: 3},
		{Run: "RUN-deadbeef", Done: 5},
	} {
		got := welcomeLine(reviewBoard(), seen)
		want := "Welcome back. 3 of 4 tasks are done; styles.css is under review."
		if got != want {
			t.Fatalf("welcomeLine(%+v) = %q, want %q", seen, got, want)
		}
	}
}

// A parked task is named before any other clause, even when a task is also in
// review.
func TestWelcomeLineParkedFirst(t *testing.T) {
	b := boardmodel.Board{
		Run: boardmodel.RunHead{ID: "RUN-1", Done: 1, Total: 3},
		Tasks: []boardmodel.TaskRow{
			{ID: "t1", Title: "done", State: boardmodel.TaskMerged},
			{ID: "t2", Title: "wire the API", State: boardmodel.TaskParked},
			{ID: "t3", Title: "page", Scope: []string{"index.html"}, State: boardmodel.TaskReview},
		},
	}
	got := welcomeLine(b, nil)
	want := "Welcome back. 1 of 3 tasks are done; wire the API needs you."
	if got != want {
		t.Fatalf("welcomeLine = %q, want %q", got, want)
	}
}

// working and paused are the later clauses when nothing is parked or in review.
func TestWelcomeLineWorkingAndPaused(t *testing.T) {
	working := boardmodel.Board{
		Run: boardmodel.RunHead{ID: "RUN-w", Done: 0, Total: 2},
		Tasks: []boardmodel.TaskRow{
			{ID: "t1", State: boardmodel.TaskWorking},
			{ID: "t2", State: boardmodel.TaskWorking},
		},
	}
	if got, want := welcomeLine(working, nil), "Welcome back. 0 of 2 tasks are done; 2 working."; got != want {
		t.Fatalf("working welcomeLine = %q, want %q", got, want)
	}
	paused := boardmodel.Board{
		Run: boardmodel.RunHead{ID: "RUN-p", Done: 1, Total: 1, State: marshal.AwaitingUser},
		Tasks: []boardmodel.TaskRow{
			{ID: "t1", State: boardmodel.TaskMerged},
		},
	}
	if got, want := welcomeLine(paused, nil), "Welcome back. 1 of 1 task is done; the run is paused and waits for you."; got != want {
		t.Fatalf("paused welcomeLine = %q, want %q", got, want)
	}
}

// The welcome line never carries a run, plan or instance id, nor a bare hex
// digest, whatever the Board holds.
func TestWelcomeLineNoIDs(t *testing.T) {
	seen := &boardSeen{Run: "RUN-deadbeef", Done: 1}
	line := welcomeLine(reviewBoard(), seen)
	for _, re := range []string{`RUN-`, `PLAN-`, `INSTANCE-`, `[0-9a-f]{7,}`} {
		if regexp.MustCompile(re).MatchString(line) {
			t.Fatalf("welcome line %q matched forbidden pattern %q", line, re)
		}
	}
}

// Titles and scope paths are owner and model text: control characters are
// cleaned, and a name carrying an internal id, a hex digest or a secret is
// replaced, never shown.
func TestWelcomeLineCleansTaskNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		task boardmodel.TaskRow
		want string
	}{
		{"control chars", boardmodel.TaskRow{Title: "fix\x07 the\tlogin", State: boardmodel.TaskParked}, "; fix the login needs you"},
		{"run id title", boardmodel.TaskRow{Title: "redo RUN-1794393904851065755", State: boardmodel.TaskParked}, "; a task needs you"},
		{"plan id scope falls back to title", boardmodel.TaskRow{Title: "page", Scope: []string{"PLAN-abc/notes.md"}, State: boardmodel.TaskReview}, "; page is under review"},
		{"hex digest", boardmodel.TaskRow{Title: "revert 017b2d0f", State: boardmodel.TaskParked}, "; a task needs you"},
		{"secret", boardmodel.TaskRow{Title: "use ghp_" + strings.Repeat("A1b2C3d4E5", 4), State: boardmodel.TaskParked}, "; a task needs you"},
		{"plain hex word stays", boardmodel.TaskRow{Title: "defaced banner", State: boardmodel.TaskParked}, "; defaced banner needs you"},
	} {
		b := boardmodel.Board{Run: boardmodel.RunHead{ID: "RUN-1", Total: 1}, Tasks: []boardmodel.TaskRow{tc.task}}
		got := welcomeLine(b, nil)
		want := "Welcome back. 0 of 1 task is done" + tc.want + "."
		if got != want {
			t.Errorf("%s: welcomeLine = %q, want %q", tc.name, got, want)
		}
		for _, re := range []string{`RUN-`, `PLAN-`, `INSTANCE-`, `[\x00-\x1f]`, `ghp_`} {
			if regexp.MustCompile(re).MatchString(got) {
				t.Errorf("%s: %q matched %q", tc.name, got, re)
			}
		}
		// A digest is seven or more hex characters with at least one digit.
		for _, m := range regexp.MustCompile(`[0-9a-f]{7,}`).FindAllString(got, -1) {
			if strings.ContainsAny(m, "0123456789") {
				t.Errorf("%s: %q shows a hex digest %q", tc.name, got, m)
			}
		}
	}
}

// Close saves the Board as it stands after every Board-updating worker has
// stopped, not the snapshot from before shutdown began.
func TestCloseSavesFinalBoardSeen(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	w.workDir = t.TempDir()
	run := marshal.Run{State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "a", State: marshal.Merged}, {PlanTaskID: "b", State: marshal.Dispatched}}}
	w.setMarshalPanel(newMarshalPanel("RUN-close", "codex", run, ""))
	final := run
	final.Tasks = []marshal.Task{{PlanTaskID: "a", State: marshal.Merged}, {PlanTaskID: "b", State: marshal.Merged}}
	if !w.startBackground(context.Background(), func(ctx context.Context) {
		<-ctx.Done()
		w.setMarshalPanel(newMarshalPanel("RUN-close", "codex", final, ""))
	}) {
		t.Fatal("worker not started")
	}
	w.Close()
	got := loadBoardSeen(w.workDir)
	if got == nil || got.Run != "RUN-close" || got.Done != 2 {
		t.Fatalf("board-seen after Close = %+v, want run RUN-close done 2", got)
	}
}

func TestBoardSeenRoundTrip(t *testing.T) {
	root := t.TempDir()
	b := boardmodel.Board{Run: boardmodel.RunHead{ID: "RUN-42", Done: 2, Total: 5}}
	if err := writeBoardSeen(root, b); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, statepath.DirName, "board-seen.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Fatalf("board-seen.json mode = %o, want 0600", mode)
	}
	got := loadBoardSeen(root)
	if got == nil {
		t.Fatal("loadBoardSeen returned nil")
	}
	if got.Run != "RUN-42" || got.Done != 2 {
		t.Fatalf("loadBoardSeen = %+v, want run RUN-42 done 2", got)
	}
	if got.At == "" {
		t.Fatalf("loadBoardSeen lost the timestamp: %+v", got)
	}
}

// A missing or unreadable seen file is not an error: loadBoardSeen returns nil
// so the welcome line simply omits the "since you left" clause.
func TestBoardSeenMissingIsNil(t *testing.T) {
	if got := loadBoardSeen(t.TempDir()); got != nil {
		t.Fatalf("loadBoardSeen on missing file = %+v, want nil", got)
	}
}
