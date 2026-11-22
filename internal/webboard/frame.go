// Package webboard serves the Board v2 view model to a local browser: a
// loopback-only page that shows what the terminal Board shows and, behind
// the same private link, lets the owner press the buttons of the card on
// screen (act.go).
//
// The wire form is its own set of structs, not vm.Board with JSON tags, so
// a field added to the view model reaches the browser only when it is
// mapped here on purpose (TestFrameMapsEveryField pins the list). Links that
// carry a private token (Decision.Link) and the collectors' internal ids
// (Refs, Ref) are never sent.
package webboard

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/redaction"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// Version is the frame format version, the "v" field of every frame.
const Version = 1

// Frame is one Board frame as the browser reads it. Times are RFC 3339 with
// their zone ("" when unknown), durations whole seconds (0 when unknown).
type Frame struct {
	V       int      `json:"v"`
	Screen  string   `json:"screen"`
	Now     string   `json:"now"`
	Top     TopBar   `json:"top"`
	NowLine NowLine  `json:"nowLine"`
	Marshal Marshal  `json:"marshal"`
	Plan    Plan     `json:"plan"`
	Lanes   Lanes    `json:"lanes"`
	Tasks   []Task   `json:"tasks"`
	Workers []Worker `json:"workers"`
	// Limits is every provider's limit rows and dispatch state; added in
	// version 1 without a version change (a reader that ignores it is
	// unaffected).
	Limits   []ProviderLimits `json:"limits"`
	Inbox    Inbox            `json:"inbox"`
	Activity []Event          `json:"activity"`
	Steps    Steps            `json:"steps"`
	Requests []Request        `json:"requests"`
	Chat     Chat             `json:"chat"`
	Ready    Readiness        `json:"ready"`
	Decision *Decision        `json:"decision"`
	Paused   *Paused          `json:"paused"`
	Keys     []Key            `json:"keys"`
	System   System           `json:"system"`
	// MarshalSays is the Marshal's last chat turn to the owner, scrubbed
	// and capped (marshalSays).
	MarshalSays MarshalSays `json:"marshalSays"`
	// Act is the owner actions the page may send now (phase 2); nil on a
	// read-only server. It is minted by the server, not read from vm.
	Act *Act `json:"act,omitempty"`
}

// MarshalSays is the Marshal's last chat turn to the owner: Text is at most
// SaysMax characters (Truncated when cut), Answered once someone wrote in
// the chat after it. Whether it asks the owner something is
// NowLine.ChatAsked, not read from this text.
type MarshalSays struct {
	Text      string `json:"text"`
	At        string `json:"at"`
	Answered  bool   `json:"answered"`
	Truncated bool   `json:"truncated"`
}

// SaysMax is the most of the Marshal's message a frame carries, in
// characters; the rest is read in the terminal chat.
const SaysMax = 1200

// marshalSays is m in its wire form: every secret the detectors know
// redacted (again: the collector did it once), then capped.
func marshalSays(m vm.MarshalSays) MarshalSays {
	text := strings.TrimSpace(redaction.RedactAll(m.Text))
	out := MarshalSays{At: stamp(m.At), Answered: m.Answered}
	if r := []rune(text); len(r) > SaysMax {
		text, out.Truncated = strings.TrimSpace(string(r[:SaysMax]))+"…", true
	}
	out.Text = text
	return out
}

// System is the computer and the install; figures are -1 when unknown.
type System struct {
	CPUPct          int    `json:"cpuPct"`
	MemPct          int    `json:"memPct"`
	DiskPct         int    `json:"diskPct"`
	TempC           int    `json:"tempC"`
	LowDisk         bool   `json:"lowDisk"`
	ThermalGuard    bool   `json:"thermalGuard"`
	ThermalCap      int    `json:"thermalCap"`
	Notice          string `json:"notice"`
	UpdateAvailable string `json:"updateAvailable"`
	MCPFailed       bool   `json:"mcpFailed"`
}

type TopBar struct {
	Version   string `json:"version"`
	Project   string `json:"project"`
	Run       string `json:"run"`
	State     string `json:"state"`
	StateTone string `json:"stateTone"`
	Workers   int    `json:"workers"`
	Inbox     int    `json:"inbox"`
	Clock     string `json:"clock"`
}

