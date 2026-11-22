package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// Owner gates: what the Marshal waits for the owner to decide. The Board
// reads each wait from canonical state and shows it in the inbox, so the
// operator never has to watch the chat to find out the run needs them.
//
// A gate is read from the store, never from the chat's words: a submitted
// project contract whose architecture waits for the owner's Accept in the
// browser (store.ReadPendingProjectContract, Extreme), and a run whose
// drafted plan waits for approval (run state drafting with planned tasks).
// The architecture gate's only button opens the browser; it never types
// into the chat. The plan gate's reply button sends the owner's exact words
// to the docked chat through the Board's say path (board_say.go); sending is
// never one key: y or a click arms the reply, Enter or a second click sends
// it, any other key cancels. Nothing here accepts or approves on its own.

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
	// details is the key that opens what the gate is about: y (open in
	// browser) for the architecture, 7 for the plan.
	details rune
	// sent is set once the reply went to the chat: the gate then waits
	// for the Marshal and offers no button until it changes.
	sent bool
	// limits is the plan's one-line limit check, "" when not read yet;
	// limitsLow is set when a provider it uses is under 20% in a window.
	limits    string
	limitsLow bool
	// warning is one plain line under the buttons, "" for none: the
	// Marshal tried to decide this gate itself and Marshal refused it.
	warning string
}

// deferredArchitecture is an "open architecture" request from the Marshal that
// arrived before the requirements were confirmed (or before a goal contract
// existed). The Board keeps it instead of dropping it and offers it in the
// inbox once the requirements are confirmed.
type deferredArchitecture struct {
	occurrences []string
	reason      string
	offered     bool
}

// gateState is the Board's owner gates: the contract last read from the
// store, the gate whose reply waits for Enter and the replies sent.
type gateState struct {
	mu           sync.Mutex
	contract     *gateContract
	deferredArch *deferredArchitecture
	armed        string
	sent         map[string]time.Time
	// refusedArch is the contract (id and revision) the Marshal tried to
	// accept for the owner and policy refused, "" for none.
	refusedArch string
	// selfApproved is the plans the Marshal approved itself and the Board
	// already said so about, so it says so once.
	selfApproved map[string]bool
}

// selfAcceptRefusedNote is what the owner reads when the Marshal tried to
// record an owner-only architecture acceptance and policy refused it.
const selfAcceptRefusedNote = "The Marshal tried to accept the architecture for you; Marshal refused it. Only you can accept it in the browser."

// selfApprovedPlanNote is what the owner reads, once per plan, when the
// Marshal approved a plan without the owner (ultra mode).
const selfApprovedPlanNote = "The Marshal approved the plan itself (ultra mode). Press 7 to see it."

// noteRefusedArchitecture records that the Marshal's own acceptance of
// contract id at revision was refused: one Activity line, and the
// architecture gate in the inbox says so while that revision waits.
func (w *Workspace) noteRefusedArchitecture(id string, revision int64) {
	w.gates.mu.Lock()
	if c := w.gates.contract; revision <= 0 && c != nil && c.ID == id {
		// The refused decision names the contract, not its revision: it
		// is the revision now waiting for the owner.
		revision = c.Revision
	}
	w.gates.refusedArch = refusedArchKey(id, revision)
	w.gates.mu.Unlock()
	w.RecordActivity(selfAcceptRefusedNote)
	w.refreshGatesSoon()
}

// refusedArchKey names one contract revision; a zero revision names every
// revision of the contract (the refusal did not say which).
func refusedArchKey(id string, revision int64) string {
	if revision <= 0 {
		return id
	}
	return fmt.Sprintf("%s:%d", id, revision)
}

// noteSelfApprovedPlan says once per plan that the Marshal approved it
// without the owner.
func (w *Workspace) noteSelfApprovedPlan(plan string) {
	w.gates.mu.Lock()
	if w.gates.selfApproved == nil {
		w.gates.selfApproved = map[string]bool{}
	}
	seen := w.gates.selfApproved[plan]
	w.gates.selfApproved[plan] = true
	w.gates.mu.Unlock()
	if !seen {
		w.RecordActivity(selfApprovedPlanNote)
	}
}

