package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The decision card: what the owner must decide before the run can move,
// over a dimmed Board (docs/design/board-v2/screen4.ans). It is read from
// the same canonical waits the inbox shows (board_gates.go, permissions.go):
// the architecture waiting for the owner's Accept, a drafted plan waiting
// for approval where the edition asks the owner, or a pending decision in
// the inbox. Its keys run the operations those entries already run
// (boardv2_cards_keys.go).

// Decision kinds.
const (
	decisionArchitecture = "architecture"
	decisionPlan         = "plan"
	decisionInbox        = "inbox"
)

// boardAcceptsArchitecture reports whether the current authority rules let
// the owner accept an architecture from the Board. They do not: the Accept
// binds the owner's instruction to the contract digest inside the browser
// review (browserAccepted), and an accept typed anywhere else is the
// Marshal's own and refused (noteRefusedArchitecture). So the card offers
// no Enter for it; when the rules change, this is the one switch.
const boardAcceptsArchitecture = false

// v2Decision is the decision card and what its keys act on.
type v2Decision struct {
	kind string
	key  string
	card vm.Decision
	gate ownerGate
	// link is the review's private address, "" when not known.
	link string
}

// v2Getenv is the environment the display check reads; tests replace it.
var v2Getenv = os.Getenv

// v2GOOS is the platform the display check reads; tests replace it.
var v2GOOS = runtime.GOOS

// headlessDisplay reports whether this machine has no screen a browser
// could open on: Linux (or another Unix desktop) with neither DISPLAY nor
// WAYLAND_DISPLAY set. macOS and Windows always have one.
func headlessDisplay() bool {
	switch v2GOOS {
	case "darwin", "windows", "ios", "android":
		return false
	}
	return strings.TrimSpace(v2Getenv("DISPLAY")) == "" && strings.TrimSpace(v2Getenv("WAYLAND_DISPLAY")) == ""
}

// browserOpenFailed reports whether the last try to open the architecture
// in a browser failed: the footer still says so, or the newest Activity
// line about opening it says it could not.
func (w *Workspace) browserOpenFailed() bool {
	w.mu.RLock()
	answer := w.answer
	w.mu.RUnlock()
	if answer == architectureNoBrowser {
		return true
	}
	for _, n := range w.noteList() {
		switch {
		case strings.HasPrefix(n.Text, "Architecture view: your browser could not be opened"):
			return true
		case strings.HasPrefix(n.Text, "Architecture view opened in your browser"):
			return false
		}
	}
	return false
}

// architectureLink is the review page's private address: the link the
// review server wrote (.marshal/architecture/review/url.txt), or the live
// session's own. "" when no review page is up.
func (w *Workspace) architectureLink() string {
	w.mu.RLock()
	rt, root := w.runtime, w.workDir
	w.mu.RUnlock()
	if rt != nil && rt.ProjectRoot() != "" {
		root = rt.ProjectRoot()
	}
	if root != "" {
		if data, err := readSmallFile(filepath.Join(root, architectureURLFile), 4096); err == nil {
			if link := strings.TrimSpace(string(data)); strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
				return link
			}
		}
	}
	// The session is read, never made: making it restores owner holds.
	w.mu.RLock()
	m := w.marshal
	w.mu.RUnlock()
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.architecture != nil {
		return m.architecture.URL
	}
	return ""
}

// readSmallFile reads at most limit bytes of a regular file.
func readSmallFile(path string, limit int64) ([]byte, error) {
	if info, err := os.Lstat(path); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%s is not a small regular file", path)
	}
	buf := make([]byte, info.Size())
	n, err := f.Read(buf)
	if err != nil && n == 0 && info.Size() > 0 {
		return nil, err
	}
	return buf[:n], nil
}

// selfApprovalLine says when the Marshal tried to accept gate g for the
// owner and that it was refused; "" when it did not.
func (w *Workspace) selfApprovalLine(g ownerGate, now time.Time) string {
	if g.warning == "" {
		return ""
	}
	var at time.Time
	for _, n := range w.noteList() {
		if n.Text == selfAcceptRefusedNote {
			at = n.At
			break
		}
	}
	s := w.v2CardState()
	s.mu.Lock()
	if s.refusedSeen == nil {
		s.refusedSeen = map[string]time.Time{}
	}
	if at.IsZero() {
		// The Activity line rolled off: the first frame that saw the
		// refusal stands in for it.
		at = s.refusedSeen[g.key]
		if at.IsZero() {
			at = now
		}
	}
	s.refusedSeen[g.key] = at
	s.mu.Unlock()
	return "The Marshal tried to accept this for you at " + at.In(now.Location()).Format("15:04") + ". Refused: only you can accept."
}