type NowLine struct {
	Marshal   string `json:"marshal"`
	Work      string `json:"work"`
	FromYou   string `json:"fromYou"`
	FromYouOK bool   `json:"fromYouOK"`
	// ChatAsked: FromYou is the Marshal's question in its chat (the one
	// source of "the Marshal asked you something", vm.NowLine.ChatAsked).
	ChatAsked bool `json:"chatAsked"`
}

type Marshal struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Effort        string `json:"effort"`
	InUse         bool   `json:"inUse"`
	Doing         string `json:"doing"`
	ContextPct    int    `json:"contextPct"`
	ContextUsed   int    `json:"contextUsed"`
	ContextWindow int    `json:"contextWindow"`
	Limit5hPct    int    `json:"limit5hPct"`
	WeeklyPct     int    `json:"weeklyPct"`
	ChatBound     bool   `json:"chatBound"`
	InputClear    bool   `json:"inputClear"`
	InputKnown    bool   `json:"inputKnown"`
}

type Stage struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Note  string `json:"note"`
}

type Plan struct {
	Title       string  `json:"title"`
	Accepted    string  `json:"accepted"`
	Stages      []Stage `json:"stages"`
	Done        int     `json:"done"`
	Active      int     `json:"active"`
	Waiting     int     `json:"waiting"`
	Total       int     `json:"total"`
	ETA         int64   `json:"etaSec"`
	Throughput  []int   `json:"throughput"`
	MergedPerHr int     `json:"mergedPerHr"`
	Reworked    int     `json:"reworked"`
	Tokens      int64   `json:"tokens"`
}

type Lane struct {
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Task       string `json:"task"`
	Step       string `json:"step"`
	Reviewing  bool   `json:"reviewing"`
	Elapsed    int64  `json:"elapsedSec"`
	LastSignal int64  `json:"lastSignalSec"`
	Stale      bool   `json:"stale"`
	Selected   bool   `json:"selected"`
}

type Lanes struct {
	Items  []Lane `json:"items"`
	Limits string `json:"limits"`
}

type Task struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Files    string `json:"files"`
	Writer   string `json:"writer"`
	Reviewer string `json:"reviewer"`
	State    string `json:"state"`
	Note     string `json:"note"`
}

type Worker struct {
	Provider  string `json:"provider"`
	Glyph     string `json:"glyph"`
	LeftPct   int    `json:"leftPct"`
	Tag       string `json:"tag"`
	TagTone   string `json:"tagTone"`
	Detail    string `json:"detail"`
	SignedIn  bool   `json:"signedIn"`
	Installed bool   `json:"installed"`
	Reason    string `json:"reason"`
}

// ProviderLimits is one provider's limits. State is "usable", "out", "not
// signed in", "not checked" or "not installed"; the rows with drives set
// decide it.
type ProviderLimits struct {
	Provider string     `json:"provider"`
	Model    string     `json:"model"`
	State    string     `json:"state"`
	Reason   string     `json:"reason"`
	Rows     []LimitRow `json:"rows"`
}

// LimitRow is one limit window. percentLeft is -1 when the provider does not
// report it (source says why): the page draws no bar for it. resetsAt and
// observedAt are RFC 3339 with their zone, "" when unknown.
type LimitRow struct {
	Family      string `json:"family"`
	Window      string `json:"window"`
	PercentLeft int    `json:"percentLeft"`
	ResetsAt    string `json:"resetsAt"`
	ObservedAt  string `json:"observedAt"`
	Source      string `json:"source"`
	Drives      bool   `json:"drives"`
}

type Inbox struct {
	Count int      `json:"count"`
	Lines []string `json:"lines"`
	Next  string   `json:"next"`
}

type Event struct {
	At    string `json:"at"`
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	Tone  string `json:"tone"`
	Count int    `json:"count"`
	Last  string `json:"last"`
}

