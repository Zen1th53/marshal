package model

import (
	"fmt"
	"go/parser"
	"go/token"
	"math/rand/v2"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
)

var epoch = time.Unix(1_800_000_000, 0).UTC()

func ev(seq int, typ events.EventType, task string, data map[string]any) events.Event {
	return events.Event{Sequence: events.Sequence(seq), Type: typ, RunID: "R1", TaskID: task, At: epoch.Add(time.Duration(seq) * time.Second), Data: data}
}

func charge(seq int, task string, units int64, known bool) events.Event {
	return ev(seq, events.EventTypeMarshalUsageCharged, task, map[string]any{"charge_phase": "hand-in", "usage_units": float64(units), "usage_known": known})
}

func run(tasks ...marshal.Task) marshal.Run {
	return marshal.Run{State: marshal.Dispatching, Tasks: tasks, Settings: marshal.Settings{ReworkLimit: 2}, Budget: marshal.Budget{Tokens: marshal.Ceiling{Plan: 1000}}}
}

func task(id string, state marshal.TaskState) marshal.Task {
	return marshal.Task{PlanTaskID: id, Worker: "codex", State: state, Files: []string{"internal/" + id + ".go"}}
}

func TestNewTaskRowMapsEveryMarshalState(t *testing.T) {
	for _, s := range []marshal.TaskState{"", marshal.Queued, marshal.Dispatched, marshal.HandedIn, marshal.Accepted, marshal.Returned, marshal.Reassigned, marshal.Escalated, marshal.Merged, marshal.Cancelled} {
		if row := NewTaskRow(task("T-1", s), marshal.Settings{}); row.State == "" {
			t.Fatalf("%q has no board state", s)
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("an unknown marshal state must not be shown as some other state")
		}
	}()
	NewTaskRow(task("T-1", "teleported"), marshal.Settings{})
}

func TestTaskGateUsesReadableCanonicalID(t *testing.T) {
	item := NewInboxItem(permission.Request{Kind: "marshal-command", TaskID: "T-031", Object: "release push"})
	if item.Canonical != "task.T-031.release-push" {
		t.Fatalf("canonical = %q", item.Canonical)
	}
}

func TestProjectOrdersTasksByStateClassThenID(t *testing.T) {
	r := run(task("T-3", marshal.Merged), task("T-2", marshal.Dispatched), task("T-5", marshal.Escalated), task("T-1", marshal.Dispatched), task("T-4", marshal.Queued))
	r.Tasks[0].ReturnsByAgent = map[string]int{"codex": 1, "claude": 3}
	b := Project(Snapshot{RunID: "R1", Run: r}, nil)
	var got []string
	for _, row := range b.Tasks {
		got = append(got, row.ID+":"+string(row.State))
	}
	want := "T-5:parked T-1:working T-2:working T-4:queued T-3:merged"
	if strings.Join(got, " ") != want {
		t.Fatalf("order = %v, want %s", got, want)
	}
	if b.Run.Done != 1 || b.Run.Total != 5 || b.Run.TokenLimit != 1000 || b.Run.State != marshal.Dispatching {
		t.Fatalf("run head = %+v", b.Run)
	}
	if merged := b.Tasks[4]; merged.Returns != 1 || merged.ReworkLimit != 2 {
		t.Fatalf("rework = %d/%d, want the current worker's 1/2", merged.Returns, merged.ReworkLimit)
	}
}

func TestProjectCarriesLiveUserPauseForDisplay(t *testing.T) {
	b := Project(Snapshot{RunID: "R1", Run: run(), UserPaused: true}, nil)
	if b.Run.State != marshal.Dispatching || !b.Run.UserPaused {
		t.Fatalf("pause display changed run state or was lost: %+v", b.Run)
	}
	b = Project(Snapshot{RunID: "R1", Run: run()}, nil)
	if b.Run.UserPaused {
		t.Fatalf("pause display remained after resume: %+v", b.Run)
	}
}

