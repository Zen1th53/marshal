package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// The run's close is an Activity line with its subject and where it went,
// in the colour of finished work: never a red bare "changed ·".
func TestActivityRunCloseNamesItsSubject(t *testing.T) {
	at := time.Date(2026, 11, 8, 6, 1, 34, 0, time.UTC)
	evs := []events.Event{
		{Sequence: 24, RunID: "R", Type: events.EventTypeMarshalPlanApproved, At: at.Add(-3 * time.Minute), Data: map[string]any{"plan_version": float64(2)}},
		{Sequence: 41, RunID: "R", TaskID: "hello-cli", Type: events.EventTypeMarshalTaskMerged, At: at.Add(-2 * time.Minute), Data: map[string]any{"operation_kind": "merge"}},
		{Sequence: 49, RunID: "R", Type: events.EventTypeMarshalRunClosed, At: at, Data: map[string]any{"checkpoint": "refs/marshal/R/pre-close", "commit": "017b2d0c768b38ddd1c74ad1de7494b31a8270dd", "operation_kind": "close", "previous_commit": "5735885a6d9f", "target": "main"}},
	}
	b := boardmodel.Project(boardmodel.Snapshot{RunID: "R", Run: marshal.Run{State: marshal.Closed}}, evs)
	th := NewTheme(ThemeDefault, true, true)
	v := boardVisual{board: b, now: at, theme: th}
	var got []string
	for _, a := range b.Activity {
		got = append(got, v.activityLine(a))
	}
	plain := StripANSI(strings.Join(got, "\n"))
	for _, want := range []string{"run closed to main 017b2d0", "plan approved version 2", "hello-cli merged"} {
		if !strings.Contains(plain, want) {
			t.Errorf("Activity misses %q:\n%s", want, plain)
		}
	}
	for _, bad := range []string{"changed", "merged ·", "· \n"} {
		if strings.Contains(plain+"\n", bad) {
			t.Errorf("Activity shows %q:\n%s", bad, plain)
		}
	}
	if !strings.Contains(got[0], v.fg("merged", "closed")) || strings.Contains(got[0], v.code("failed", true)) {
		t.Errorf("the close is not in the colour of finished work: %q", got[0])
	}
}
