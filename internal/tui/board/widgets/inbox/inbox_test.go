package inbox

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

func TestTypedGateRequiresExactCanonicalID(t *testing.T) {
	item := model.NewInboxItem(permission.Request{Kind: "marshal-command", Object: "/marshal approve", Scope: "one run"})
	v := View{}
	v.Update([]model.InboxItem{item})
	if !v.Begin('a') {
		t.Fatal("allow did not open confirmation")
	}
	v.Input(item.Canonical + "x")
	if _, ok := v.Confirm(); ok {
		t.Fatal("wrong id authorized decision")
	}
	v.ClearInput()
	v.Input(item.Canonical)
	decision, ok := v.Confirm()
	if !ok || !decision.Allow || decision.Canonical != item.Canonical {
		t.Fatalf("exact id refused: %+v %v", decision, ok)
	}
}

func TestConfirmationRemainsVisibleInFullInbox(t *testing.T) {
	var items []model.InboxItem
	for i := range 8 {
		items = append(items, model.NewInboxItem(permission.Request{Kind: "marshal-command", Object: string(rune('a' + i)), Scope: "one run"}))
	}
	v := View{}
	v.Update(items)
	v.Move(7)
	v.Begin('d')
	lines := v.Render(80, 3, layout.Compact)
	if len(lines) != 3 || !strings.Contains(lines[1], items[7].Canonical) || !strings.Contains(lines[2], "Type "+items[7].Canonical) {
		t.Fatalf("selected decision or confirmation hidden: %v", lines)
	}
}

func TestUntrustedReasonCannotWriteTerminalControls(t *testing.T) {
	item := model.NewInboxItem(permission.Request{Kind: "read", Object: "/tmp/\x1b[2J", Scope: "one run", Reason: "\x1b[31mdeny"})
	if strings.ContainsRune(Blast(item), '\x1b') || strings.ContainsRune(Reason(item), '\x1b') {
		t.Fatal("terminal control reached Board output")
	}
}

func TestMoveNeverDecidesAndCancelLeavesPending(t *testing.T) {
	items := []model.InboxItem{
		model.NewInboxItem(permission.Request{Kind: "read", Object: "/first"}),
		model.NewInboxItem(permission.Request{Kind: "read", Object: "/second"}),
	}
	v := View{}
	v.Update(items)
	v.Move(1)
	if v.Selected() != items[1].Request.Key() {
		t.Fatal("movement did not select second request")
	}
	if _, ok := v.Confirm(); ok {
		t.Fatal("movement made a decision")
	}
	v.Begin('d')
	v.Cancel()
	if _, ok := v.Confirm(); ok {
		t.Fatal("cancel made a decision")
	}
}

// A plain confirmation reads no text: a typed key is dropped, so it can
// neither pile up unseen nor turn up in a later typed confirmation.
func TestPlainConfirmationReadsNoText(t *testing.T) {
	item := model.NewInboxItem(permission.Request{Kind: "read", Object: "/tmp/history", Scope: "this project"})
	if item.Gate == model.GateTyped {
		t.Skip("read is a typed gate here")
	}
	v := View{}
	v.Update([]model.InboxItem{item})
	v.Begin('a')
	if v.WantsText() {
		t.Fatal("allow once reads text")
	}
	v.Input("tl")
	if v.input != "" {
		t.Fatalf("plain confirmation kept %q", v.input)
	}
	v.Cancel()
	v.Begin('r')
	if !v.WantsText() {
		t.Fatal("return does not read its reason")
	}
	v.Input("no")
	if d, ok := v.Confirm(); !ok || d.Reason != "no" {
		t.Fatalf("return reason: %+v %v", d, ok)
	}
}
