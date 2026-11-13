// Package model is the Board's read model: a projection of one Marshal run
// snapshot and the run's canonical decision events. It never writes to the
// store and holds nothing that cannot be projected again from those two
// inputs, so a Board can be thrown away and rebuilt at any time.
package model

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
)

// Snapshot is what the Board starts from. Run is the run as the store last
// saved it; Requests and Workers are the live sources the store does not
// hold: the permission queue in arrival order and the worker invocations the
// tmux monitor tracks.
type Snapshot struct {
	RunID    string
	Run      marshal.Run
	Requests []permission.Request
	Workers  []Worker
	// Driving is set while this process is driving the run (dispatching,
	// collecting, reviewing, merging): a task in review is then being
	// reviewed, not stalled.
	Driving bool
	// UserPaused is the TUI's live pause hold; it affects only the display.
	UserPaused bool
}

// Worker is one live worker invocation as the tmux monitor knows it.
// Status is the state the worker itself last reported over OSC 7501
// (working, idle, blocked, done, error), empty when it reported none; Last
// is then the message it reported with it, already redacted.
type Worker struct {
	ID, Provider, TaskID, Pane, Last string
	Status                           string
}

// Board is the read model. Fields that come from the snapshot change only
// when a new snapshot is projected; fields that come from events change on
// Tail. Each field's comment says which.
type Board struct {
	Run      RunHead
	Tasks    []TaskRow      // snapshot; ordered by state class, then ID
	PlanID   string         // snapshot; inspector breadcrumb
	Budget   marshal.Budget // snapshot; inspector authority
	Events   []events.Event // events of this run; inspector's complete timeline
	Lanes    []Lane         // snapshot workers, event usage
	Inbox    []InboxItem    // snapshot; typed gates first, then arrival
	Activity []ActivityLine // events; newest first, at most maxActivity
	// Stale is set by the caller when the store could not be read, so the
	// last projection stays on screen with a notice. Project and Tail clear it.
	Stale bool
	// Seq is the last event sequence applied. Tail skips anything at or
	// below it, so re-reading an overlapping window of the stream is harmless.
	Seq events.Sequence
}

// RunHead is the one-line summary of the run.
type RunHead struct {
	ID          string
	Title       string           // plan title, when supplied by the caller
	State       marshal.RunState // snapshot
	Done        int              // snapshot: merged tasks
	Running     int              // optional run summary when task rows are windowed
	ActiveLanes int              // optional run summary when lane rows are windowed
	LaneCap     int              // snapshot: most lanes the run dispatches at once; 0 when unknown
	Total       int              // snapshot
	TokenLimit  int64            // snapshot: the plan ceiling, 0 when unset
	Tokens      marshal.Amount   // events: sum of usage charges
	Charges     int              // events: usage charges seen
	Started     time.Time        // events: first plan approval
	// Paused and Next are why an awaiting_user run waits for the operator
	// and what resolves it, from the run's pause (snapshot); empty when the
	// run does not wait or recorded no reason.
	Paused string
	Next   []string
	// PauseAction, PauseTask and PauseCode are the pause's machine fields.
	PauseAction marshal.PauseAction
	PauseTask   string
	PauseCode   string
	// PauseKind names a pause the Board handles specially ("quota" is the
	// all-providers-at-limit pause); PauseUntil is when a limit is expected
	// to free up, zero when unknown.
	PauseKind  string
	PauseUntil time.Time
	// Driving is the snapshot's: this process is driving the run now.
	Driving bool
	// UserPaused is the TUI's live pause hold; it affects only the display.
	UserPaused bool
}

// TaskState is the operator's word for where a task is. It folds the
// canonical marshal.TaskState into what the operator decides about: a
// returned and a reassigned task both wait for rework.
type TaskState string

const (
	TaskParked    TaskState = "parked"
	TaskRework    TaskState = "rework"
	TaskReview    TaskState = "review"
	TaskAccepted  TaskState = "accepted"
	TaskWorking   TaskState = "working"
	TaskQueued    TaskState = "queued"
	TaskMerged    TaskState = "merged"
	TaskCancelled TaskState = "cancelled"
)

