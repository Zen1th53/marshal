package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/store"
)

func taskNotices(t *testing.T, s *MarshalService) []store.NoticeOutboxEntry {
	t.Helper()
	notices, err := s.Store.ListNotices(t.Context(), s.ProjectID, "run", 100)
	if err != nil {
		t.Fatal(err)
	}
	// The outbox lists the newest first; read it in the order recorded.
	var out []store.NoticeOutboxEntry
	for i := len(notices) - 1; i >= 0; i-- {
		if notices[i].Category == TaskNoticeCategory {
			out = append(out, notices[i])
		}
	}
	return out
}

func noticeStates(notices []store.NoticeOutboxEntry) []string {
	var out []string
	for _, n := range notices {
		out = append(out, n.Transition)
	}
	return out
}

// A result is collected only from the task's current dispatch: one bound to
// another run, task, worker or attempt, or a repeat of a result already
// collected, is refused with nothing recorded and no rework spent.
func TestCompletionRequiresAssignedDispatch(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 2)
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "write file")
	if err != nil {
		t.Fatal(err)
	}
	req := d.Handle.Request()
	foreign := func(mutate func(*driver.Request)) MarshalDispatch {
		r := req
		r.Task.ReturnsByAgent = nil
		mutate(&r)
		f := d
		f.Handle = driver.NewHandle(r)
		return f
	}
	unchanged := func(label string) {
		t.Helper()
		run, err := s.Snapshot(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		task := run.Tasks[taskIndex(run, "a")]
		if run.Operation != nil || len(task.ReturnsByAgent) != 0 {
			t.Fatalf("%s: refused result changed the run: op=%+v returns=%v", label, run.Operation, task.ReturnsByAgent)
		}
	}
	cases := map[string]MarshalDispatch{
		"other run":     foreign(func(r *driver.Request) { r.RunID = "other" }),
		"other task":    foreign(func(r *driver.Request) { r.Task.PlanTaskID = "b" }),
		"other worker":  foreign(func(r *driver.Request) { r.Task.Worker = "someone-else" }),
		"prior attempt": foreign(func(r *driver.Request) { r.Task.ReturnsByAgent = map[string]int{"worker": 1} }),
	}
	for label, stale := range cases {
		if _, err := s.CollectHandIn(ctx, "run", stale); !errors.Is(err, ErrUnassignedHandIn) {
			t.Fatalf("%s: collected: %v", label, err)
		}
		unchanged(label)
		if run, _ := s.Snapshot(ctx, "run"); run.Tasks[taskIndex(run, "a")].State != marshal.Dispatched {
			t.Fatalf("%s: task left its dispatch", label)
		}
	}
	if n := taskNotices(t, s); len(n) != 0 {
		t.Fatalf("refused results were announced: %v", noticeStates(n))
	}
	if _, err := s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(ctx, "run", d); !errors.Is(err, ErrUnassignedHandIn) {
		t.Fatalf("duplicate collected: %v", err)
	}
	unchanged("duplicate")
	run, _ := s.Snapshot(ctx, "run")
	if run.Tasks[taskIndex(run, "a")].State != marshal.HandedIn {
		t.Fatalf("duplicate moved the task: %s", run.Tasks[taskIndex(run, "a")].State)
	}
	if got := noticeStates(taskNotices(t, s)); strings.Join(got, ",") != TaskNoticeWorkerExited+","+TaskNoticeHandedIn {
		t.Fatalf("notices: %v", got)
	}
}

// A worker exit and a hand-in are announced as such; a returned review says
// so; only the merge after an accepted review announces the task complete.
func TestCompletionNoticeWaitsForReviewAndMerge(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	accept := s.Model
	draft := s.Model.(marshalFakeModel).draft
	s.Model = marshalFakeModel{draft: draft, review: marshal.Review{Verdict: marshal.VerdictReturn, Reviewer: "marshal", Reasons: []string{"the file is empty"}}}
	completed := func() []string {
		var out []string
		for _, n := range taskNotices(t, s) {
			if strings.Contains(n.Summary, "completed") {
				out = append(out, n.Transition)
			}
		}
		return out
	}
	collect := func() {
		t.Helper()
		d, err := s.Dispatch(ctx, "run", "a", "write")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
			t.Fatal(err)
		}
	}
	collect()
	if v, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || v != marshal.VerdictReturn {
		t.Fatalf("review: %s %v", v, err)
	}
	if got := completed(); len(got) != 0 {
		t.Fatalf("completion escaped before review and merge: %v", got)
	}
	s.Model = accept
	collect()
	if v, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review: %s %v", v, err)
	}
	if got := completed(); len(got) != 0 {
		t.Fatalf("completion escaped before merge: %v", got)
	}
	if err := s.Merge(ctx, "run", "a"); err != nil {
		t.Fatal(err)
	}
	want := []string{TaskNoticeWorkerExited, TaskNoticeHandedIn, TaskNoticeReviewReturned, TaskNoticeWorkerExited, TaskNoticeHandedIn, TaskNoticeReviewAccepted, TaskNoticeMerged}
	notices := taskNotices(t, s)
	got := noticeStates(notices)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("notices: %v, want %v", got, want)
	}
	if c := completed(); len(c) != 1 || c[0] != TaskNoticeMerged {
		t.Fatalf("completed notices: %v", c)
	}
	ids := map[string]bool{}
	for _, n := range notices {
		if ids[n.EventID] {
			t.Fatalf("duplicate notice id %s", n.EventID)
		}
		ids[n.EventID] = true
	}
	if !strings.Contains(notices[0].EventID, ":attempt:1:") || !strings.Contains(notices[3].EventID, ":attempt:2:") {
		t.Fatalf("notices are not bound to their attempts: %s %s", notices[0].EventID, notices[3].EventID)
	}
}
