package tui

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
)

// erroringPlanSource always fails its read with the same error.
type erroringPlanSource struct{ err error }

func (s erroringPlanSource) PlanOverview(context.Context, string) (store.PlanOverview, error) {
	return store.PlanOverview{}, s.err
}

// TestViewReadErrorLoggedOnce: two refreshes that fail with the same error
// write exactly one line to the project's error log.
func TestViewReadErrorLoggedOnce(t *testing.T) {
	tmp := t.TempDir()
	w := NewWorkspace(nil, "project", "session")
	w.workDir = tmp
	w.state.Marshal = &MarshalPanel{RunID: "run"}
	w.planSource = erroringPlanSource{err: errors.New("open /x/state.db: no such file")}

	w.refreshPlan(t.Context())
	w.refreshPlan(t.Context())

	data, err := os.ReadFile(statepath.Path(tmp, "logs", "tui-errors.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("log has %d lines, want 1: %q", len(lines), string(data))
	}
	if !strings.Contains(lines[0], "Plan view") {
		t.Fatalf("log line %q does not name the view", lines[0])
	}
}