// stateClass is the Board's task order: what needs the operator first,
// finished work last.
var stateClass = map[TaskState]int{
	TaskParked: 0, TaskRework: 1, TaskReview: 2, TaskAccepted: 3,
	TaskWorking: 4, TaskQueued: 5, TaskMerged: 6, TaskCancelled: 7,
}

// A drafted task has no state until the plan is saved; it waits like a
// queued one.
var taskStates = map[marshal.TaskState]TaskState{
	"":                 TaskQueued,
	marshal.Queued:     TaskQueued,
	marshal.Dispatched: TaskWorking,
	marshal.HandedIn:   TaskReview,
	marshal.Accepted:   TaskAccepted,
	marshal.Returned:   TaskRework,
	marshal.Reassigned: TaskRework,
	marshal.Escalated:  TaskParked,
	marshal.Merged:     TaskMerged,
	marshal.Cancelled:  TaskCancelled,
}

// eventStates names the state each task event moves a task into.
var eventStates = map[events.EventType]TaskState{
	events.EventTypeMarshalTaskDispatched: TaskWorking,
	events.EventTypeMarshalTaskHandedIn:   TaskReview,
	events.EventTypeMarshalTaskAccepted:   TaskAccepted,
	events.EventTypeMarshalTaskReturned:   TaskRework,
	events.EventTypeMarshalTaskReassigned: TaskRework,
	events.EventTypeMarshalEscalated:      TaskParked,
	events.EventTypeMarshalTaskMerged:     TaskMerged,
	events.EventTypeMarshalTaskCancelled:  TaskCancelled,
}

// TaskRow is one plan task.
type TaskRow struct {
	BaseCommit, ResultCommit string
	ID                       string
	Worker                   string
	Scope                    []string // the task's files
	Title, ParentID, Branch  string
	Checks                   []string
	Criteria                 []string
	AuthorizedHosts          []string // available authority manifest entries
	AuthorizedTools          []string
	EvidenceSummary          string // verifier's compact claim summary, when known
	State                    TaskState
	// Since is when the task entered State: the latest event that moves a
	// task into State. Zero when no event does, as for a task an amendment
	// queued again.
	Since time.Time
	// Began is when the task was first dispatched in the events seen, zero
	// when it never was. A finished task's time runs from Began to Since.
	Began time.Time
	// Returns and ReworkLimit come from the snapshot only. Counting returned
	// events would disagree with the run: operator retries record a return
	// without charging rework, and reassignment charges the previous worker.
	Returns, ReworkLimit int
}

// StateOf is the operator's word for a canonical task state, and whether
// the state is known.
func StateOf(s marshal.TaskState) (TaskState, bool) {
	state, ok := taskStates[s]
	return state, ok
}

// NewTaskRow projects one task of a run with its rework settings.
func NewTaskRow(t marshal.Task, settings marshal.Settings) TaskRow {
	state, ok := taskStates[t.State]
	if !ok {
		panic("board: unknown marshal task state " + string(t.State))
	}
	return TaskRow{
		BaseCommit:   t.BaseCommit,
		ResultCommit: t.ResultCommit,
		ID:           t.PlanTaskID,
		Worker:       t.Worker,
		Scope:        slices.Clone(t.Files),
		Title:        t.Title,
		ParentID:     t.ParentID,
		Branch:       t.Branch,
		Checks:       taskChecks(t.Checks),
		Criteria:     slices.Clone(t.Criteria),
		State:        state,
		Returns:      t.ReturnsByAgent[t.Worker],
		ReworkLimit:  settings.ReworkLimit,
	}
}

func taskChecks(checks []marshal.Check) []string {
	out := make([]string, 0, len(checks))
	for _, check := range checks {
		out = append(out, check.Command)
	}
	return out
}

// LaneState is what a live worker invocation is doing, read from its task.
type LaneState string