type Step struct {
	At   string `json:"at"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type Steps struct {
	Items []Step `json:"items"`
	Quiet int64  `json:"quietSec"`
	Model string `json:"model"`
}

type Request struct {
	Sent    string `json:"sent"`
	What    string `json:"what"`
	Answer  string `json:"answer"`
	Latency int64  `json:"latencySec"`
	Tone    string `json:"tone"`
}

type Chat struct {
	Bound      bool   `json:"bound"`
	Restarts   int    `json:"restarts"`
	ReboundAt  string `json:"reboundAt"`
	Resent     int    `json:"resent"`
	InputClear bool   `json:"inputClear"`
	InputKnown bool   `json:"inputKnown"`
	Answered   int    `json:"answered"`
	Stage      string `json:"stage"`
}

type Check struct {
	Mark  string `json:"mark"`
	Label string `json:"label"`
	Text  string `json:"text"`
	Tag   string `json:"tag"`
	Hint  string `json:"hint"`
}

type Setting struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Readiness struct {
	Checks       []Check   `json:"checks"`
	Ready        int       `json:"ready"`
	Total        int       `json:"total"`
	TelemetryOn  bool      `json:"telemetryOn"`
	TelemetryAsk bool      `json:"telemetryAsk"`
	Defaults     []Setting `json:"defaults"`
	Checked      string    `json:"checked"`
	Actions      []Key     `json:"actions"`
}

type Node struct {
	Name    string `json:"name"`
	Sub     string `json:"sub"`
	Changed bool   `json:"changed"`
}

type CardLine struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Text  string `json:"text"`
	Note  string `json:"note"`
	Tone  string `json:"tone"`
}

type Link struct {
	From    int  `json:"from"`
	To      int  `json:"to"`
	Both    bool `json:"both"`
	Changed bool `json:"changed"`
}

// Decision is the owner card. The review link is never sent: it carries the
// architecture review's private token, which is the owner's Accept.
type Decision struct {
	Kind         string     `json:"kind"`
	Title        string     `json:"title"`
	Corner       string     `json:"corner"`
	Summary      string     `json:"summary"`
	ChangedNote  string     `json:"changedNote"`
	Nodes        []Node     `json:"nodes"`
	Lines        []CardLine `json:"lines"`
	HasLink      bool       `json:"hasLink"`
	NoDisplay    bool       `json:"noDisplay"`
	SelfApproval string     `json:"selfApproval"`
	Actions      []Key      `json:"actions"`
	Links        []Link     `json:"links"`
}

type PausedTask struct {
	Title    string `json:"title"`
	Progress string `json:"progress"`
	Kept     string `json:"kept"`
}

type Paused struct {
	Kind    string       `json:"kind"`
	Title   string       `json:"title"`
	Resets  string       `json:"resets"`
	Cause   string       `json:"cause"`
	Tasks   []PausedTask `json:"tasks"`
	Will    string       `json:"will"`
	Actions []Key        `json:"actions"`
	Room    string       `json:"room"`
	Run     string       `json:"run"`
}

type Key struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func secs(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64(d / time.Second)
}

func keys(in []vm.Key) []Key {
	out := make([]Key, 0, len(in))
	for _, k := range in {
		out = append(out, Key{Key: k.Key, Label: k.Label})
	}
	return out
}

func limits(in []vm.ProviderLimits) []ProviderLimits {
	out := make([]ProviderLimits, 0, len(in))
	for _, p := range in {
		l := ProviderLimits{Provider: p.Provider, Model: p.Model, State: string(p.State), Reason: p.Reason, Rows: make([]LimitRow, 0, len(p.Rows))}
		for _, r := range p.Rows {
			l.Rows = append(l.Rows, LimitRow{Family: r.Family, Window: r.Window, PercentLeft: r.PercentLeft, ResetsAt: stamp(r.ResetsAt),
				ObservedAt: stamp(r.ObservedAt), Source: r.Source, Drives: r.Drives})
		}
		out = append(out, l)
	}
	return out
}

// FromBoard is b in its wire form. Every slice is non-nil, so the page reads
// an empty list, never null.
func FromBoard(b vm.Board) Frame {
	f := Frame{
		V: Version, Screen: string(b.Screen), Now: stamp(b.Now),
		Top: TopBar{Version: b.Top.Version, Project: b.Top.Project, Run: b.Top.Run, State: b.Top.State, StateTone: string(b.Top.StateTone),
			Workers: b.Top.Workers, Inbox: b.Top.Inbox, Clock: stamp(b.Top.Clock)},
		NowLine: NowLine{Marshal: b.NowLine.Marshal, Work: b.NowLine.Work, FromYou: b.NowLine.FromYou, FromYouOK: b.NowLine.FromYouOK, ChatAsked: b.NowLine.ChatAsked},
		Marshal: Marshal{Provider: b.Marshal.Provider, Model: b.Marshal.Model, Effort: b.Marshal.Effort, InUse: b.Marshal.InUse, Doing: b.Marshal.Doing,
			ContextPct: b.Marshal.ContextPct, ContextUsed: b.Marshal.ContextUsed, ContextWindow: b.Marshal.ContextWindow,
			Limit5hPct: b.Marshal.Limit5hPct, WeeklyPct: b.Marshal.WeeklyPct, ChatBound: b.Marshal.ChatBound,
			InputClear: b.Marshal.InputClear, InputKnown: b.Marshal.InputKnown},
		Plan: Plan{Title: b.Plan.Title, Accepted: b.Plan.Accepted, Stages: []Stage{}, Done: b.Plan.Done, Active: b.Plan.Active,
			Waiting: b.Plan.Waiting, Total: b.Plan.Total, ETA: secs(b.Plan.ETA), Throughput: append([]int{}, b.Plan.Throughput...),
			MergedPerHr: b.Plan.MergedPerHr, Reworked: b.Plan.Reworked, Tokens: b.Plan.Tokens},
		Lanes:    Lanes{Items: []Lane{}, Limits: b.Lanes.Limits},
		Tasks:    []Task{},
		Workers:  []Worker{},
		Limits:   limits(b.Limits),
		Inbox:    Inbox{Count: b.Inbox.Count, Lines: append([]string{}, b.Inbox.Lines...), Next: b.Inbox.Next},
		Activity: []Event{},
		Steps:    Steps{Items: []Step{}, Quiet: secs(b.Steps.Quiet), Model: b.Steps.Model},
		Requests: []Request{},
		Chat: Chat{Bound: b.Chat.Bound, Restarts: b.Chat.Restarts, ReboundAt: stamp(b.Chat.ReboundAt), Resent: b.Chat.Resent,
			InputClear: b.Chat.InputClear, InputKnown: b.Chat.InputKnown, Answered: b.Chat.Answered, Stage: b.Chat.Stage},
		Ready: Readiness{Checks: []Check{}, Ready: b.Ready.Ready, Total: b.Ready.Total, TelemetryOn: b.Ready.TelemetryOn,
			TelemetryAsk: b.Ready.TelemetryAsk, Defaults: []Setting{}, Checked: stamp(b.Ready.Checked), Actions: keys(b.Ready.Actions)},
		Keys: keys(b.Keys),
		System: System{CPUPct: b.System.CPUPct, MemPct: b.System.MemPct, DiskPct: b.System.DiskPct, TempC: b.System.TempC,
			LowDisk: b.System.LowDisk, ThermalGuard: b.System.ThermalGuard, ThermalCap: b.System.ThermalCap, Notice: b.System.Notice,
			UpdateAvailable: b.System.UpdateAvailable, MCPFailed: b.System.MCPFailed},
		MarshalSays: marshalSays(b.MarshalSays),
	}
	for _, s := range b.Plan.Stages {
		f.Plan.Stages = append(f.Plan.Stages, Stage{Name: s.Name, State: s.State, Note: s.Note})
	}
	for _, l := range b.Lanes.Items {
		f.Lanes.Items = append(f.Lanes.Items, Lane{Name: l.Name, Provider: l.Provider, Model: l.Model, Task: l.Task, Step: l.Step,
			Reviewing: l.Reviewing, Elapsed: secs(l.Elapsed), LastSignal: secs(l.LastSignal), Stale: l.Stale, Selected: l.Selected})
	}
	for _, t := range b.Tasks {
		f.Tasks = append(f.Tasks, Task{ID: t.ID, Title: t.Title, Files: t.Files, Writer: t.Writer, Reviewer: t.Reviewer, State: t.State, Note: t.Note})
	}
	for _, w := range b.Workers {
		f.Workers = append(f.Workers, Worker{Provider: w.Provider, Glyph: w.Glyph, LeftPct: w.LeftPct, Tag: w.Tag, TagTone: string(w.TagTone),
			Detail: w.Detail, SignedIn: w.SignedIn, Installed: w.Installed, Reason: w.Reason})
	}
	for _, e := range b.Activity {
		f.Activity = append(f.Activity, Event{At: stamp(e.At), Kind: e.Kind, Text: e.Text, Tone: string(e.Tone), Count: e.Count, Last: stamp(e.Last)})
	}
	for _, s := range b.Steps.Items {
		f.Steps.Items = append(f.Steps.Items, Step{At: stamp(s.At), Kind: s.Kind, Text: s.Text})
	}
	for _, r := range b.Requests {
		f.Requests = append(f.Requests, Request{Sent: stamp(r.Sent), What: r.What, Answer: r.Answer, Latency: secs(r.Latency), Tone: string(r.Tone)})
	}
	for _, c := range b.Ready.Checks {
		f.Ready.Checks = append(f.Ready.Checks, Check{Mark: c.Mark, Label: c.Label, Text: c.Text, Tag: c.Tag, Hint: c.Hint})
	}
	for _, d := range b.Ready.Defaults {
		f.Ready.Defaults = append(f.Ready.Defaults, Setting{Name: d[0], Value: d[1]})
	}
	if d := b.Decision; d != nil {
		c := &Decision{Kind: d.Kind, Title: d.Title, Corner: d.Corner, Summary: d.Summary, ChangedNote: d.ChangedNote,
			Nodes: []Node{}, Lines: []CardLine{}, HasLink: d.Link != "", NoDisplay: d.NoDisplay, SelfApproval: d.SelfApproval,
			Actions: keys(d.Actions), Links: []Link{}}
		for _, n := range d.Nodes {
			c.Nodes = append(c.Nodes, Node{Name: n.Name, Sub: n.Sub, Changed: n.Changed})
		}
		for _, l := range d.Lines {
			c.Lines = append(c.Lines, CardLine{Key: l.Key, Label: l.Label, Text: l.Text, Note: l.Note, Tone: string(l.Tone)})
		}
		for _, l := range d.Links {
			c.Links = append(c.Links, Link{From: l.From, To: l.To, Both: l.Both, Changed: l.Changed})
		}
		f.Decision = c
	}
	if p := b.Paused; p != nil {
		c := &Paused{Kind: p.Kind, Title: p.Title, Resets: p.Resets, Cause: p.Cause, Tasks: []PausedTask{}, Will: p.Will,
			Actions: keys(p.Actions), Room: p.Room, Run: p.Run}
		for _, t := range p.Tasks {
			c.Tasks = append(c.Tasks, PausedTask{Title: t.Title, Progress: t.Progress, Kept: t.Kept})
		}
		f.Paused = c
	}
	return f
}

// tokenParam is a private link's key in free text ("?token=…", "&t=…"),
// as JSON writes it (& is \u0026).
var tokenParam = regexp.MustCompile(`((?:[?&]|\\u0026)(?:token|t)=)[A-Za-z0-9_.~%-]+`)

// Encode is b as JSON in the version 1 frame format. A private link's key
// that free text carries (an Activity line, a card line) is cut out.
func Encode(b vm.Board) ([]byte, error) {
	return encodeFrame(FromBoard(b))
}

// encodeFrame is f as JSON with private link keys cut out of free text.
func encodeFrame(f Frame) ([]byte, error) {
	body, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	return tokenParam.ReplaceAll(body, []byte("${1}…")), nil
}

// changeKey is the frame with the clocks cleared: two frames with the same
// key show the same thing, so no new frame is pushed for a clock tick alone.
// The page ticks its own clock.
func changeKey(b vm.Board) ([]byte, error) {
	b.Now, b.Top.Clock = time.Time{}, time.Time{}
	return Encode(b)
}
