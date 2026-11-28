package tui

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/board/vm"
	"github.com/Zen1th53/marshal/internal/marshalsteps"
)

// The Marshal's steps, from its tool calls. Every call the Marshal makes
// through the proposal broker (proposal_mcp_extreme.go) notes one short
// plain line, and so does the Marshal's MCP child for the tools it answers
// itself (internal/mcp): which tool and a few words about it, never what was
// sent. Both append to the step log in the state directory
// (internal/marshalsteps), which this collector tails, so the steps of a
// Marshal an earlier Board served, or that its MCP child served, still
// show. The Board v2 steps pane shows them, newest first, with how long the
// Marshal has been quiet.

// marshalStepLimit is how many steps are kept.
const marshalStepLimit = 32

// marshalStep is one noted tool call; Count folds the same step repeated
// back to back (a paged read, a polled receipt).
type marshalStep struct {
	At    time.Time
	Kind  string // read, check, submit, decide, assign, start
	Text  string
	Count int
}

// marshalSteps is the steps of this workspace's Marshal sessions.
type marshalSteps struct {
	mu       sync.Mutex
	items    []marshalStep // newest first
	last     time.Time     // the last tool call, shown or not
	lease    string        // the broker lease of the last call
	provider string        // the provider that lease runs on
	// path is the step log, "" while the workspace has no state directory
	// (then steps are kept in memory only); tail reads what was added to it.
	path   string
	tail   *marshalsteps.Tail
	loaded bool
}

// bind points the steps at the step log at path. A new path drops what was
// read from the old one.
func (s *marshalSteps) bind(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if path == s.path {
		return
	}
	s.path, s.tail, s.loaded = path, nil, false
	s.items, s.last = nil, time.Time{}
	if path != "" {
		s.tail = marshalsteps.NewTail(path)
	}
}

// record notes one broker call already named: kind and text, "" for one
// that adds no line.
func (s *marshalSteps) record(lease, provider, kind, text string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var lines []marshalsteps.Line
	if lease != s.lease {
		s.lease = lease
		lines = append(lines, marshalsteps.Line{At: at, Kind: "start", Text: "session on " + providerLabel(provider), Provider: provider})
	}
	if text == "" {
		kind = ""
	}
	lines = append(lines, marshalsteps.Line{At: at, Kind: kind, Text: text})
	var unlogged []marshalsteps.Line
	for _, l := range lines {
		if s.path == "" || marshalsteps.Append(s.path, l) != nil {
			unlogged = append(unlogged, l)
		}
	}
	s.refreshLocked()
	for _, l := range unlogged {
		s.apply(l)
	}
}

// refresh reads what was added to the step log since the last look. The
// chat window's monitor calls it every tick; a frame never does.
func (s *marshalSteps) refresh() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
}

// refreshLocked is refresh with s.mu held.
func (s *marshalSteps) refreshLocked() {
	if s.tail == nil {
		return
	}
	s.loaded = true
	lines, reset, err := s.tail.Read()
	if err != nil {
		return
	}
	if reset {
		s.items, s.last = nil, time.Time{}
	}
	for _, l := range lines {
		s.apply(l)
	}
}

// apply folds one logged line into the steps. Callers hold s.mu.
func (s *marshalSteps) apply(l marshalsteps.Line) {
	if l.At.After(s.last) {
		s.last = l.At
	}
	if l.Kind == "start" && l.Provider != "" {
		s.provider = l.Provider
	}
	if l.Text == "" {
		return
	}
	s.add(marshalStep{At: l.At, Kind: l.Kind, Text: l.Text})
}

// add puts st first, folding it into the newest step when it repeats it.
// Callers hold s.mu.
func (s *marshalSteps) add(st marshalStep) {
	if len(s.items) > 0 && s.items[0].Kind == st.Kind && s.items[0].Text == st.Text {
		s.items[0].At = st.At
		s.items[0].Count++
		return
	}
	st.Count = 1
	s.items = append([]marshalStep{st}, s.items...)
	s.items = s.items[:min(len(s.items), marshalStepLimit)]
}

// snapshot is the steps for a frame, the last call and its provider. The
// first snapshot reads the step log once, so a Board that starts shows the
// steps before the monitor's first tick; later ones read only memory.
func (s *marshalSteps) snapshot() ([]marshalStep, time.Time, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		s.refreshLocked()
	}
	return append([]marshalStep(nil), s.items...), s.last, s.provider
}

// stepsLog is the workspace's steps, bound to the step log in its state
// directory. It reads w.runtime under w.mu, so callers hold neither w.mu
// nor the proposal broker's mutex.
func (w *Workspace) stepsLog() *marshalSteps {
	w.mu.RLock()
	rt := w.runtime
	w.mu.RUnlock()
	path := ""
	if rt != nil {
		if dir := rt.StateDirectory(); dir != "" {
			path = marshalsteps.Path(dir)
		}
	}
	w.marshalSteps.bind(path)
	return &w.marshalSteps
}

// providerLabel is a provider as a step names it.
func providerLabel(provider string) string {
	if p := providerOf(provider); p != "" {
		return p
	}
	if provider = strings.TrimSpace(provider); provider != "" {
		return provider
	}
	return "the Marshal's provider"
}