func TestEventsSetSinceButNeverState(t *testing.T) {
	// T-1 was returned after its dispatch; T-2 was merged once, then an
	// amendment queued it again without a task event.
	r := run(task("T-1", marshal.Returned), task("T-2", marshal.Queued))
	b := Project(Snapshot{RunID: "R1", Run: r}, []events.Event{
		ev(1, events.EventTypeMarshalTaskDispatched, "T-1", nil),
		ev(2, events.EventTypeMarshalTaskReturned, "T-1", map[string]any{"reason": "tests fail"}),
		ev(3, events.EventTypeMarshalTaskDispatched, "T-2", nil),
		ev(4, events.EventTypeMarshalTaskMerged, "T-2", nil),
		ev(5, events.EventTypeMarshalPlanAmended, "", nil),
	})
	rows := map[string]TaskRow{}
	for _, row := range b.Tasks {
		rows[row.ID] = row
	}
	if r1 := rows["T-1"]; r1.State != TaskRework || !r1.Since.Equal(epoch.Add(2*time.Second)) {
		t.Fatalf("T-1 = %s since %v, want rework since the return", r1.State, r1.Since)
	}
	if r2 := rows["T-2"]; r2.State != TaskQueued || !r2.Since.IsZero() {
		t.Fatalf("T-2 = %s since %v, want the snapshot's queued state and no since", r2.State, r2.Since)
	}
}

func TestLaneUsageBelongsToTheLatestDispatch(t *testing.T) {
	r := run(task("T-1", marshal.Dispatched), task("T-2", marshal.HandedIn))
	snap := Snapshot{RunID: "R1", Run: r, Workers: []Worker{{ID: "codex-1", Provider: "codex", TaskID: "T-1", Pane: "%3"}, {ID: "agy-1", TaskID: "T-9"}, {ID: "claude-1", TaskID: "T-2"}}}
	b := Project(snap, []events.Event{
		ev(1, events.EventTypeMarshalPlanApproved, "", nil),
		ev(2, events.EventTypeMarshalTaskDispatched, "T-1", nil),
		charge(3, "T-1", 100, true),
		charge(4, "T-1", 50, true),
		ev(5, events.EventTypeMarshalTaskDispatched, "T-1", nil),
		charge(6, "T-1", 10, true),
		charge(7, "T-2", 999, false),
	})
	lane := b.Lanes[0]
	if lane.State != LaneWorking || lane.Pane != "%3" || lane.Tokens != 10 || !lane.Started.Equal(epoch.Add(5*time.Second)) {
		t.Fatalf("lane = %+v, want working with only the second dispatch's 10 tokens", lane)
	}
	if lane.Recent != [8]int64{7: 10} {
		t.Fatalf("recent = %v", lane.Recent)
	}
	if b.Lanes[1].State != LaneIdle || b.Lanes[2].State != LaneReview || b.Lanes[2].Tokens != 0 {
		t.Fatalf("lanes = %+v", b.Lanes)
	}
	if b.Run.Tokens != (marshal.Amount{Value: 160}) || b.Run.Charges != 4 || !b.Run.Started.Equal(epoch.Add(time.Second)) {
		t.Fatalf("run = %+v, want 160 tokens unknown after an unknown charge", b.Run)
	}
	if known := Project(snap, []events.Event{charge(1, "T-1", 5, true)}).Run.Tokens; known != (marshal.Amount{Value: 5, Known: true}) {
		t.Fatalf("tokens = %+v, want known after only known charges", known)
	}
	if none := Project(snap, nil).Run.Tokens; none.Known {
		t.Fatal("tokens with no charge at all must not read as a known zero")
	}
}

func TestSparkScalesToItsOwnMaximum(t *testing.T) {
	l := Lane{Recent: [8]int64{0, 10, 20, 35, 70, 0, 0, 7}}
	if got := l.Spark(); got != [8]uint8{0, 1, 2, 3, 7, 0, 0, 0} {
		t.Fatalf("spark = %v", got)
	}
	if (Lane{}).Spark() != [8]uint8{} {
		t.Fatal("an idle lane has a flat spark")
	}
}

