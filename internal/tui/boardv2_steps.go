package tui

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The Marshal's steps, from its tool calls. Every call the Marshal makes
// through the proposal broker (proposal_mcp_extreme.go) notes one short
// plain line here: which tool, never what was sent. The Board v2 steps pane
// shows them, newest first, with how long the Marshal has been quiet.

// marshalStepLimit is how many steps are kept.
const marshalStepLimit = 32

// marshalStep is one noted tool call; Count folds the same step repeated
// back to back (a paged read, a polled receipt).
type marshalStep struct {
	At    time.Time
	Kind  string // read, check, draft, assign, start
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
}

// note records one broker call. A call on a lease not seen before starts a
// session: that is noted first as a start step. Receipts and aborts are
// polling, so they move the quiet clock but add no line.
func (s *marshalSteps) note(lease, provider, method string, raw json.RawMessage, at time.Time) {
	kind, text := stepOf(method, raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = at
	if lease != s.lease {
		s.lease, s.provider = lease, provider
		s.add(marshalStep{At: at, Kind: "start", Text: "session on " + providerLabel(provider)})
	}
	if text == "" {
		return
	}
	s.add(marshalStep{At: at, Kind: kind, Text: text})
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

// snapshot is the steps for a frame, the last call and its provider.
func (s *marshalSteps) snapshot() ([]marshalStep, time.Time, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]marshalStep(nil), s.items...), s.last, s.provider
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
	"project_contract_context":       {"read", "project contract context"},
	"project_contract_read":          {"read", "accepted architecture"},
	"project_contract_inventory":     {"read", "project inventory"},
	"project_contract_at_commit":     {"read", "architecture at a commit"},
	"project_contract_history":       {"read", "architecture history"},
	"project_contract_conformance":   {"check", "architecture conformance"},
	"project_contract_submit":        {"draft", "architecture submitted for your review"},
	"architecture_request_answer":    {"check", "answered an architecture request"},
	"memory_submit":                  {"draft", "memory note submitted"},
	"project_contract_receipt":       {},
	"project_contract_context_abort": {},
	"memory_receipt":                 {},
	"receipt":                        {},
}

// proposalSteps names a submitted proposal's step by its action.
var proposalSteps = map[string][2]string{
	"intake":        {"draft", "intake"},
	"ask":           {"draft", "a question for you"},
	"requirements":  {"draft", "requirements"},
	"architecture":  {"draft", "architecture"},
	"amend":         {"draft", "a plan change"},
	"read":          {"read", "a read request"},
	"continue":      {"read", "continue an earlier chat"},
	"setting":       {"assign", "a run setting"},
	"hold":          {"assign", "hold tasks"},
	"release":       {"assign", "release held tasks"},
	"reassign":      {"assign", "move a task to another worker"},
	"accept":        {"check", "accept a task"},
	"return":        {"check", "return a task"},
	"memory":        {"check", "keep a memory"},
	"approve":       {"check", "approve"},
	"amend-approve": {"check", "approve a plan change"},
	"amend-deny":    {"check", "decline a plan change"},
	"decide":        {"check", "answered a request"},
	"retry":         {"start", "retry a task"},
	"resume":        {"start", "resume the run"},
	"deliver":       {"start", "deliver the run"},
	"close":         {"start", "close the run"},
}

// stepOf is a broker call's kind and line, "" for a call that adds none.
// Only the method and a submitted proposal's action and task are read.
func stepOf(method string, raw json.RawMessage) (string, string) {
	if method == "submit" {
		var args struct {
			Proposal struct {
				Action string `json:"action"`
				TaskID string `json:"task_id"`
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
		return st[0], text
	}
	if st, ok := brokerSteps[method]; ok {
		return st[0], st[1]
	}
	if m := cleanStepWord(method); m != "" {
		return "check", strings.ReplaceAll(m, "_", " ")
	}
	return "", ""
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