const (
	LaneWorking LaneState = "working"
	LaneReview  LaneState = "review"
	LaneRework  LaneState = "rework"
	LaneParked  LaneState = "parked"
	LaneIdle    LaneState = "idle"
)

var laneStates = map[TaskState]LaneState{
	TaskWorking: LaneWorking,
	TaskReview:  LaneReview,
	TaskRework:  LaneRework,
	TaskParked:  LaneParked,
}

// Lane is one live worker invocation.
type Lane struct {
	Worker, Provider, TaskID, Pane string
	State                          LaneState // snapshot: from its task
	// Started, Tokens and Recent are this invocation's: the task's latest
	// dispatch resets them.
	Started time.Time
	Tokens  int64
	Recent  [8]int64 // known tokens of the last eight charges, oldest first
	Last    string   // last action, one line, redacted
	// Status is what the worker reported over OSC 7501, empty for none.
	// It colours the row; State, from the task, stays the lane's state.
	Status string
}

// NewLane projects one worker invocation. A worker whose task the run does
// not know, or whose task is not in a working state, is idle.
func NewLane(w Worker, tasks []TaskRow) Lane {
	l := Lane{Worker: w.ID, Provider: w.Provider, TaskID: w.TaskID, Pane: w.Pane, Last: w.Last, Status: w.Status, State: LaneIdle}
	for _, t := range tasks {
		if t.ID == w.TaskID {
			if s, ok := laneStates[t.State]; ok {
				l.State = s
			}
			break
		}
	}
	return l
}

// Live reports whether a live worker invocation is on the task: a lane that
// names it and is not idle.
func (b Board) Live(taskID string) bool {
	for _, l := range b.Lanes {
		if l.TaskID == taskID && l.State != LaneIdle {
			return true
		}
	}
	return false
}

// Stalled reports whether the task claims to be moving with nothing behind
// it: working with no live lane on it, or in review while nothing drives the
// run, as a run recovered after its workers ended shows. A review has no
// worker lane: while the run is driven the Marshal is reviewing it. Such a
// row is drawn still, never spinning.
func (b Board) Stalled(t TaskRow) bool {
	switch t.State {
	case TaskWorking:
		return !b.Live(t.ID)
	case TaskReview:
		return !b.Run.Driving && !b.Live(t.ID)
	}
	return false
}

// Finished reports whether the task's work is over: merged or cancelled.
func (t TaskRow) Finished() bool {
	return t.State == TaskMerged || t.State == TaskCancelled
}

// Elapsed is the task row's time and whether it has one. A live task's
// time runs from when it entered its state to now; a finished task's time
// is frozen at how long it took, from its first dispatch to its end, and it
// has none when it never ran.
func (t TaskRow) Elapsed(now time.Time) (time.Duration, bool) {
	if t.Since.IsZero() {
		return 0, false
	}
	end, start := now, t.Since
	if t.Finished() {
		if t.Began.IsZero() {
			return 0, false
		}
		end, start = t.Since, t.Began
	}
	return max(0, end.Sub(start)), true
}

// Spark scales Recent to eight levels, 0 to 7, against its own maximum, so
// the shape shows the lane's rhythm whatever its absolute volume.
func (l Lane) Spark() [8]uint8 {
	var out [8]uint8
	top := slices.Max(l.Recent[:])
	if top <= 0 {
		return out
	}
	for i, v := range l.Recent {
		out[i] = uint8(v * 7 / top)
	}
	return out
}

// InboxKind groups permission requests by what the operator is deciding.
type InboxKind string

const (
	InboxEgress     InboxKind = "egress"
	InboxTool       InboxKind = "tool" // a Marshal tool call from the native chat
	InboxRead       InboxKind = "read"
	InboxCredential InboxKind = "credential"
	InboxMemory     InboxKind = "memory"
	InboxGate       InboxKind = "gate"
)

var inboxKinds = map[string]InboxKind{
	"network":          InboxEgress,
	"read":             InboxRead,
	"credential":       InboxCredential,
	"credential-bound": InboxCredential,
	"memory":           InboxMemory,
	"marshal-command":  InboxGate,
	"project-contract": InboxGate,
	"marshal-tool":     InboxTool,
}

