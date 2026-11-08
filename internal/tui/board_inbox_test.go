package tui

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/inbox"
)

func TestInboxDecisionMatchesPopupEvent(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-inbox-equivalence", false)
	req := permission.Request{Kind: "read", Object: t.TempDir(), Scope: "this project, until revoked, read-only", Who: "Marshal"}
	if err := w.decidePermission(t.Context(), req, false, "operator popup"); err != nil {
		t.Fatal(err)
	}
	item := model.NewInboxItem(req)
	w.permissions.inbox = []permission.Request{req}
	if err := w.decideInbox(t.Context(), inbox.Decision{RequestKey: req.Key(), Canonical: item.Canonical, Blast: inbox.Blast(item), Action: 'd'}); err != nil {
		t.Fatal(err)
	}
	events, err := runtime.Store().ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var decisions []map[string]any
	for _, event := range events {
		if event.Type == "PERMISSION_DECIDED" {
			decisions = append(decisions, event.Data)
		}
	}
	if len(decisions) != 2 {
		t.Fatalf("got %d permission events", len(decisions))
	}
	if !strings.Contains(decisions[1]["source"].(string), item.Canonical) || !strings.Contains(decisions[1]["source"].(string), inbox.Blast(item)) {
		t.Fatal("confirmation artifact omitted id or shown blast radius")
	}
	delete(decisions[0], "source")
	delete(decisions[1], "source")
	if !reflect.DeepEqual(decisions[0], decisions[1]) {
		t.Fatalf("decision events differ: %v / %v", decisions[0], decisions[1])
	}
}

func TestBoardQueueStaysPendingWithoutPopup(t *testing.T) {
	if Edition() == "Marshal" {
		t.Skip("Marshal build routes reads automatically")
	}
	w, runtime := realControlWorkspace(t, "SESSION-inbox-pending", false)
	w.screen = NewScreen(nil)
	w.mu.Lock()
	w.state.Marshal = &MarshalPanel{RunID: "run"}
	w.mu.Unlock()
	req := permission.Request{Kind: "read", Object: t.TempDir(), Scope: "one run", Who: "Marshal"}
	w.permissions.queue.Add(req)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	w.runPermissionQueue(ctx)
	if len(w.boardInbox()) != 1 {
		t.Fatal("Board request was not retained")
	}
	events, err := runtime.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "PERMISSION_DECIDED" {
			t.Fatal("undecided Board request was resolved")
		}
	}
}

func TestBoardInboxOrdersGateBlastAndAge(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	w.permissions.inbox = []permission.Request{
		{Kind: "read", Object: "/run-old", Scope: "one run"},
		{Kind: "read", Object: "/project-old", Scope: "this project"},
		{Kind: "read", Object: "/project-new", Scope: "this project"},
		{Kind: "marshal-command", Object: "/gate", Scope: "one run"},
	}
	items := w.boardInbox()
	want := []string{"/gate", "/project-old", "/project-new", "/run-old"}
	for i, item := range items {
		if item.Request.Object != want[i] {
			t.Fatalf("order %d: %s, want %s", i, item.Request.Object, want[i])
		}
	}
}

// TestBoardInboxXCancelsPlainConfirmation: x cancels a confirmation that
// reads no text, as the help says, and a stray key typed into one neither
// decides nor stays behind.
func TestBoardInboxXCancelsPlainConfirmation(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.frameLines(120, 40)
	w.inboxView.Update(w.boardInbox())
	press := func(r rune) bool { return w.handleBoardInboxKey(t.Context(), KeyEvent{Type: KeyRune, Rune: r}) }
	if !press('a') || !w.inboxView.Confirming() {
		t.Fatal("a did not open the confirmation")
	}
	if !press('t') || w.inboxView.Prompt() != "Enter to confirm a" {
		t.Fatalf("stray key changed the confirmation: %q", w.inboxView.Prompt())
	}
	if !press('x') || w.inboxView.Confirming() {
		t.Fatal("x did not cancel the confirmation")
	}
	if n := len(w.boardInbox()); n != 1 {
		t.Fatalf("x decided the request: %d left", n)
	}
}
