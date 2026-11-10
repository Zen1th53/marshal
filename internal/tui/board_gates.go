package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// Owner gates: what the Marshal waits for the owner to say in its chat.
// The Marshal asks for them in prose ("Reply accept architecture",
// "Approve this one-task plan?"); the Board reads the same wait from
// canonical state and shows it in the inbox, so the operator never has to
// watch the chat to find out the run needs them.
//
// A gate is read from the store, never from the chat's words: a submitted
// project contract that waits for the owner's acceptance
// (store.ReadPendingProjectContract, Extreme), and a run whose drafted plan
// waits for approval (run state drafting with planned tasks). Its reply
// button sends the owner's exact words to the docked chat through the
// Board's say path (board_say.go), as if the owner typed them there, so
// the existing owner-instruction binding records it. Sending is never one
// key: y or a click arms the reply, Enter or a second click sends it, any
// other key cancels. Nothing here accepts or approves on its own.

// gateContract is a submitted project contract waiting for the owner.
type gateContract struct {
	ID        string
	Revision  int64
	Digest    string
	Purpose   string
	Submitted time.Time
}

// ownerGate is one decision the Marshal waits for the owner to make.
type ownerGate struct {
	// key identifies the gate across paints: a new contract revision or
	// plan is a new gate.
	key string
	// kind is "architecture" or "plan".
	kind string
	// title is the entry's first line after its kind; summary follows it.
	title, summary string
	// reply is the owner's exact words the reply button says to the
	// Marshal.
	reply string
	// details is the key that opens what the gate is about: b for the
	// architecture, 7 for the plan.
	details rune
	// sent is set once the reply went to the chat: the gate then waits
	// for the Marshal and offers no button until it changes.
	sent bool
}

// gateState is the Board's owner gates: the contract last read from the
// store, the gate whose reply waits for Enter and the replies sent.
type gateState struct {
	mu       sync.Mutex
	contract *gateContract
	armed    string
	sent     map[string]time.Time
}

// gateResend is how long a sent reply holds its gate's button back: when
// the gate is still there after it, the reply may not have reached the
// Marshal, and it can be sent again.
const gateResend = 5 * time.Minute

// Replies the gates send: the exact words the Marshal asks for and the
// owner-instruction binding accepts (store.ArchitectureAcceptanceInstruction).
const (
	gateReplyArchitecture = "accept architecture"
	gateReplyPlan         = "approve the plan"
)

// gateSource is a test's seam for the pending contract; nil reads the
// store (board_gates_extreme.go).
type gateSource func(ctx context.Context) (*gateContract, error)

// refreshGates reads the pending project contract once, off the paint
// path, and repaints when it changed.
func (w *Workspace) refreshGates(ctx context.Context) {
	read := w.gateContractSource
	if read == nil {
		read = w.readPendingContract
	}
	c, err := read(ctx)
	if err != nil || ctx.Err() != nil {
		return
	}
	w.gates.mu.Lock()
	was := w.gates.contract
	w.gates.contract = c
	w.gates.mu.Unlock()
	if (was == nil) != (c == nil) || was != nil && c != nil && *was != *c {
		w.requestRepaint()
	}
}

// refreshGatesSoon reads the gates again in the background: a contract
// was just submitted or a reply just sent.
func (w *Workspace) refreshGatesSoon() {
	w.startBackground(w.workers.context(), func(ctx context.Context) { w.refreshGates(ctx) })
}

// ownerGates is the gates waiting now for the run on the Board (p, nil
// when no run is open), architecture first: a plan is approved against
// an accepted architecture.
func (w *Workspace) ownerGates(p *MarshalPanel) []ownerGate {
	w.gates.mu.Lock()
	contract := w.gates.contract
	sent := w.gates.sent
	w.gates.mu.Unlock()
	var out []ownerGate
	if c := contract; c != nil {
		g := ownerGate{
			key:     fmt.Sprintf("architecture:%s:%d:%s", c.ID, c.Revision, c.Digest),
			kind:    "architecture",
			title:   fmt.Sprintf("contract %s rev %d waits for your acceptance", c.ID, c.Revision),
			summary: firstLine(c.Purpose),
			reply:   gateReplyArchitecture,
			details: 'b',
		}
		out = append(out, g)
	}
	if p != nil && planAwaitsApproval(p) {
		b := p.Board
		title := "drafted plan waits for your approval"
		summary := fmt.Sprintf("%d task", b.Run.Total)
		if b.Run.Total != 1 {
			summary += "s"
		}
		if b.Run.Title != "" {
			summary = firstLine(b.Run.Title) + " · " + summary
		}
		out = append(out, ownerGate{key: "plan:" + b.Run.ID + ":" + b.PlanID, kind: "plan", title: title, summary: summary, reply: gateReplyPlan, details: '7'})
	}
	for i := range out {
		if at, ok := sent[out[i].key]; ok && time.Since(at) < gateResend {
			out[i].sent = true
		}
	}
	return out
}

// planAwaitsApproval reports whether the run's drafted plan waits for the
// owner: drafting with planned tasks, not already being approved and not
// a major amendment, which /marshal amend approve decides.
func planAwaitsApproval(p *MarshalPanel) bool {
	b := p.Board
	if b.Run.State != marshal.Drafting || b.Run.Total == 0 {
		return false
	}
	note := strings.ToLower(p.Note)
	return !strings.HasPrefix(note, "approving") && !strings.Contains(note, "amendment") && !strings.HasPrefix(note, "planning failed") && !strings.HasPrefix(note, "drafting")
}

