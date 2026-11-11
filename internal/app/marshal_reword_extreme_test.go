//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func TestLaneAdmissionPauseIsPlain(t *testing.T) {
	s, _, _ := parallelWaitFixture(t, 1)
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) {
		return nil, fmt.Errorf("%w: task lane capacity exhausted", execution.ErrRunBlocked)
	}}
	run := executeUntil(t, s, "par", func(run marshal.Run) bool { return run.Pause != nil })
	pause := run.Pause
	if pause == nil || pause.Reason != "A task couldn't start because too many tasks were running. Nothing ran." || len(pause.Resolutions) == 0 || pause.Resolutions[0] != "Press Enter to try it again." || !strings.HasPrefix(pause.Detail, "task lane admission refused: ") {
		t.Fatalf("capacity pause: %+v", pause)
	}
	decisions, err := s.Store.MarshalDecisions(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range decisions {
		if event.Type == events.EventTypeMarshalEscalated {
			if !strings.HasPrefix(fmt.Sprint(event.Data["reason"]), "task lane admission refused: ") || !strings.Contains(fmt.Sprint(event.Data["summary"]), "couldn't start yet: too many tasks were running.") {
				t.Fatalf("escalation event: %#v", event.Data)
			}
			return
		}
	}
	t.Fatal("missing escalation event")
}

func TestApprovalWaitPauseIsPlain(t *testing.T) {
	if got := approvalWaitReason(1); got != "A task is waiting for the Marshal's go-ahead." {
		t.Fatalf("one wait: %q", got)
	}
	if got := approvalWaitReason(2); got != "2 tasks are waiting for the Marshal's go-ahead." {
		t.Fatalf("two waits: %q", got)
	}
	old := (&process05ApprovalWait{"run", "task", "approval"}).Error()
	if !strings.Contains(old, "/marshal approve-task approval") || strings.Contains(process05WaitResolution, "/marshal") {
		t.Fatalf("machine detail/resolution changed unexpectedly: %q / %q", old, process05WaitResolution)
	}
}

func TestInfraRetryLimitPauseIsPlain(t *testing.T) {
	if got := plainInfraKind("provider quota or rate limit"); got != "usage limit" {
		t.Fatalf("plain kind: %q", got)
	}
	if got := plainInfraKind("unrecognized"); got != "problem" {
		t.Fatalf("unknown kind: %q", got)
	}
	if got := fmt.Sprintf("%s failed 4 times in a row (%s).", ProviderDisplayName("codex"), plainInfraKind("provider quota or rate limit")); got != "Codex failed 4 times in a row (usage limit)." {
		t.Fatalf("plain retry limit: %q", got)
	}
}

func TestFailoverQuotaPauseShowsLocalTime(t *testing.T) {
	// The helper shows times in the machine's own zone, so build them there.
	loc := time.Local
	now := time.Date(2026, time.January, 2, 12, 4, 0, 0, loc)
	until := now.Add(3 * time.Hour)
	if got := formatLocalUntil(until, now); got != "15:04" {
		t.Fatalf("same-day time: %q", got)
	}
	quota := &quotaWaitError{mode: marshal.Governed, until: until}
	if !errors.Is(quota, errWaitingOnQuota) {
		t.Fatalf("quota error lost errors.Is: %v", quota)
	}
	if got := formatLocalUntil(time.Date(2026, time.January, 3, 15, 4, 0, 0, loc), now); got != "Sat 3 Jan 15:04" {
		t.Fatalf("other-day time: %q", got)
	}
}