// GateLevel is how much the operator must do to decide.
type GateLevel string

const (
	GateSingle GateLevel = "single" // one key
	GateTyped  GateLevel = "typed"  // type Canonical
)

// InboxItem is one pending decision. Request is kept whole because the
// decision goes back through decidePermission with it unchanged.
type InboxItem struct {
	Kind      InboxKind
	Gate      GateLevel
	Canonical string
	Actions   []string
	Request   permission.Request
}

// NewInboxItem projects one pending permission request. A kind the Board
// does not know becomes a typed gate: an unrecognised decision must be
// neither hidden nor one key away.
func NewInboxItem(r permission.Request) InboxItem {
	kind, ok := inboxKinds[r.Kind]
	if !ok {
		kind = InboxGate
	}
	gate := GateSingle
	if kind == InboxGate {
		gate = GateTyped
	}
	// A task gate uses the plan's canonical task id and a readable action slug.
	// Requests without a task retain a stable digest identity.
	sum := sha256.Sum256([]byte(r.Key()))
	canonical := string(kind) + "." + hex.EncodeToString(sum[:4])
	if gate == GateTyped && r.TaskID != "" {
		slug := strings.ToLower(r.Object)
		slug = strings.TrimSpace(slug)
		slug = strings.ReplaceAll(slug, " --", " ")
		var b strings.Builder
		dash := false
		for _, c := range slug {
			if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
				b.WriteRune(c)
				dash = false
			} else if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
		slug = strings.Trim(b.String(), "-")
		if slug == "" {
			slug = "confirm"
		}
		canonical = "task." + r.TaskID + "." + slug
	}
	return InboxItem{Kind: kind, Gate: gate, Canonical: canonical, Actions: []string{"allow once", "allow run", "deny", "inspect", "return", "cancel"}, Request: r}
}

// ActivityLine is one state change of the run or of a task.
type ActivityLine struct {
	Seq    events.Sequence
	At     time.Time
	Type   events.EventType
	TaskID string    // empty for a run-level change
	Detail string    // the event's reason, or the worker it names
	State  TaskState // resulting task state; empty for run-level changes
	// Subject and Word name a run-level change in the operator's words
	// ("run" "closed", "plan" "approved"); both are empty for a task line,
	// whose subject is TaskID and whose word is State. A run-level line
	// never paints a bare verb.
	Subject, Word string
}

// maxActivity bounds the Board's history; the inspector reads the full
// event stream, so the Board keeps only what fits on a screen.
const maxActivity = 32

// activityTypes are the state changes; charges, recommendations and
// verifier records are evidence, not state.
var activityTypes = map[events.EventType]bool{
	events.EventTypeMarshalPlanApproved: true,
	events.EventTypeMarshalPlanAmended:  true,
	events.EventTypeMarshalRunClosed:    true,
}

func init() {
	for t := range eventStates {
		activityTypes[t] = true
	}
}

// plainVerificationRetry is what the person does about a verification stop
// the runtime's own bookkeeping caused.
const plainVerificationRetry = marshal.VerificationRetry

// plainVerificationStop is the plain sentence for a verification stop an
// older build recorded with the runtime's raw execution-run error as its
// reason; the runtime now records the sentence itself and keeps the raw
// error as the pause's detail (app.internalVerificationStop).
// An older build also recorded a required-context or verifier-output refusal
// as its bare code (REQUIRED_CONTEXT_REVIEW_PENDING); it reads as the plain
// sentence the runtime records now (marshal.PlainVerificationStop).
func plainVerificationStop(reason string) (string, bool) {
	for _, raw := range []string{"verification stopped: execution run conflict", "verification stopped: execution run invalid", "verification stopped: invalid state transition"} {
		if strings.HasPrefix(reason, raw) {
			return "Marshal could not finish checking the run.", true
		}
	}
	if plain, _, ok := marshal.PlainVerificationStop(reason); ok {
		return plain, true
	}
	return "", false
}

