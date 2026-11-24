//go:build linux

package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// webChoice is f's typed choice with id and value, failing when absent.
func webChoice(t *testing.T, f webboard.Frame, id, value string) webboard.Choice {
	t.Helper()
	if f.Decision == nil {
		t.Fatalf("no decision card: %+v", f.Paused)
	}
	for _, c := range f.Decision.Choices {
		if c.ID == id && c.Value == value {
			return c
		}
	}
	t.Fatalf("no choice %s/%s in %+v", id, value, f.Decision.Choices)
	return webboard.Choice{}
}

// webDecideNow sends a typed choice of the card the page sees now.
func webDecideNow(t *testing.T, w *Workspace, f webboard.Frame, id, value string, edit func(*webboard.Action)) webboard.Result {
	t.Helper()
	a := webboard.Action{Kind: "decide", Card: webboard.CardID(f), Choice: id, Value: value, Revision: f.Decision.Revision}
	if f.Decision.Plan != nil {
		a.Digest = f.Decision.Plan.Digest
	}
	if edit != nil {
		edit(&a)
	}
	return w.webBoardAct(t.Context(), a)
}

func heldChangeBoard(t *testing.T) (*Workspace, *fakeChatTransport) {
	t.Helper()
	w, tr := changeChatWorkspace(t, []taskSpec{{"a", marshal.Queued}})
	t.Cleanup(func() { v2Cards.Delete(w) })
	hp, _ := parseMarshalProposal(holdProposalLine(`["a"]`, "switch to OAuth"))
	w.applyHold(hp)
	if !w.marshalSession().isHeldFor("run", "a") {
		t.Fatal("task a was not held")
	}
	return w, tr
}

// The change card's k is "keep the current goal" by its typed id, never the
// page's local comment button, and keeping releases the hold.
func TestChangeKeepActionUsesDecisionID(t *testing.T) {
	w, tr := heldChangeBoard(t)
	f := webCard(t, w)
	keep := webChoice(t, f, "change.keep", "")
	if keep.Key != "k" || keep.Local {
		t.Fatalf("keep choice %+v", keep)
	}
	if res := webDecideNow(t, w, f, "change.keep", "", func(a *webboard.Action) { a.Revision = "change:other" }); res.Status != "stale" || !w.marshalSession().isHeldFor("run", "a") {
		t.Fatalf("stale revision: %+v", res)
	}
	if res := webDecideNow(t, w, f, "change.keep", "", nil); res.Status != "done" {
		t.Fatalf("keep: %+v", res)
	}
	if w.currentChange() != nil || w.marshalSession().isHeldFor("run", "a") {
		t.Fatal("keep did not close the card and release the hold")
	}
	chatWaitFor(t, "the keep message", func() bool { return len(tr.sentTurns()) == 1 })
	if got := tr.sentTurns(); got[0] != "keep the current goal" {
		t.Fatalf("sent %q", got)
	}
}

// A requirements edit names its line and the card revision it was made on;
// the edit moves the revision, so the same view cannot edit twice.
func TestRequirementsEditVersioned(t *testing.T) {
	w, _, tr := boardWithNativeChat(t)
	t.Cleanup(func() { v2Cards.Delete(w) })
	p, err := parseMarshalProposal(reqProposalLine(exampleReqCard))
	if err != nil {
		t.Fatal(err)
	}
	w.openRequirements(p)
	f := webCard(t, w)
	if f.Decision.Kind != decisionRequirements {
		t.Fatalf("card %+v", f.Decision)
	}
	for name, edit := range map[string]func(*webboard.Action){
		"no line":      func(a *webboard.Action) { a.Text = "x" },
		"bad line":     func(a *webboard.Action) { a.Line, a.Text = "z", "x" },
		"no text":      func(a *webboard.Action) { a.Line = "c" },
		"control char": func(a *webboard.Action) { a.Line, a.Text = "c", "a\x07b" },
	} {
		if res := webDecideNow(t, w, f, "requirements.edit", "", edit); res.Status != "refused" {
			t.Errorf("%s: %+v", name, res)
		}
	}
	res := webDecideNow(t, w, f, "requirements.edit", "", func(a *webboard.Action) { a.Line, a.Text = "c", "from dev, lands on main" })
	if res.Status != "done" {
		t.Fatalf("edit: %+v", res)
	}
	chatWaitFor(t, "the edit", func() bool { return len(tr.sentTurns()) == 1 })
	if got := tr.sentTurns()[0]; got != "edit c: from dev, lands on main" {
		t.Fatalf("sent %q", got)
	}
	next := webCard(t, w)
	if next.Decision.Revision == f.Decision.Revision {
		t.Fatal("the edit did not move the card revision")
	}
	// The same view again, its revision with a fresh card id: refused.
	res = webDecideNow(t, w, next, "requirements.edit", "", func(a *webboard.Action) { a.Line, a.Text, a.Revision = "d", "again", f.Decision.Revision })
	if res.Status != "stale" || len(tr.sentTurns()) != 1 {
		t.Fatalf("stale edit: %+v sent %q", res, tr.sentTurns())
	}
	if res := webDecideNow(t, w, next, "requirements.edit", "", func(a *webboard.Action) { a.Line, a.Text = "d", "go test ./..." }); res.Status != "done" {
		t.Fatalf("second edit: %+v", res)
	}
}