func TestInboxPutsTypedGatesFirstAndKeepsArrivalOrder(t *testing.T) {
	reqs := []permission.Request{
		{Kind: "network", Object: "registry.npmjs.org:443", TaskID: "T-023"},
		{Kind: "marshal-command", Object: "/marshal approve-task A1"},
		{Kind: "read", Object: "/home/x"},
		{Kind: "teleport", Object: "?"},
		{Kind: "credential-bound", Object: "codex"},
	}
	b := Project(Snapshot{RunID: "R1", Requests: reqs}, nil)
	var got []string
	for _, it := range b.Inbox {
		got = append(got, string(it.Kind)+"/"+string(it.Gate)+"/"+it.Request.Object)
	}
	want := []string{"gate/typed//marshal approve-task A1", "gate/typed/?", "egress/single/registry.npmjs.org:443", "read/single//home/x", "credential/single/codex"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inbox = %v\nwant %v", got, want)
	}
	again := NewInboxItem(reqs[1])
	if again.Canonical != b.Inbox[0].Canonical || !strings.HasPrefix(again.Canonical, "gate.") || len(again.Canonical) != len("gate.")+8 {
		t.Fatalf("canonical = %q, want a stable gate.<8 hex>", again.Canonical)
	}
	if b.Inbox[0].Canonical == b.Inbox[1].Canonical {
		t.Fatal("two decisions share a canonical id")
	}
}

func TestActivityKeepsStateChangesNewestFirstAndBounded(t *testing.T) {
	var evs []events.Event
	seq := 0
	for i := range maxActivity + 5 {
		seq++
		evs = append(evs, ev(seq, events.EventTypeMarshalTaskReturned, "T-1", map[string]any{"reason": fmt.Sprintf("r%d\nsecond line", i)}))
		seq++
		evs = append(evs, charge(seq, "T-1", 1, true))
	}
	seq++
	evs = append(evs, ev(seq, events.EventTypeMarshalVerifierRecorded, "", nil))
	seq++
	other := ev(seq, events.EventTypeMarshalRunClosed, "", nil)
	other.RunID = "R2"
	evs = append(evs, other)
	seq++
	evs = append(evs, ev(seq, events.EventTypeMarshalTaskReassigned, "T-1", map[string]any{"worker": "claude"}))
	b := Project(Snapshot{RunID: "R1", Run: run(task("T-1", marshal.Reassigned))}, evs)
	if len(b.Activity) != maxActivity {
		t.Fatalf("activity has %d lines, want %d", len(b.Activity), maxActivity)
	}
	if top := b.Activity[0]; top.Type != events.EventTypeMarshalTaskReassigned || top.Detail != "claude" || top.Seq != events.Sequence(seq) {
		t.Fatalf("newest = %+v", top)
	}
	if next := b.Activity[1]; next.Detail != fmt.Sprintf("r%d", maxActivity+4) {
		t.Fatalf("second = %+v, want the last return's first line", next)
	}
	if b.Seq != events.Sequence(seq) {
		t.Fatalf("seq = %d, want %d", b.Seq, seq)
	}
}

func TestTailSkipsRedeliveryAndLeavesTheReceiver(t *testing.T) {
	snap := Snapshot{RunID: "R1", Run: run(task("T-1", marshal.Dispatched)), Workers: []Worker{{ID: "codex-1", TaskID: "T-1"}}}
	evs := []events.Event{ev(1, events.EventTypeMarshalTaskDispatched, "T-1", nil), charge(2, "T-1", 40, true)}
	before := Project(snap, evs[:1])
	before.Stale = true
	after := before.Tail(evs).Tail(evs)
	if after.Lanes[0].Tokens != 40 || after.Run.Charges != 1 || len(after.Activity) != 1 || after.Stale {
		t.Fatalf("after = %+v, want each event applied once and stale cleared", after)
	}
	want := Project(snap, evs[:1])
	want.Stale = true
	if !reflect.DeepEqual(before, want) {
		t.Fatal("Tail wrote into the Board it was called on")
	}
}