// NewActivityLine projects an event into a line, or reports false when the
// event is not a state change.
func NewActivityLine(e events.Event) (ActivityLine, bool) {
	if !activityTypes[e.Type] {
		return ActivityLine{}, false
	}
	detail, _ := e.Data["summary"].(string)
	if detail == "" {
		detail, _ = e.Data["reason"].(string)
	}
	if detail == "" {
		detail, _ = e.Data["worker"].(string)
	}
	detail, _, _ = strings.Cut(detail, "\n")
	if plain, ok := plainVerificationStop(detail); ok && e.Type == events.EventTypeMarshalEscalated {
		detail = plain
	}
	line := ActivityLine{Seq: e.Sequence, At: e.At, Type: e.Type, TaskID: e.TaskID, Detail: detail, State: eventStates[e.Type]}
	switch e.Type {
	case events.EventTypeMarshalRunClosed:
		// marshal.run.closed carries the branch the run delivered to and
		// the commit it left there (app/marshal_lifecycle.go).
		line.Subject, line.Word, line.Detail = "run", "closed", runClosedDetail(e.Data)
	case events.EventTypeMarshalPlanApproved:
		line.Subject, line.Word = "plan", "approved"
		if v := number(e.Data["plan_version"]); v > 0 {
			line.Detail = fmt.Sprintf("version %d", v)
		}
	case events.EventTypeMarshalPlanAmended:
		line.Subject, line.Word = "plan", "amended"
		if major, ok := e.Data["major"].(bool); ok {
			line.Detail = map[bool]string{true: "major change", false: "in scope"}[major]
		}
	}
	if e.Type == events.EventTypeMarshalTaskAccepted {
		line.Detail = "checks passed"
	}
	return line, true
}

// runClosedDetail is where a closed run went: "to main 017b2d0". Either
// half is left out when the event does not carry it.
func runClosedDetail(data map[string]any) string {
	target, _ := data["target"].(string)
	commit, _ := data["commit"].(string)
	if len(commit) > 7 {
		commit = commit[:7]
	}
	var parts []string
	if target = strings.TrimSpace(target); target != "" {
		parts = append(parts, "to "+target)
	}
	if commit = strings.TrimSpace(commit); commit != "" {
		parts = append(parts, commit)
	}
	return strings.Join(parts, " ")
}

// Project builds the Board from a snapshot and the run's events in sequence
// order. Project(s, a+b) equals Project(s, a).Tail(b) for any split.
func Project(s Snapshot, evs []events.Event) Board {
	b := Board{Run: RunHead{ID: s.RunID, State: s.Run.State, Total: len(s.Run.Tasks), TokenLimit: s.Run.Budget.Tokens.Plan, LaneCap: laneCap(s.Run.Settings), Driving: s.Driving, UserPaused: s.UserPaused}, PlanID: s.Run.PlanID, Budget: s.Run.Budget}
	if s.Run.State == marshal.AwaitingUser && s.Run.Pause != nil {
		b.Run.Paused, b.Run.Next = s.Run.Pause.Reason, slices.Clone(s.Run.Pause.Resolutions)
		if s.Run.Pause.Code == marshal.CodeVerificationStopped {
			if reason, ok := plainVerificationStop(s.Run.Pause.Reason); ok {
				b.Run.Paused, b.Run.Next = reason, []string{plainVerificationRetry}
			}
		}
		b.Run.PauseAction, b.Run.PauseTask, b.Run.PauseCode = s.Run.Pause.Action, s.Run.Pause.TaskID, s.Run.Pause.Code
		b.Run.PauseKind, b.Run.PauseUntil = s.Run.Pause.Kind, s.Run.Pause.Until
	}
	for _, t := range s.Run.Tasks {
		row := NewTaskRow(t, s.Run.Settings)
		if row.State == TaskWorking || row.State == TaskReview || row.State == TaskAccepted {
			b.Run.Running++
		}
		if row.State == TaskMerged {
			b.Run.Done++
		}
		b.Tasks = append(b.Tasks, row)
	}
	slices.SortStableFunc(b.Tasks, func(x, y TaskRow) int {
		return cmp.Or(cmp.Compare(stateClass[x.State], stateClass[y.State]), strings.Compare(x.ID, y.ID))
	})
	for _, w := range s.Workers {
		lane := NewLane(w, b.Tasks)
		b.Lanes = append(b.Lanes, lane)
		if lane.State != LaneIdle && lane.State != LaneParked {
			b.Run.ActiveLanes++
		}
	}
	for _, r := range s.Requests {
		b.Inbox = append(b.Inbox, NewInboxItem(r))
	}
	slices.SortStableFunc(b.Inbox, func(x, y InboxItem) int {
		return cmp.Compare(gateRank(x.Gate), gateRank(y.Gate))
	})
	for _, e := range evs {
		b.apply(e)
	}
	return b
}