// waiting is the gates the owner has not answered yet.
func waitingGates(gates []ownerGate) int {
	n := 0
	for _, g := range gates {
		if !g.sent {
			n++
		}
	}
	return n
}

// gateArmed is the gate whose reply waits for Enter, "" for none.
func (w *Workspace) gateArmed() string {
	w.gates.mu.Lock()
	defer w.gates.mu.Unlock()
	return w.gates.armed
}

// armGate arms a gate's reply: the inbox shows what Enter will send.
func (w *Workspace) armGate(g ownerGate) {
	if g.sent {
		w.memoryAnswer("Already sent “"+g.reply+"” to the Marshal; the Board shows the "+g.kind+" once it acts.", false)
		return
	}
	w.gates.mu.Lock()
	w.gates.armed = g.key
	w.gates.mu.Unlock()
}

// disarmGate cancels an armed reply.
func (w *Workspace) disarmGate() {
	w.gates.mu.Lock()
	w.gates.armed = ""
	w.gates.mu.Unlock()
}

// sendGate sends the armed gate's reply to the Marshal chat. It is the
// only place a gate acts, and only after the arm and the confirm.
func (w *Workspace) sendGate(ctx context.Context, g ownerGate) {
	w.disarmGate()
	if w.sayPane() == "" && !w.nativeChatShown() {
		w.setOutput("No Marshal chat is open, so “"+g.reply+"” was not sent; /marshal chat starts one.", true)
		return
	}
	w.gates.mu.Lock()
	if w.gates.sent == nil {
		w.gates.sent = map[string]time.Time{}
	}
	w.gates.sent[g.key] = time.Now()
	w.gates.mu.Unlock()
	w.sayToMarshal(ctx, g.reply)
	w.memoryAnswer("Sent “"+g.reply+"” to the Marshal as your reply; Activity shows when it reaches the chat.", false)
	w.refreshGatesSoon()
}

// handleGateKey is the owner gates' keys on the Board: with a reply armed,
// Enter sends it and any other key cancels; otherwise y arms the first
// gate still waiting. It takes nothing while no gate waits.
func (w *Workspace) handleGateKey(event KeyEvent, p *MarshalPanel) bool {
	gates := w.ownerGates(p)
	if armed := w.gateArmed(); armed != "" {
		for _, g := range gates {
			if g.key == armed && event.Type == KeyEnter {
				w.sendGate(w.workers.context(), g)
				return true
			}
		}
		w.disarmGate()
		w.memoryAnswer("Reply not sent.", false)
		return true
	}
	if event.Type != KeyRune || event.Rune != 'y' || w.anyBodyView() || w.boardUI.inspect != "" {
		return false
	}
	for _, g := range gates {
		if !g.sent {
			w.armGate(g)
			return true
		}
	}
	if len(gates) > 0 {
		// Every reply is sent: y says so instead of typing a "y".
		w.armGate(gates[0])
		return true
	}
	return false
}

// clickGate is a click on a gate's button: the reply button arms the
// reply and a second click on it sends, as Enter does; the details button
// opens what the gate is about.
func (w *Workspace) clickGate(key string, action rune) {
	w.mu.RLock()
	p := w.state.Marshal
	w.mu.RUnlock()
	for _, g := range w.ownerGates(p) {
		if g.key != key {
			continue
		}
		switch action {
		case 'y':
			if w.gateArmed() == key {
				w.sendGate(w.workers.context(), g)
			} else {
				w.armGate(g)
			}
		case 'n':
			w.disarmGate()
		default:
			w.disarmGate()
			w.boardUI.inspect = ""
			if !w.switchBodyView(KeyEvent{Type: KeyRune, Rune: g.details}) {
				w.setOutput("That view has nothing to read in this session.", false)
			}
		}
		return
	}
}

// gateEntry is one owner gate as the inbox shows it, two rows: what waits
// and its summary, then its buttons, or while armed the confirmation. x is
// the inbox's first cell, row its frame row.
func (v boardVisual) gateEntry(g ownerGate, x, row, w int) []string {
	flag := "⚑ "
	if !v.unicode() {
		flag = "! "
	}
	color := "waiting"
	if g.sent {
		color = "dim"
	}
	first := v.fg(color, flag+g.kind) + v.fg("dim", " · ") + v.bold(v.fg("white", g.title))
	if g.summary != "" {
		first += v.fg("dim", " · "+v.trunc(cleanBoard(g.summary), max(8, w-VisibleLen(first)-3)))
	}
	second, at := "    ", 4
	button := func(label string, action rune) {
		v.hit(boardHit{row: row + 1, x0: x + at, x1: x + min(w, at+VisibleLen(label)), kind: hitGate, id: g.key, action: action})
		second += label
		at += VisibleLen(label)
	}
	gap := func(s string) {
		second += s
		at += VisibleLen(s)
	}
	details := "open the plan"
	if g.kind == "architecture" {
		details = "open details"
	}
	switch {
	case g.sent:
		gap(v.fg("dim", "sent “"+g.reply+"” to the Marshal · waiting for it to act  "))
		button(v.bg("rule", "["+string(g.details)+"]")+" "+details, g.details)
	case v.gateArmed == g.key:
		gap(v.fg("waiting", "send “"+g.reply+"” to the Marshal as your reply? "))
		button(v.bg("rule", "[Enter]")+" send", 'y')
		gap("  ")
		button(v.bg("rule", "[Esc]")+" cancel", 'n')
	default:
		button(v.bg("rule", "[y]")+" "+g.reply, 'y')
		gap("  ")
		button(v.bg("rule", "["+string(g.details)+"]")+" "+details, g.details)
	}
	return []string{PadCell(first, w), PadCell(second, w)}
}