// holdArchitectureRequest keeps a bound Marshal "open architecture" request
// that arrived before the requirements were confirmed. If the requirements are
// already confirmed, it stays offered immediately; otherwise it waits silently
// until offerDeferredArchitecture runs.
func (w *Workspace) holdArchitectureRequest(occurrences []string, reason string) {
	w.req.mu.Lock()
	confirmed := w.req.confirmed != nil && w.req.card == nil
	w.req.mu.Unlock()

	w.gates.mu.Lock()
	newlyHeld := w.gates.deferredArch == nil
	if w.gates.deferredArch == nil {
		w.gates.deferredArch = &deferredArchitecture{
			reason:  strings.TrimSpace(reason),
			offered: confirmed,
		}
	} else {
		if strings.TrimSpace(reason) != "" && w.gates.deferredArch.reason == "" {
			w.gates.deferredArch.reason = strings.TrimSpace(reason)
		}
		if confirmed {
			w.gates.deferredArch.offered = true
		}
	}
	for _, id := range occurrences {
		if id == "" {
			continue
		}
		dup := false
		for _, existing := range w.gates.deferredArch.occurrences {
			if existing == id {
				dup = true
				break
			}
		}
		if !dup {
			w.gates.deferredArch.occurrences = append(w.gates.deferredArch.occurrences, id)
		}
	}
	offered := w.gates.deferredArch.offered
	w.gates.mu.Unlock()

	if newlyHeld {
		if offered {
			w.RecordActivity("Architecture is ready to open: press y on the Board to open it in your browser.")
		} else {
			w.RecordActivity("Marshal wants to open the architecture; the Board will offer it once you confirm the requirements.")
		}
	}
	w.requestRepaint()
}

// offerDeferredArchitecture makes any held "open architecture" request visible
// in the Board inbox once the owner confirms the requirements (or a Goal is
// confirmed).
func (w *Workspace) offerDeferredArchitecture() {
	w.gates.mu.Lock()
	if w.gates.deferredArch == nil || w.gates.deferredArch.offered {
		w.gates.mu.Unlock()
		return
	}
	w.gates.deferredArch.offered = true
	w.gates.mu.Unlock()
	w.RecordActivity("Requirements confirmed; press y on the Board to open the architecture in your browser.")
	w.requestRepaint()
}

func (w *Workspace) hasDeferredArchitecture() bool {
	w.gates.mu.Lock()
	defer w.gates.mu.Unlock()
	return w.gates.deferredArch != nil
}

// clearDeferredArchitecture clears any held/offered architecture request and
// returns its proposal occurrence IDs so the caller can resolve them.
func (w *Workspace) clearDeferredArchitecture() []string {
	w.gates.mu.Lock()
	d := w.gates.deferredArch
	w.gates.deferredArch = nil
	w.gates.mu.Unlock()
	if d == nil {
		return nil
	}
	w.requestRepaint()
	return d.occurrences
}

// gateResend is how long a sent reply holds its gate's button back: when
// the gate is still there after it, the reply may not have reached the
// Marshal, and it can be sent again.
const gateResend = 5 * time.Minute

// gateReplyPlan is the exact words the plan gate's reply button says to the
// Marshal; its owner-instruction binding records the approval.
const gateReplyPlan = "approve the plan"

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
	var deferred *deferredArchitecture
	if w.gates.deferredArch != nil && w.gates.deferredArch.offered {
		cp := *w.gates.deferredArch
		deferred = &cp
	}
	sent := w.gates.sent
	refused := w.gates.refusedArch
	w.gates.mu.Unlock()
	var out []ownerGate
	if c := contract; c != nil {
		g := ownerGate{
			key:     fmt.Sprintf("architecture:%s:%d:%s", c.ID, c.Revision, c.Digest),
			kind:    "architecture",
			title:   "waits for you in the browser",
			summary: firstLine(c.Purpose),
			reply:   "",
			details: 'y',
		}
		if refused != "" && (refused == c.ID || refused == refusedArchKey(c.ID, c.Revision)) {
			g.warning = selfAcceptRefusedNote
		}
		out = append(out, g)
	} else if deferred != nil {
		g := ownerGate{
			key:     "architecture:deferred",
			kind:    "architecture",
			title:   "ready to open in the browser",
			summary: firstLine(deferred.reason),
			reply:   "",
			details: 'y',
		}
		out = append(out, g)
	}
	if ownerPlanGate && p != nil && planAwaitsApproval(p) {
		b := p.Board
		title := "drafted plan waits for your approval"
		summary := fmt.Sprintf("%d task", b.Run.Total)
		if b.Run.Total != 1 {
			summary += "s"
		}
		if b.Run.Title != "" {
			summary = firstLine(b.Run.Title) + " · " + summary
		}
		g := ownerGate{key: "plan:" + b.Run.ID + ":" + b.PlanID, kind: "plan", title: title, summary: summary, reply: gateReplyPlan, details: '7'}
		g.limits, g.limitsLow, _ = w.planLimits(p)
		out = append(out, g)
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
			if g.kind == "architecture" {
				w.openArchitectureReview()
			} else if w.gateArmed() == key {
				w.sendGate(w.workers.context(), g)
			} else {
				w.armGate(g)
			}
		case 'n':
			w.disarmGate()
		default:
			w.disarmGate()
			if !w.switchBodyView(KeyEvent{Type: KeyRune, Rune: g.details}) {
				w.setOutput("That view has nothing to read in this session.", false)
			}
		}
		return
	}
}