// A plan is approved only after the owner opened it from the page and only
// for the digest opened, which must still be the current plan's.
func TestPlanApproveRequiresViewedCurrentDigest(t *testing.T) {
	if !ownerPlanGate {
		t.Skip("the shipped build has no owner plan gate")
	}
	w, _, tr := boardWithNativeChat(t)
	t.Cleanup(func() { v2Cards.Delete(w) })
	w.planSource = fakePlanSource{}
	w.setMarshalPanel(draftingPanel("awaiting plan approval; /marshal approve"))
	f := webCard(t, w)
	if f.Decision.Kind != decisionPlan || f.Decision.Plan == nil || len(f.Decision.Plan.Tasks) != 1 || f.Decision.Plan.Tasks[0].ID != "hello-cli" {
		t.Fatalf("plan card %+v", f.Decision)
	}
	if res := webDecideNow(t, w, f, "plan.approve", "", nil); res.Status != "refused" || !strings.Contains(res.Text, "Read the plan") {
		t.Fatalf("unread approve: %+v", res)
	}
	if res := webDecideNow(t, w, webCard(t, w), "plan.read", "", nil); res.Status != "done" {
		t.Fatalf("read: %+v", res)
	}
	read := webCard(t, w)
	// The plan changes after it was read: the digest read no longer binds.
	p := draftingPanel("awaiting plan approval; /marshal approve")
	p.run.Tasks[0].Checks = []marshal.Check{{Command: "go test ./..."}}
	w.setMarshalPanel(p)
	now := webCard(t, w)
	if now.Decision.Plan.Digest == read.Decision.Plan.Digest {
		t.Fatal("a changed plan kept its digest")
	}
	if res := webDecideNow(t, w, now, "plan.approve", "", func(a *webboard.Action) { a.Digest = read.Decision.Plan.Digest }); res.Status != "stale" {
		t.Fatalf("old digest: %+v", res)
	}
	if res := webDecideNow(t, w, now, "plan.approve", "", nil); res.Status != "refused" {
		t.Fatalf("current digest never read: %+v", res)
	}
	if got := now.Decision.Plan.Tasks[0].Checks; !slices.Equal(got, []string{"go test ./..."}) {
		t.Fatalf("exact checks %q", got)
	}
	webDecideNow(t, w, now, "plan.read", "", nil)
	res := webDecideNow(t, w, webCard(t, w), "plan.approve", "", nil)
	if res.Status != "done" || res.Confirm == nil || len(tr.sentTurns()) != 0 {
		t.Fatalf("approve arms: %+v sent %q", res, tr.sentTurns())
	}
	if res := webDecideNow(t, w, webCard(t, w), "plan.approve", "", func(a *webboard.Action) { a.Confirmed = true }); res.Status != "done" {
		t.Fatalf("confirm: %+v", res)
	}
	chatWaitFor(t, "the reply", func() bool { return len(tr.sentTurns()) == 1 })
	if got := tr.sentTurns()[0]; got != gateReplyPlan {
		t.Fatalf("sent %q", got)
	}
}

