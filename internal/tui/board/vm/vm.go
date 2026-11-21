// Package vm is the Board's view model: everything the Board v2 screens show,
// as plain data. Renderers read a Board and never reach into the store, the
// runtime or tmux; collectors fill a Board from those sources. A zero value
// means "not known yet" and renders as a dash, so a screen can be drawn before
// every collector is wired.
package vm

import "time"

// Screen is which Board v2 layout is drawn.
type Screen string

const (
	ScreenReady    Screen = "ready"    // first open, no run yet
	ScreenPlanning Screen = "planning" // goal set, Marshal planning
	ScreenRunning  Screen = "running"  // command centre during a run
	ScreenDecision Screen = "decision" // a card that needs the owner sits over the Board
	ScreenPaused   Screen = "paused"   // a stop that needs one choice
	ScreenChat     Screen = "chat"     // the Marshal chat full screen (F11)
	ScreenSplit    Screen = "split"    // Marshal chat left, compact Board right
)

// Tone is the colour role of a value. Renderers map it to the theme.
type Tone string

const (
	ToneNone  Tone = ""
	ToneOK    Tone = "ok"
	ToneWarn  Tone = "warn"
	ToneBad   Tone = "bad"
	ToneInfo  Tone = "info"  // accent
	ToneMuted Tone = "muted" // faint
	ToneVio   Tone = "vio"   // review
)

// Board is one frame of data.
type Board struct {
	Screen   Screen
	Now      time.Time
	Top      TopBar
	NowLine  NowLine
	Marshal  Marshal
	Plan     Plan
	Lanes    Lanes
	Tasks    []Task
	Workers  []Worker
	Inbox    Inbox
	Activity []Event
	Steps    Steps
	Requests []Request
	Chat     Chat
	Ready    Readiness
	Decision *Decision // non-nil: the decision card is shown over the Board
	Paused   *Paused   // non-nil: the paused card is shown
	Keys     []Key
}

// TopBar is row 0 on every screen.
type TopBar struct {
	Version   string
	Project   string
	Run       string // empty: no run
	State     string // RUNNING, PLANNING, READY, YOUR TURN, 2 PAUSED ...
	StateTone Tone
	Workers   int // running worker processes
	Inbox     int
	Clock     time.Time
}

// NowLine is row 1: what the Marshal does, what the work does, what is
// needed from the owner. FromYou is never empty: "nothing" is an answer.
type NowLine struct {
	Marshal   string
	Work      string
	FromYou   string
	FromYouOK bool // true: nothing needed (green); false: owner action (yellow)
}

// Marshal is the Marshal pane and the Marshal segment of the top bar.
type Marshal struct {
	Provider      string // claude, codex, agy, opencode
	Model         string // as the provider names it
	Effort        string // low, medium, high ... empty when the provider has none
	InUse         bool
	Doing         string
	ContextPct    int // -1 unknown
	ContextUsed   int // tokens
	ContextWindow int
	Limit5hPct    int // -1 unknown; percent left
	WeeklyPct     int // -1 unknown; percent left
	ChatBound     bool
	InputClear    bool
	// InputKnown is set once the chat's input box was looked at; until
	// then the input is shown as nothing, neither clear nor waiting.
	InputKnown bool
}

// Stage is one step of the plan pipeline.
type Stage struct {
	Name  string
	State string // done, current, todo
	Note  string // "5/12", "6m"
}

// Plan is the Plan pane.
type Plan struct {
	Title                        string // "12 tasks · 18 requirements"
	Accepted                     string // "accepted rev 1 · 18:52"
	Stages                       []Stage
	Done, Active, Waiting, Total int
	ETA                          time.Duration // 0 unknown
	Throughput                   []int         // merges per 5 minutes, oldest first
	MergedPerHr                  int
	Reworked                     int
	Tokens                       int64
}

// Lane is one worker lane, from heartbeats.
type Lane struct {
	Name       string // claude-1
	Provider   string
	Model      string
	Task       string // "T03 sub-view external links" or "review T02 (by agy-1)"
	Step       string // editing, tests, reviewing
	Reviewing  bool
	Elapsed    time.Duration
	LastSignal time.Duration // since the last heartbeat; Stale beyond 60s
	Stale      bool
	Selected   bool
}