// v2DecisionNow is the decision waiting for the owner now: an open owner
// card first, in the order the classic Board takes their keys (the guard's
// alert, "what next?", the Marshal's question, the requirements, the
// change; boardv2_cards_owner.go), then the architecture (a plan is
// approved against an accepted architecture), then a drafted plan, then the
// inbox's first pending decision. A decision the owner put off with Esc is
// not shown until another one waits.
func (w *Workspace) v2DecisionNow(now time.Time) (v2Decision, bool) {
	p := w.marshalPanel()
	found := w.v2OwnerCards()
	for _, g := range w.ownerGates(p) {
		switch g.kind {
		case decisionArchitecture:
			found = append(found, w.architectureDecision(g, now))
		case decisionPlan:
			found = append(found, w.planDecision(g))
		}
	}
	if items := w.boardInbox(); len(items) > 0 {
		found = append(found, inboxDecision(items[0]))
	}
	s := w.v2CardState()
	s.mu.Lock()
	defer s.mu.Unlock()
	// A put-off decision that no longer waits is forgotten, so the same
	// key waiting again later shows again.
	open := map[string]bool{}
	for _, d := range found {
		open[d.key] = true
	}
	for k := range s.dismissed {
		if !open[k] {
			delete(s.dismissed, k)
		}
	}
	for _, d := range found {
		if !s.dismissed[d.key] {
			return d, true
		}
	}
	return v2Decision{}, false
}

// dismissV2Card puts the decision key off ("later") while it waits.
func (w *Workspace) dismissV2Card(key string) {
	s := w.v2CardState()
	s.mu.Lock()
	if s.dismissed == nil {
		s.dismissed = map[string]bool{}
	}
	s.dismissed[key] = true
	s.mu.Unlock()
	w.requestRepaint()
}

// architectureDecision is the "Accept architecture" card for gate g.
func (w *Workspace) architectureDecision(g ownerGate, now time.Time) v2Decision {
	d := v2Decision{kind: decisionArchitecture, key: g.key, gate: g}
	c := vm.Decision{Kind: decisionArchitecture, Title: "Accept architecture"}
	w.gates.mu.Lock()
	contract := w.gates.contract
	w.gates.mu.Unlock()
	if contract != nil && contract.Revision > 0 {
		c.Title += fmt.Sprintf(" · rev %d", contract.Revision)
	}
	if contract != nil {
		if sk := w.cachedSketch(g.key); sk != nil {
			parts := []string{count(sk.Parts, "part", "parts"), count(sk.Links, "link", "links")}
			if cov := coversText(sk.Covers); cov != "" {
				parts = append(parts, "covers "+cov)
			}
			c.Summary = strings.Join(parts, " · ")
			if sk.Changed > 0 {
				c.ChangedNote = count(sk.Changed, "part", "parts") + " changed by this goal (dashed)"
			}
			c.Nodes = append([]vm.Node(nil), sk.Nodes...)
		}
	}
	if c.Summary == "" {
		c.Summary = g.summary
	}
	d.link = w.architectureLink()
	c.NoDisplay = headlessDisplay() || w.browserOpenFailed()
	if c.NoDisplay {
		c.Link = d.link
	}
	c.SelfApproval = w.selfApprovalLine(g, now)
	c.Actions = []vm.Key{{Key: "a", Label: "open in browser"}}
	if d.link != "" {
		c.Actions = append(c.Actions, vm.Key{Key: "c", Label: "copy link"})
	}
	if boardAcceptsArchitecture {
		c.Actions = append(c.Actions, vm.Key{Key: "⏎", Label: "accept here"})
	}
	c.Actions = append(c.Actions, vm.Key{Key: "k", Label: "comment"})
	d.card = c
	return d
}

// planDecision is the "Approve plan" card for the plan gate g: Enter arms
// the owner's reply and a second Enter sends it, as the inbox does.
func (w *Workspace) planDecision(g ownerGate) v2Decision {
	c := vm.Decision{Kind: decisionPlan, Title: "Approve plan", Summary: g.summary, ChangedNote: g.limits}
	enter := vm.Key{Key: "⏎", Label: g.reply}
	switch {
	case g.sent:
		enter = vm.Key{}
		c.ChangedNote = "Sent “" + g.reply + "” to the Marshal; waiting for it to act."
	case w.v2PlanArmed() == g.key:
		enter.Label = "send “" + g.reply + "” to the Marshal"
	}
	if enter.Key != "" {
		c.Actions = append(c.Actions, enter)
	}
	c.Actions = append(c.Actions, vm.Key{Key: "7", Label: "open the plan"}, vm.Key{Key: "k", Label: "comment"})
	return v2Decision{kind: decisionPlan, key: g.key, gate: g, card: c}
}

// v2PlanArmed is the plan decision whose Enter waits for a second Enter.
func (w *Workspace) v2PlanArmed() string {
	s := w.v2CardState()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.planArmed
}

// inboxDecision is the inbox's first pending decision as a card; its keys
// are the inbox's own (handleBoardInboxKey).
func inboxDecision(it boardmodel.InboxItem) v2Decision {
	r := it.Request
	what := strings.TrimSpace(r.Kind + " " + r.Object)
	c := vm.Decision{Kind: decisionInbox, Title: "Decide · " + firstLine(what)}
	var summary []string
	if r.Reason != "" {
		summary = append(summary, firstLine(r.Reason))
	}
	if r.Scope != "" {
		summary = append(summary, "scope "+firstLine(r.Scope))
	}
	if r.TaskID != "" {
		summary = append(summary, "task "+cleanBoard(r.TaskID))
	}
	c.Summary = strings.Join(summary, " · ")
	if r.RunID != "" {
		c.ChangedNote = "The run waits for this decision."
	}
	c.Actions = []vm.Key{{Key: "a", Label: "allow"}, {Key: "d", Label: "deny"}, {Key: "⏎", Label: "details"}}
	return v2Decision{kind: decisionInbox, key: "inbox:" + it.Canonical, card: c}
}