// brokerSteps names each broker method's step. A method missing here is
// noted as a check of that method's name; receipts and aborts are polling
// and add no line.
var brokerSteps = map[string][2]string{
	"project_contract_context":       {"read", "architecture context"},
	"project_contract_read":          {"read", "accepted architecture"},
	"project_contract_inventory":     {"read", "memory inventory"},
	"project_contract_at_commit":     {"read", "architecture at a commit"},
	"project_contract_history":       {"read", "architecture history"},
	"project_contract_conformance":   {"check", "architecture conformance"},
	"project_contract_submit":        {"submit", "architecture"},
	"architecture_request_answer":    {"check", "answered an architecture request"},
	"memory_submit":                  {"submit", "memory note"},
	"project_contract_receipt":       {},
	"project_contract_context_abort": {},
	"memory_receipt":                 {},
	"receipt":                        {},
}

// proposalSteps names a submitted proposal's step by its action.
var proposalSteps = map[string][2]string{
	"goal":             {"submit", "goal"},
	"intake":           {"submit", "intake"},
	"ask":              {"submit", "a question for you"},
	"requirements":     {"submit", "requirements"},
	"architecture":     {"submit", "architecture"},
	"amend":            {"submit", "a plan change"},
	"read":             {"read", "a read request"},
	"continue":         {"read", "continue an earlier chat"},
	"setting":          {"assign", "a run setting"},
	"hold":             {"assign", "hold tasks"},
	"release":          {"assign", "release held tasks"},
	"reassign":         {"assign", "move a task to another worker"},
	"harness-verify":   {"check", "verify a provider"},
	"accept":           {"check", "accept a task"},
	"return":           {"check", "return a task"},
	"memory":           {"check", "keep a memory"},
	"approve":          {"check", "approve"},
	"amend-approve":    {"check", "approve a plan change"},
	"amend-deny":       {"check", "decline a plan change"},
	"decide":           {"decide", "a request"},
	"retry":            {"start", "retry a task"},
	"resume":           {"start", "resume the run"},
	"deliver":          {"start", "deliver the run"},
	"close":            {"start", "close the run"},
	"project-contract": {"submit", "architecture"},
}

// stepOf is a broker call's kind and line, "" for a call that adds none.
// Only the method, a submitted proposal's action, task, decision and
// contract revision, and the page numbers of a context reply (out, nil when
// the call failed or is not served yet) are read: never submitted text.
func stepOf(method string, raw, out json.RawMessage) (string, string) {
	switch method {
	case "submit":
		var args struct {
			Proposal struct {
				Action string `json:"action"`
				TaskID string `json:"task_id"`
				Allow  *bool  `json:"allow"`
			} `json:"proposal"`
		}
		_ = json.Unmarshal(raw, &args)
		action := strings.TrimSpace(args.Proposal.Action)
		st, ok := proposalSteps[action]
		if !ok {
			return "check", "proposal"
		}
		text := st[1]
		if id := cleanStepWord(args.Proposal.TaskID); id != "" {
			text += " " + id
		}
		if action == "decide" && args.Proposal.Allow != nil {
			text += " → " + decideWord(*args.Proposal.Allow)
		}
		return st[0], text
	case "project_contract_submit":
		var args struct {
			Proposal struct {
				Contract struct {
					Revision int `json:"revision"`
				} `json:"contract"`
			} `json:"proposal"`
		}
		_ = json.Unmarshal(raw, &args)
		if r := args.Proposal.Contract.Revision; r > 0 {
			return "submit", "architecture rev " + strconv.Itoa(r)
		}
	case "project_contract_context":
		var reply struct {
			Page  int `json:"page"`
			Pages int `json:"pages"`
		}
		if json.Unmarshal(out, &reply) == nil && reply.Pages > 1 && reply.Page > 0 {
			return "read", "architecture context page " + strconv.Itoa(reply.Page) + "/" + strconv.Itoa(reply.Pages)
		}
	}
	if st, ok := brokerSteps[method]; ok {
		return st[0], st[1]
	}
	if m := cleanStepWord(method); m != "" {
		return "check", strings.ReplaceAll(m, "_", " ")
	}
	return "", ""
}

func decideWord(allow bool) string {
	if allow {
		return "allow"
	}
	return "decline"
}

// noteBrokerStep notes a served broker call on the steps: a decision names
// the request it answered as the Board sent it ("memory import → allow"),
// and a refused call says so. It runs after the broker's mutex is released.
func (w *Workspace) noteBrokerStep(lease, provider, method string, raw, out json.RawMessage, err error) {
	steps := w.stepsLog()
	kind, text := stepOf(method, raw, out)
	if kind == "decide" {
		var args struct {
			Proposal struct {
				ID    string `json:"id"`
				Allow *bool  `json:"allow"`
			} `json:"proposal"`
		}
		_ = json.Unmarshal(raw, &args)
		if what := w.chatRequestWhat(args.Proposal.ID); what != "" && args.Proposal.Allow != nil {
			text = what + " → " + decideWord(*args.Proposal.Allow)
		}
	}
	if err != nil && text != "" {
		text += " · refused"
	}
	steps.record(lease, provider, kind, text, time.Now())
}

// cleanStepWord keeps an identifier short and printable for a step line.
func cleanStepWord(s string) string {
	s = strings.TrimSpace(cleanBoard(s))
	if strings.ContainsAny(s, " \t") || len(s) > 40 {
		return ""
	}
	return s
}

// stepsView is the steps pane from the noted steps at now. model is the
// model planning runs on; quiet counts from the last call, 0 when none.
func stepsView(items []marshalStep, last time.Time, model string, now time.Time) vm.Steps {
	out := vm.Steps{Model: model}
	if !last.IsZero() && now.After(last) {
		out.Quiet = now.Sub(last)
	}
	for _, st := range items {
		text := st.Text
		if st.Count > 1 {
			text += " ×" + strconv.Itoa(st.Count)
		}
		out.Items = append(out.Items, vm.Step{At: st.At, Kind: st.Kind, Text: text})
	}
	return out
}
