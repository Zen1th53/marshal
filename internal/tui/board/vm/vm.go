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
	// LastRun marks a finished result shown while waiting for a new goal.
	LastRun  bool
	Screen   Screen
	Now      time.Time
	Top      TopBar
	NowLine  NowLine
	Marshal  Marshal
	Plan     Plan
	Lanes    Lanes
	Tasks    []Task
	Workers  []Worker
	Limits   []ProviderLimits
	Inbox    Inbox
	Activity []Event
	Steps    Steps
	Requests []Request
	Chat     Chat
	Ready    Readiness
	Decision *Decision // non-nil: the decision card is shown over the Board
	Paused   *Paused   // non-nil: the paused card is shown
	Keys     []Key
	System   System
	Web      Web
	// MarshalSays is the Marshal chat's last turn to the owner.
	MarshalSays MarshalSays
}

// MarshalSays is the final text of the Marshal's last chat turn: assistant
// prose only, secrets redacted. Answered is set once someone wrote in the
// chat after it. A zero value: the Marshal has not said anything yet.
type MarshalSays struct {
	Text     string
	At       time.Time
	Answered bool
}

// System is the computer and the install the Board runs on: host load, the
// thermal guard, a newer release, whether MCP works. Figures are -1 when
// unknown.
type System struct {
	CPUPct, MemPct, DiskPct int
	TempC                   int
	LowDisk                 bool
	ThermalGuard            bool   // the thermal rule is active
	ThermalCap              int    // lane cap the guard asked for while active; 0 none
	Notice                  string // the host watcher's notice: "host: thermal guard · …"
	UpdateAvailable         string // tag of a newer release; "" none or not checked
	MCPFailed               bool   // MCP failed in this session (the reason stays in the terminal, F6)
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
	// ChatAsked is set when FromYou is the Marshal's question in its chat:
	// its last turn ended asking the owner something and the chat's input
	// sits idle, so the answer is typed in the chat, not on a card.
	ChatAsked bool
}

// ChatAskedLine is FromYou while the Marshal waits on its question in chat.
const ChatAskedLine = "the Marshal asked you something in chat"

// Web is the Web Board's state, for the line that says where it is.
type Web struct {
	Running   bool // the server is up
	Opened    bool // the browser was opened on it
	NoDisplay bool // no display here: the link must be opened by hand (/marshal web)
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

// LimitState is whether the dispatcher sends a provider work, as its limits
// and sign-in say.
type LimitState string

const (
	LimitUsable       LimitState = "usable"        // takes work: the model it runs has limit left, or its limit is not reported
	LimitOut          LimitState = "out"           // the model it runs has no limit left, or the provider reported a limit
	LimitNotSignedIn  LimitState = "not signed in" // installed, but its CLI is signed out
	LimitNotChecked   LimitState = "not checked"   // not checked yet, or a check is running
	LimitNotInstalled LimitState = "not installed" // its CLI is not on PATH
)

// ProviderLimits is one provider's limits, one row per family and window,
// and its dispatch state with the rows that decide it.
type ProviderLimits struct {
	Provider string
	Model    string // the model id its workers run, which picks the rows that apply; "" unknown
	State    LimitState
	Reason   string // the plain cause of State; "" when usable and nothing to add
	Rows     []LimitRow
}

// LimitRow is one limit window as the provider reported it. Nothing is
// estimated: a limit the provider does not report has PercentLeft -1 and a
// Source that says so, and draws no bar.
type LimitRow struct {
	Family      string    // model family the limit covers ("Claude and GPT"); "" for every model
	Window      string    // "5h", "weekly" or another length ("90m"); "" when not reported
	PercentLeft int       // -1 not reported or unknown
	ResetsAt    time.Time // with its date; zero unknown
	ObservedAt  time.Time // when the provider wrote the figure; zero unknown
	Source      string    // "codex session log", "agy /usage", "agy models", "not reported by the CLI", "not checked"
	Drives      bool      // this row decides State: the tightest window of the model the provider runs, or one at 0%
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
	Kind         string // architecture, plan, inbox, ask, requirements, change, next, guard: what the card decides
	Title        string // "Accept architecture · rev 1"
	Corner       string // the card's top-right note; empty: "esc later"
	Summary      string
	ChangedNote  string
	Nodes        []Node
	Lines        []CardLine // what the owner reads before deciding, under the summary
	Link         string     // shown when no browser could open
	NoDisplay    bool
	SelfApproval string // non-empty: the Marshal tried to approve for the owner
	Actions      []Key
	Refs         []string // per action, what a click on it reaches ("" for its key); the collector's own ids
	Links        []Link   // arrows between Nodes on the sketch (4 per row)
}

// CardLine is one row of a card's content: a requirement line ("a", "scope",
// its text, "· its source"), a question, a changed file. Text wraps; Note is
// drawn dim after it when it fits.
type CardLine struct {
	Key, Label string
	Text, Note string
	Tone       Tone   // the text's colour; ToneMuted for a blank
	Ref        string // what a click on the row reaches, "" for nothing; the collector's own id
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