func TestNewWiredStatus(t *testing.T) {
	for _, c := range []struct {
		walk, detail string
		want         WiredStatus
	}{
		{"FORM", "▸ Exact task ID *: (empty)", WiredStatus{SpecID: "S", State: WiredLive}},
		{"CONFIRM", "", WiredStatus{SpecID: "S", State: WiredLive}},
		{"RESULT", "BLOCKED: no run", WiredStatus{SpecID: "S", State: WiredBlockedByState, Cause: "BLOCKED: no run"}},
		{"DISABLED", "needs cloud", WiredStatus{SpecID: "S", State: WiredUnderConstruction, Cause: "needs cloud"}},
		{"ERROR", "count table", WiredStatus{SpecID: "S", State: WiredUnderConstruction, Cause: "count table"}},
	} {
		got, err := NewWiredStatus("S", c.walk, c.detail)
		if err != nil || got != c.want {
			t.Fatalf("%s: %+v, %v; want %+v", c.walk, got, err, c.want)
		}
	}
	if _, err := NewWiredStatus("S", "MAYBE", ""); err == nil {
		t.Fatal("an unknown walk state must be refused, not guessed")
	}
}

// TestRebuildEqualsTail is the read model's contract: a Board projected from
// scratch equals one kept up to date by tailing, whatever the windows and
// however much of the stream is delivered twice.
func TestRebuildEqualsTail(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 1000 {
		snap, evs := randomRun(rng)
		want := Project(snap, evs)
		pos := rng.IntN(len(evs) + 1)
		prefix := pos
		first := Project(snap, evs[:prefix])
		got := first
		for pos < len(evs) {
			start := max(0, pos-rng.IntN(4))
			end := min(len(evs), pos+rng.IntN(12))
			if rng.IntN(5) == 0 {
				got.Stale = true
			}
			got = got.Tail(evs[start:end])
			pos = end
		}
		got.Stale = false
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("sequence %d: tail differs from rebuild\n tail    %+v\n rebuild %+v", i, got, want)
		}
		if !reflect.DeepEqual(first, Project(snap, evs[:prefix])) {
			t.Fatalf("sequence %d: tailing changed an earlier Board", i)
		}
	}
}

var randomTypes = []events.EventType{
	events.EventTypeMarshalPlanApproved, events.EventTypeMarshalPlanAmended, events.EventTypeMarshalRecommended,
	events.EventTypeMarshalTaskDispatched, events.EventTypeMarshalUsageCharged, events.EventTypeMarshalUsageCharged,
	events.EventTypeMarshalTaskHandedIn, events.EventTypeMarshalTaskAccepted, events.EventTypeMarshalTaskReturned,
	events.EventTypeMarshalTaskReassigned, events.EventTypeMarshalEscalated, events.EventTypeMarshalTaskMerged,
	events.EventTypeMarshalTaskCancelled, events.EventTypeMarshalVerifierRecorded, events.EventTypeMarshalRunClosed,
}

var randomStates = []marshal.TaskState{marshal.Queued, marshal.Dispatched, marshal.HandedIn, marshal.Accepted, marshal.Returned, marshal.Reassigned, marshal.Escalated, marshal.Merged, marshal.Cancelled}

var randomKinds = []string{"network", "read", "credential", "credential-bound", "memory", "marshal-command", "project-contract", "unknown"}