// Lanes is the Lanes pane.
type Lanes struct {
	Items  []Lane
	Limits string // "limit claude 4 · agy 4 · total 12"
}

// Task is one plan task row.
type Task struct {
	ID, Title, Files, Writer, Reviewer string
	State                              string // merged, review, building, waiting
	Note                               string // "waits T03"
}

// Worker is one provider row of the Workers pane.
type Worker struct {
	Provider  string
	Glyph     string
	LeftPct   int    // -1 unknown
	Tag       string // "4 lanes", "spare", "in use", "not installed"
	TagTone   Tone
	Detail    string // model or "wk 11%"
	SignedIn  bool
	Installed bool
	Reason    string // plain cause when not usable: "not signed in: run agy once"
}

// Inbox is the Inbox pane.
type Inbox struct {
	Count int
	Lines []string // empty: "Nothing waits for you."
	Next  string
	Refs  []string // per line, what a click on it reaches ("" for nothing); the collector's own ids
}

// Event is one Activity row. Equal events within a window fold into one with Count.
type Event struct {
	At    time.Time
	Kind  string // merge, review, signal, plan, arch, memory, note, error
	Text  string
	Tone  Tone
	Count int // >1 shows ×N
	Last  time.Time
}

// Step is one Marshal step taken from its tool calls.
type Step struct {
	At   time.Time
	Kind string // read, check, draft, assign, start
	Text string
}

// Steps is the Marshal steps pane.
type Steps struct {
	Items []Step
	Quiet time.Duration // since the last tool call
	Model string        // the model planning runs on
}

// Request is one request the Board sent to the Marshal chat.
type Request struct {
	Sent    time.Time
	What    string
	Answer  string // allow, decline, pending, expired
	Latency time.Duration
	Tone    Tone
}

// Chat is the Marshal chat state.
type Chat struct {
	Bound      bool
	Restarts   int
	ReboundAt  time.Time
	Resent     int
	InputClear bool
	InputKnown bool // InputClear means something only when set
	Answered   int
	Stage      string
}

// Check is one readiness row.
type Check struct {
	Mark  string // ok, warn, bad
	Label string
	Text  string
	Tag   string
	Hint  string // second line under a warning
}

// Readiness is the first-open checklist.
type Readiness struct {
	Checks       []Check
	Ready, Total int
	TelemetryOn  bool
	TelemetryAsk bool
	Defaults     [][2]string
	Checked      time.Time // when the checks last ran; zero: not yet
	Actions      []Key     // buttons under the checks: fixes before ⏎, ⏎ the main one, then the rest
}

// Node is one part on the decision card's architecture sketch.
type Node struct {
	Name, Sub string
	Changed   bool
}

// Decision is the card over the Board when the owner must act.
type Decision struct {
	Kind         string // architecture, plan, inbox: what the card decides
	Title        string // "Accept architecture · rev 1"
	Summary      string
	ChangedNote  string
	Nodes        []Node
	Link         string // shown when no browser could open
	NoDisplay    bool
	SelfApproval string // non-empty: the Marshal tried to approve for the owner
	Actions      []Key
	Links        []Link // arrows between Nodes on the sketch (4 per row)
}

// Link is one arrow between two Nodes of the decision sketch, by index.
// Only neighbours in a row or a column are drawn.
type Link struct {
	From, To int
	Both     bool // ◀─▶ rather than ──▶
	Changed  bool // dashed: the goal changes this link
}

// PausedTask is one task a stop paused.
type PausedTask struct{ Title, Progress, Kept string }

// Paused is the one-choice recovery card.
type Paused struct {
	Kind    string // quota, auth, orphan, timeout, lease, interrupted, stalled, stopped, other
	Title   string // "Paused · agy Gemini 5h limit"
	Resets  string
	Cause   string
	Tasks   []PausedTask
	Will    string // what Enter does; a second line after "\n" is drawn dim
	Actions []Key
	Room    string // under Providers: whether the fallback has room for the moved work
	Run     string // under the Run bar: "6 lanes running · no other stops"
}

// Key is one key hint.
type Key struct{ Key, Label string }
