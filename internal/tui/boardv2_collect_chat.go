package tui

import (
	"time"

	"github.com/Zen1th53/marshal/internal/board/vm"
)

// collectV2Chat fills the Marshal chat state (Chat, Requests, Marshal.ChatBound/InputClear/InputKnown) of the Board v2 view model. A field left at its zero
// value renders as "not known yet".
//
// It reads only what the Board already holds: the chat window's state, the
// live lease, the last input-box observation (the chat window's monitor
// reads it from the capture it already takes every tick, and delivery from
// its own), and the record of requests the Board sent. It never captures
// the pane: an input not looked at yet is unknown and shows as nothing.
func (w *Workspace) collectV2Chat(b *vm.Board) {
	w.tmuxMu.Lock()
	pane, state := "", ""
	if a := w.tmuxActiveWins["marshal-chat"]; a != nil {
		pane, state = a.paneID, a.state
	}
	w.tmuxMu.Unlock()
	_, native := w.nativeChatDecisionTarget()
	live := state == "working" || pane == "" && native
	bound := live && w.chatAuthorityBound()

	inputClear, inputKnown := false, false
	if pane != "" {
		inputClear, inputKnown = chatInputClear(pane)
	} else if native {
		// The native chat has no input box of its own to block delivery.
		inputClear, inputKnown = true, true
	}

	c := &w.chatBinding
	c.mu.Lock()
	chat := vm.Chat{Bound: bound, Restarts: c.restarts, ReboundAt: c.reboundAt, Resent: c.resent, InputClear: inputClear, InputKnown: inputKnown, Answered: c.answered}
	restarting := c.restarts > 0 && c.restartAt.After(c.reboundAt) && !bound
	blocked := c.blocked
	requests := make([]vm.Request, 0, len(c.requests))
	for i := len(c.requests) - 1; i >= 0; i-- { // newest first
		r := c.requests[i]
		req := vm.Request{Sent: r.sent, What: r.what, Answer: r.answer, Tone: requestTone(r.answer)}
		if !r.answeredAt.IsZero() {
			req.Latency = r.answeredAt.Sub(r.sent).Round(time.Second)
		}
		requests = append(requests, req)
	}
	c.mu.Unlock()

	switch {
	case restarting:
		chat.Stage = "restarting"
	case pane == "" && !native:
		chat.Stage = ""
	case !live:
		chat.Stage = "starting"
	case blocked == chatQueuedUnsent:
		chat.Stage = "sending"
	case blocked == chatQueuedOwnerInput:
		chat.Stage = "waiting on your input"
	case !bound:
		chat.Stage = "not bound"
	default:
		chat.Stage = "live"
	}
	if !inputKnown {
		chat.InputClear = false
	}
	b.Chat = chat
	b.Requests = requests
	b.Marshal.ChatBound = bound
	b.Marshal.InputClear = chat.InputClear
	b.Marshal.InputKnown = inputKnown
}

// requestTone colours a request's answer.
func requestTone(answer string) vm.Tone {
	switch answer {
	case "allow":
		return vm.ToneOK
	case "decline":
		return vm.ToneWarn
	case "expired":
		return vm.ToneBad
	case "pending":
		return vm.ToneInfo
	}
	return vm.ToneNone
}