// For each card kind, a stale revision, a stale card, a choice the card does
// not offer and a page-only choice are refused before anything runs.
func TestDecisionAuthorityAndRevisionMatrix(t *testing.T) {
	cases := []struct {
		name   string
		board  func(t *testing.T) *Workspace
		id, v  string
		intact func(w *Workspace) bool
	}{
		{"change", func(t *testing.T) *Workspace { w, _ := heldChangeBoard(t); return w }, "change.apply", "",
			func(w *Workspace) bool { c := w.currentChange(); return c != nil && !c.applying }},
		{"requirements", func(t *testing.T) *Workspace { return reqBoard(t) }, "requirements.confirm", "",
			func(w *Workspace) bool { return w.currentReq() != nil }},
		{"ask", func(t *testing.T) *Workspace { return askBoard(t) }, "card.choose", "ask:1",
			func(w *Workspace) bool { return w.currentAsk() != nil }},
		{"guard", func(t *testing.T) *Workspace { return guardBoard(t, false) }, "card.choose", "guard:K",
			func(w *Workspace) bool { return w.guard.alert != nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := c.board(t)
			t.Cleanup(func() { v2Cards.Delete(w) })
			f := webCard(t, w)
			webChoice(t, f, c.id, c.v)
			for name, tc := range map[string]struct {
				edit func(*webboard.Action)
				want string
			}{
				"stale revision": {func(a *webboard.Action) { a.Revision += ":old" }, "stale"},
				"stale card":     {func(a *webboard.Action) { a.Card = "decision:000" }, "stale"},
				"not offered":    {func(a *webboard.Action) { a.Choice = "plan.approve" }, "refused"},
				"other value":    {func(a *webboard.Action) { a.Value = "ask:99" }, "refused"},
			} {
				if res := webDecideNow(t, w, f, c.id, c.v, tc.edit); res.Status != tc.want {
					t.Errorf("%s: %+v", name, res)
				}
				if !c.intact(w) {
					t.Fatalf("%s ran the choice", name)
				}
			}
			if res := webDecideNow(t, w, f, c.id, c.v, nil); res.Status == "stale" {
				t.Fatalf("current choice: %+v", res)
			}
		})
	}
	// A page-only choice is never run by the Board.
	w := emptyBoardWorkspace(t)
	t.Cleanup(w.Close)
	t.Cleanup(func() { v2Cards.Delete(w) })
	f := webboard.Frame{Decision: &webboard.Decision{Kind: decisionPlan, Revision: "r", Choices: []webboard.Choice{{ID: "plan.comment", Local: true}}}}
	if res := w.webDecide(f, webboard.Action{Choice: "plan.comment", Revision: "r"}); res.Status != "refused" {
		t.Fatalf("local choice: %+v", res)
	}
}

// A card's legacy key resolves to its typed choice and runs under the same
// checks: k on the change card keeps; a on the inbox card begins the allow
// of the request the card names, even with the inbox selection elsewhere;
// Enter on a plan card no one opened is refused.
func TestLegacyActionUsesTypedAuthority(t *testing.T) {
	w, tr := heldChangeBoard(t)
	if res := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(webCard(t, w)), Key: "k"}); res.Status != "done" {
		t.Fatalf("k: %+v", res)
	}
	if w.currentChange() != nil {
		t.Fatal("legacy k did not keep the current goal")
	}
	chatWaitFor(t, "the keep message", func() bool { return len(tr.sentTurns()) == 1 })

	ib := emptyBoardWorkspace(t)
	t.Cleanup(ib.Close)
	t.Cleanup(func() { v2Cards.Delete(ib) })
	ib.permissions.mu.Lock()
	ib.permissions.inbox = []permission.Request{
		{Kind: "network", Object: "proxy.golang.org", Reason: "go mod download", RunID: "R-1", TaskID: "T03"},
		{Kind: "network", Object: "example.org", Reason: "docs", RunID: "R-1", TaskID: "T04"},
	}
	ib.permissions.mu.Unlock()
	items := ib.boardInbox()
	ib.inboxView.Update(items)
	ib.inboxView.Move(1)
	f := webCard(t, ib)
	if !strings.HasSuffix(f.Decision.Revision, items[0].Canonical) {
		t.Fatalf("inbox revision %q", f.Decision.Revision)
	}
	res := ib.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(f), Key: "a"})
	if res.Status != "done" || res.Confirm == nil || ib.inboxView.Selected() != items[0].Request.Key() || ib.inboxView.Action() != 'a' {
		t.Fatalf("legacy a: %+v selected %q", res, ib.inboxView.Selected())
	}
	if items[0].Gate == boardmodel.GateTyped && !res.Confirm.Text {
		t.Fatal("a hard gate's confirmation does not ask for its id")
	}
	res = webDecideNow(t, ib, webCard(t, ib), "inbox.decide", "allow", func(a *webboard.Action) { a.Confirmed, a.Text = true, items[0].Canonical })
	// This Board has no runtime: the decision reaches the approval writer
	// for the card's request, which refuses for want of one.
	if res.Status != "refused" || !strings.Contains(res.Text, "no runtime is attached") || ib.inboxView.Confirming() {
		t.Fatalf("confirm: %+v", res)
	}

	if !ownerPlanGate {
		return
	}
	pw, _, ptr := boardWithNativeChat(t)
	t.Cleanup(func() { v2Cards.Delete(pw) })
	pw.planSource = fakePlanSource{}
	pw.setMarshalPanel(draftingPanel("awaiting plan approval; /marshal approve"))
	if res := pw.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(webCard(t, pw)), Key: "⏎"}); res.Status != "refused" || pw.v2PlanArmed() != "" || len(ptr.sentTurns()) != 0 {
		t.Fatalf("legacy Enter on an unread plan: %+v", res)
	}
	if res := pw.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(webCard(t, pw)), Key: "7"}); res.Status != "done" {
		t.Fatalf("legacy 7: %+v", res)
	}
	if res := pw.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(webCard(t, pw)), Key: "⏎"}); res.Status != "done" || res.Confirm == nil {
		t.Fatalf("legacy Enter on a read plan: %+v", res)
	}
}
