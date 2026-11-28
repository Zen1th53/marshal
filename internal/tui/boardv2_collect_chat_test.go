package tui

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/board/vm"
	"github.com/Zen1th53/marshal/internal/permission"
)

func TestCollectV2ChatRequestsAndState(t *testing.T) {
	w := &Workspace{tmuxActiveWins: map[string]*activeTmuxAgent{"marshal-chat": {id: "marshal-chat", role: "marshal-chat", provider: "claude", paneID: "%77", state: "working"}}}
	sent := time.Now().Add(-time.Minute)
	w.chatRequestSent("d1", permission.Request{Kind: "project-contract", Object: "C1"}, sent)
	w.chatRequestSent("d2", permission.Request{Kind: "marshal-command", Object: "/marshal approve"}, sent.Add(time.Second))
	w.chatRequestSent("d3", permission.Request{Kind: "memory", Object: "MEM-IMPORT-1"}, sent.Add(2*time.Second))
	w.chatRequestSent("d1", permission.Request{Kind: "project-contract", Object: "C1"}, sent.Add(time.Hour)) // re-delivery keeps the first
	w.chatRequestAnswered("d1", true, sent.Add(3*time.Second))
	w.chatRequestAnswered("d3", false, sent.Add(6*time.Second))
	w.chatRequestExpired("d2")
	w.chatRestarted()
	w.chatRebound(time.Now(), 2)
	noteChatInput("%77", true)

	var b vm.Board
	w.collectV2Chat(&b)
	if len(b.Requests) != 3 {
		t.Fatalf("requests: %+v", b.Requests)
	}
	newest, oldest := b.Requests[0], b.Requests[2]
	if newest.What != "memory import" || newest.Answer != "decline" || newest.Latency != 4*time.Second || newest.Tone != vm.ToneWarn {
		t.Fatalf("newest: %+v", newest)
	}
	if oldest.What != "apply architecture" || oldest.Answer != "allow" || oldest.Latency != 3*time.Second || !oldest.Sent.Equal(sent) || oldest.Tone != vm.ToneOK {
		t.Fatalf("oldest: %+v", oldest)
	}
	if b.Requests[1].What != "approve plan" || b.Requests[1].Answer != "expired" || b.Requests[1].Latency != 0 {
		t.Fatalf("expired: %+v", b.Requests[1])
	}
	c := b.Chat
	if c.Restarts != 1 || c.Resent != 2 || c.ReboundAt.IsZero() || c.Answered != 2 || !c.InputClear || !b.Marshal.InputClear {
		t.Fatalf("chat: %+v", c)
	}
	if c.Bound != w.chatAuthorityBound() || b.Marshal.ChatBound != c.Bound {
		t.Fatalf("binding: %+v", c)
	}

	// A blocked delivery shows as the stage; a restart not yet rebound too.
	w.noteChatOnce(chatQueuedOwnerInput)
	w.noteChatOnce(chatQueuedOwnerInput)
	b = vm.Board{}
	w.collectV2Chat(&b)
	if c.Bound && b.Chat.Stage != "waiting on your input" {
		t.Fatalf("stage: %q", b.Chat.Stage)
	}
	n := 0
	for _, note := range w.noteList() {
		if note.Text == chatQueuedOwnerInput {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("repeated delivery note logged %d times", n)
	}
	w.chatDeliveryUnblocked()
	w.tmuxActiveWins["marshal-chat"].state = "starting"
	w.chatRestarted()
	b = vm.Board{}
	w.collectV2Chat(&b)
	if b.Chat.Stage != "restarting" || b.Chat.Bound || b.Marshal.ChatBound {
		t.Fatalf("restarting chat: %+v", b.Chat)
	}

	// No chat: nothing known.
	b = vm.Board{}
	(&Workspace{}).collectV2Chat(&b)
	if b.Chat.Bound || b.Chat.Stage != "" || len(b.Requests) != 0 || b.Marshal.InputClear {
		t.Fatalf("empty: %+v", b.Chat)
	}
}

// Equal delivery notes are logged once per blockage, however often the
// delivery is retried, and once more after it cleared.
func TestNoteChatOnceLogsOncePerBlockage(t *testing.T) {
	w := &Workspace{}
	for range 5 {
		w.noteChatOnce(chatQueuedOwnerInput)
		w.RecordActivity("something else")
	}
	count := func() (n int) {
		for _, note := range w.noteList() {
			if note.Text == chatQueuedOwnerInput {
				n++
			}
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("logged %d times", count())
	}
	w.noteChatOnce(chatQueuedOwnerInput)
	if w.noteList()[0].Text != chatQueuedOwnerInput || count() != 1 {
		t.Fatal("repeat did not update the standing note")
	}
	w.chatDeliveryUnblocked()
	w.RecordActivity("Marshal decision requested")
	w.noteChatOnce(chatQueuedOwnerInput)
	if count() != 2 {
		t.Fatalf("new blockage not logged: %d", count())
	}
}