func randomRun(rng *rand.Rand) (Snapshot, []events.Event) {
	ids := []string{"", "T-unknown"}
	r := run()
	for i := range 1 + rng.IntN(60) {
		id := "T-" + strconv.Itoa(i)
		ids = append(ids, id)
		r.Tasks = append(r.Tasks, task(id, randomStates[rng.IntN(len(randomStates))]))
	}
	snap := Snapshot{RunID: "R1", Run: r}
	for i := range rng.IntN(13) {
		snap.Workers = append(snap.Workers, Worker{ID: "w" + strconv.Itoa(i), TaskID: ids[rng.IntN(len(ids))]})
	}
	for i := range rng.IntN(7) {
		snap.Requests = append(snap.Requests, permission.Request{Kind: randomKinds[rng.IntN(len(randomKinds))], Object: "o" + strconv.Itoa(i)})
	}
	var evs []events.Event
	seq := 0
	for range rng.IntN(200) {
		seq += 1 + rng.IntN(3)
		e := ev(seq, randomTypes[rng.IntN(len(randomTypes))], ids[rng.IntN(len(ids))], map[string]any{})
		if rng.IntN(10) == 0 {
			e.RunID = "R2"
		}
		switch rng.IntN(3) {
		case 0:
			e.Data["reason"] = "reason " + strconv.Itoa(seq)
		case 1:
			e.Data["worker"] = "claude"
		}
		if e.Type == events.EventTypeMarshalUsageCharged {
			e.Data["usage_units"] = int64(rng.IntN(5000))
			e.Data["usage_known"] = rng.IntN(8) != 0
		}
		evs = append(evs, e)
	}
	return snap, evs
}

func TestProjectionNeverImportsTheStore(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); strings.HasSuffix(path, "/internal/store") || path == "database/sql" {
				t.Fatalf("%s imports %s; the Board is a read model and never touches the store", name, path)
			}
		}
	}
}

// BenchmarkProject53Tasks12Lanes is the Board's budget: a full rebuild of a
// 53-task run with 12 live lanes and eight events per task under 2 ms.
func BenchmarkProject53Tasks12Lanes(b *testing.B) {
	r := run()
	for i := range 53 {
		r.Tasks = append(r.Tasks, task(fmt.Sprintf("T-%03d", i), randomStates[i%len(randomStates)]))
	}
	snap := Snapshot{RunID: "R1", Run: r}
	for i := range 12 {
		snap.Workers = append(snap.Workers, Worker{ID: fmt.Sprintf("codex-%d", i), TaskID: fmt.Sprintf("T-%03d", i)})
	}
	for i := range 5 {
		snap.Requests = append(snap.Requests, permission.Request{Kind: randomKinds[i], Object: strconv.Itoa(i)})
	}
	var evs []events.Event
	for i := range 53 * 8 {
		id := fmt.Sprintf("T-%03d", i%53)
		if i%2 == 0 {
			evs = append(evs, charge(i+1, id, int64(i), true))
		} else {
			evs = append(evs, ev(i+1, randomTypes[i%len(randomTypes)], id, map[string]any{"reason": "r"}))
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		Project(snap, evs)
	}
}

// A recovered run keeps tasks dispatched or handed in while no worker runs:
// those tasks are stalled. A task with a live lane on it is not.
func TestStalledIsWorkingOrReviewWithoutALiveLane(t *testing.T) {
	r := run(task("T-1", marshal.Dispatched), task("T-2", marshal.HandedIn), task("T-3", marshal.Dispatched), task("T-4", marshal.Escalated), task("T-5", marshal.Queued))
	b := Project(Snapshot{RunID: "R1", Run: r, Workers: []Worker{{ID: "codex-1", Provider: "codex", TaskID: "T-3"}, {ID: "codex-2", Provider: "codex", TaskID: "T-9"}}}, nil)
	got := map[string]bool{}
	for _, row := range b.Tasks {
		got[row.ID] = b.Stalled(row)
	}
	want := map[string]bool{"T-1": true, "T-2": true, "T-3": false, "T-4": false, "T-5": false}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stalled = %v, want %v", got, want)
	}
	if !b.Live("T-3") || b.Live("T-9") {
		t.Fatalf("live T-3=%v T-9=%v: an idle lane on an unknown task is not live", b.Live("T-3"), b.Live("T-9"))
	}
	// No lanes at all, as LANES 0 shows: every moving task is stalled.
	empty := Project(Snapshot{RunID: "R1", Run: r}, nil)
	for _, row := range empty.Tasks {
		if want := row.State == TaskWorking || row.State == TaskReview; empty.Stalled(row) != want {
			t.Fatalf("%s %s stalled = %v with no lanes", row.ID, row.State, !want)
		}
	}
}