func gateRank(g GateLevel) int {
	if g == GateTyped {
		return 0
	}
	return 1
}

// Tail applies events that arrived after the Board was projected. It returns
// a new Board and leaves the receiver untouched, so a Board already handed to
// the painter is never written under it.
func (b Board) Tail(evs []events.Event) Board {
	b.Tasks = slices.Clone(b.Tasks)
	b.Lanes = slices.Clone(b.Lanes)
	b.Activity = slices.Clone(b.Activity)
	b.Events = slices.Clone(b.Events)
	b.Stale = false
	for _, e := range evs {
		b.apply(e)
	}
	return b
}

// apply folds one event in place. Task state is the snapshot's and never
// moves here: amendments re-queue tasks without a task event, so folding
// events into state would resurrect states the run has left.
func (b *Board) apply(e events.Event) {
	if e.Sequence <= b.Seq {
		return
	}
	b.Seq = e.Sequence
	if e.RunID != b.Run.ID {
		return
	}
	b.Events = append(b.Events, e)
	switch e.Type {
	case events.EventTypeMarshalPlanApproved:
		if b.Run.Started.IsZero() {
			b.Run.Started = e.At
		}
	case events.EventTypeMarshalUsageCharged:
		b.charge(e)
	case events.EventTypeMarshalTaskDispatched:
		for i := range b.Lanes {
			if b.Lanes[i].TaskID == e.TaskID {
				b.Lanes[i].Started, b.Lanes[i].Tokens, b.Lanes[i].Recent = e.At, 0, [8]int64{}
			}
		}
		for i := range b.Tasks {
			if b.Tasks[i].ID == e.TaskID && b.Tasks[i].Began.IsZero() {
				b.Tasks[i].Began = e.At
			}
		}
	}
	if state, ok := eventStates[e.Type]; ok && e.TaskID != "" {
		for i := range b.Tasks {
			if b.Tasks[i].ID == e.TaskID && b.Tasks[i].State == state {
				b.Tasks[i].Since = e.At
			}
		}
	}
	if line, ok := NewActivityLine(e); ok {
		b.Activity = append([]ActivityLine{line}, b.Activity[:min(len(b.Activity), maxActivity-1)]...)
	}
}

// charge adds one usage charge. The run total is known only while every
// charge so far was known, as in the Marshal panel's budget line.
func (b *Board) charge(e events.Event) {
	known, _ := e.Data["usage_known"].(bool)
	tokens := number(e.Data["usage_units"])
	b.Run.Tokens.Known = known && (b.Run.Charges == 0 || b.Run.Tokens.Known)
	b.Run.Charges++
	if !known {
		tokens = 0
	}
	b.Run.Tokens.Value += tokens
	for i := range b.Lanes {
		l := &b.Lanes[i]
		if l.TaskID == e.TaskID && e.TaskID != "" {
			l.Tokens += tokens
			copy(l.Recent[:], l.Recent[1:])
			l.Recent[len(l.Recent)-1] = tokens
		}
	}
}

// number reads a JSON number: int64 when the event was built in process,
// float64 when it was decoded from the store.
func number(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}