// A run-level change names its subject and what happened, from the event's
// own data: the close says where the run went (the walkthrough's close
// painted a bare "changed ·").
func TestRunLevelActivityNamesSubjectAndWord(t *testing.T) {
	for _, tc := range []struct {
		e                     events.Event
		subject, word, detail string
	}{
		{events.Event{Type: events.EventTypeMarshalRunClosed, Data: map[string]any{"target": "main", "commit": "017b2d0c768b38ddd1c74ad1de7494b31a8270dd", "checkpoint": "refs/x"}}, "run", "closed", "to main 017b2d0"},
		{events.Event{Type: events.EventTypeMarshalRunClosed, Data: map[string]any{}}, "run", "closed", ""},
		{events.Event{Type: events.EventTypeMarshalPlanApproved, Data: map[string]any{"plan_version": float64(2)}}, "plan", "approved", "version 2"},
		{events.Event{Type: events.EventTypeMarshalPlanAmended, Data: map[string]any{"major": true}}, "plan", "amended", "major change"},
		{events.Event{Type: events.EventTypeMarshalTaskMerged, TaskID: "hello-cli", Data: map[string]any{}}, "", "", ""},
	} {
		l, ok := NewActivityLine(tc.e)
		if !ok || l.Subject != tc.subject || l.Word != tc.word || l.Detail != tc.detail {
			t.Errorf("%s: %+v %v", tc.e.Type, l, ok)
		}
	}
}

// An accepted task shows that checks passed, never the raw gate code the
// audit record carries.
func TestAcceptedActivityHasNoGateCode(t *testing.T) {
	e := events.Event{Type: events.EventTypeMarshalTaskAccepted, TaskID: "T-1", Data: map[string]any{"reason": "CONST_ALLOWED"}}
	l, ok := NewActivityLine(e)
	if !ok {
		t.Fatalf("accepted event produced no activity line")
	}
	if l.Detail != "checks passed" {
		t.Fatalf("Detail = %q, want %q", l.Detail, "checks passed")
	}
	if strings.Contains(l.Detail, "CONST_") {
		t.Fatalf("Detail %q leaks the gate code", l.Detail)
	}
}

// A verification stop an older build recorded with the runtime's raw
// execution-run error reads in plain words on the Board and in Activity;
// any other stop keeps its own reason.
func TestInternalVerificationStopReadsPlain(t *testing.T) {
	raw := "verification stopped: execution run conflict: required completion child identity/state"
	r := marshal.Run{State: marshal.AwaitingUser, Pause: &marshal.Pause{Reason: raw, Resolutions: []string{"inspect the retained verification error and artifacts; resolve the reported cause, then /marshal resume to rerun verification"}, Code: marshal.CodeVerificationStopped, ResumeState: marshal.Verifying, Action: marshal.PauseResume}}
	b := Project(Snapshot{RunID: "R1", Run: r}, nil)
	if b.Run.Paused != "Marshal could not finish checking the run." || !reflect.DeepEqual(b.Run.Next, []string{"Press Enter to let the Marshal retry."}) {
		t.Fatalf("board stop: %q %q", b.Run.Paused, b.Run.Next)
	}
	l, ok := NewActivityLine(events.Event{Type: events.EventTypeMarshalEscalated, Data: map[string]any{"reason": raw, "role": "verification"}})
	if !ok || l.Detail != "Marshal could not finish checking the run." {
		t.Fatalf("activity: %+v %v", l, ok)
	}
	check := "verification stopped: go test ./... failed (exit 1)"
	r.Pause.Reason = check
	if b := Project(Snapshot{RunID: "R1", Run: r}, nil); b.Run.Paused != check {
		t.Fatalf("a check stop lost its reason: %q", b.Run.Paused)
	}
	if l, _ := NewActivityLine(events.Event{Type: events.EventTypeMarshalEscalated, Data: map[string]any{"reason": check}}); l.Detail != check {
		t.Fatalf("a check stop's activity lost its reason: %q", l.Detail)
	}
}
