package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/webboard"
)

func composerWorkspace(t *testing.T, session string) (*Workspace, *fakeChat) {
	t.Helper()
	w, _ := receiptTestWorkspace(t, session)
	seedMarshalRunForReceiptTest(t, w, "work", marshal.Run{State: marshal.Dispatching, PlanID: "plan-1", PlanVersion: 1, Tasks: []marshal.Task{{PlanTaskID: "one", Title: "One", Worker: "codex", State: marshal.Queued}}})
	c := &fakeChat{sent: make(chan string, 8)}
	w.chat.mu.Lock()
	w.chat.tr, w.chat.launch = c, "launch-1"
	w.chat.mu.Unlock()
	t.Cleanup(func() { w.chat.mu.Lock(); w.chat.tr = nil; w.chat.mu.Unlock() })
	return w, c
}

func composerSent(t *testing.T, c *fakeChat) string {
	t.Helper()
	select {
	case got := <-c.sent:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("the message never reached the chat")
	}
	return ""
}

func composerNothingSent(t *testing.T, c *fakeChat) {
	t.Helper()
	time.Sleep(100 * time.Millisecond)
	select {
	case got := <-c.sent:
		t.Fatalf("a message was sent: %q", got)
	default:
	}
}

func TestNewGoalAndNewPlanUseBrowserComposer(t *testing.T) {
	w, c := composerWorkspace(t, "composer-goal")
	for _, op := range []string{"run/chat.send", "run/plan.comment", "run/goal.intake", "run/goal.amend.propose"} {
		mustRunCapability(t, w, op)
	}
	service := w.runtime.Marshal()
	before, _ := service.Snapshot(t.Context(), "work")
	intake := runTestAction(t, w, mustRunCapability(t, w, "run/goal.intake"), "a0112233445566778899aabbccddeeff", webboard.SettingPayload{Text: "Also support offline mode"})
	if r := w.webWorkCommand(intake); r.Status != "done" {
		t.Fatalf("intake: %+v", r)
	}
	if got := composerSent(t, c); !strings.Contains(got, "Also support offline mode") || !strings.Contains(got, "nothing is approved") {
		t.Fatalf("intake message: %q", got)
	}
	if r := runReceipt(t, w, intake); r.Status != "applied" || len(r.Effects) != 1 || r.Effects[0] != "owner-message" {
		t.Fatalf("intake receipt: %+v", r)
	}
	after, _ := service.Snapshot(t.Context(), "work")
	if after.State != before.State || after.PlanID != before.PlanID || after.PlanVersion != before.PlanVersion || after.ApprovalScopeDigest != before.ApprovalScopeDigest {
		t.Fatalf("intake changed the run: %+v", after)
	}
	// Without a chat to take the words, nothing offers to send them.
	w.chat.mu.Lock()
	w.chat.tr = nil
	w.chat.mu.Unlock()
	for _, op := range []string{"run/chat.send", "run/plan.comment", "run/goal.intake"} {
		if _, ok := runCapability(w, op); ok {
			t.Fatalf("%s offered without a chat", op)
		}
	}
}

func TestPlanCommentUsesScopedOwnerComposer(t *testing.T) {
	w, c := composerWorkspace(t, "composer-plan")
	service := w.runtime.Marshal()
	run, _ := service.Snapshot(t.Context(), "work")
	digest := planPreviewOf(run).Digest
	comment := runTestAction(t, w, mustRunCapability(t, w, "run/plan.comment"), "b0112233445566778899aabbccddeeff", webboard.SettingPayload{Text: "Split task one"})

	if r := w.webWorkCommand(comment); r.Status != "done" {
		t.Fatalf("comment: %+v", r)
	}
	got := composerSent(t, c)
	if !strings.Contains(got, "plan-1") || !strings.Contains(got, digest) || !strings.Contains(got, "not an approval") || !strings.HasSuffix(got, "Split task one") {
		t.Fatalf("scoped message: %q", got)
	}
	if r := runReceipt(t, w, comment); r.Status != "applied" || r.Code != "admitted" {
		t.Fatalf("receipt: %+v", r)
	}
	after, _ := service.Snapshot(t.Context(), "work")
	if after.State != run.State || after.ApprovalScopeDigest != run.ApprovalScopeDigest || after.PlanVersion != run.PlanVersion || len(after.Commands) != 0 {
		t.Fatalf("comment touched approval state: %+v", after)
	}

	// A comment written against a plan that was replaced is refused.
	stale := runTestAction(t, w, mustRunCapability(t, w, "run/plan.comment"), "b1112233445566778899aabbccddeeff", webboard.SettingPayload{Text: "Too late"})
	record, _ := service.Store.GetMarshalRun(t.Context(), service.ProjectID, "work")
	record.Value.Tasks[0].Title = "Renamed"
	if _, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, "work", record.Value, record.Revision); err != nil {
		t.Fatal(err)
	}
	if r := w.webWorkCommand(stale); r.Status != "refused" {
		t.Fatalf("stale plan comment: %+v", r)
	}
	composerNothingSent(t, c)
	if r := runReceipt(t, w, stale); r.Status != "queued" {
		t.Fatalf("stale receipt: %+v", r)
	}
}

func TestChatSendRefusesChangedLaunch(t *testing.T) {
	w, c := composerWorkspace(t, "composer-launch")
	send := runTestAction(t, w, mustRunCapability(t, w, "run/chat.send"), "e0112233445566778899aabbccddeeff", webboard.SettingPayload{Text: "hello"})
	w.chat.mu.Lock()
	w.chat.launch = "launch-2"
	w.chat.mu.Unlock()
	if r := w.webWorkCommand(send); r.Status != "refused" {
		t.Fatalf("stale launch: %+v", r)
	}
	composerNothingSent(t, c)
	if _, ok := runCapability(w, "run/architecture.comment"); ok {
		t.Fatal("architecture comment offered with no review open")
	}
}

func TestLegacySayAndTypedChatShareOutbox(t *testing.T) {
	w, c := composerWorkspace(t, "composer-say")
	typed := runTestAction(t, w, mustRunCapability(t, w, "run/chat.send"), "c0112233445566778899aabbccddeeff", webboard.SettingPayload{Text: "typed"})
	if r := w.webWorkCommand(typed); r.Status != "done" {
		t.Fatalf("typed: %+v", r)
	}
	if got := composerSent(t, c); got != "typed" {
		t.Fatalf("typed message: %q", got)
	}
	if r := runReceipt(t, w, typed); r.Status != "applied" || r.Effects[0] != "owner-message" {
		t.Fatalf("typed receipt: %+v", r)
	}
	f := webboard.FromBoard(w.webBoardFrame())
	legacy := w.webBoardAct(t.Context(), webboard.Action{Kind: "say", Card: webboard.CardID(f), Text: "/marshal stop"})
	if legacy.Status != "sent" {
		t.Fatalf("legacy: %+v", legacy)
	}
	if got := composerSent(t, c); got != "/marshal stop" {
		t.Fatalf("legacy message: %q", got)
	}
	// Both entered the one journal and neither is left unfinished.
	left, err := w.store.UnfinishedWebRequests(t.Context(), w.ultraProjectLocked())
	if err != nil || len(left) != 0 {
		t.Fatalf("unfinished: %+v %v", left, err)
	}
	i, ok, _ := w.webLegacyIntent("again")
	if !ok {
		t.Fatal("a legacy message was not journaled")
	}
	if r := w.webOwnerMessage(i, marshal.Run{}, "again", w.webDeliverChat); r.Status != "sent" {
		t.Fatalf("legacy writer: %+v", r)
	}
	composerSent(t, c)
	rec, err := w.store.WebReceipt(t.Context(), i.ProjectID, i.Owner, i.RequestID, i.Digest)
	if err != nil || rec.Status != "applied" || rec.Effects[0] != "owner-message" {
		t.Fatalf("legacy receipt: %+v %v", rec, err)
	}
}

func TestComposerLostReplyReconcilesWithoutResend(t *testing.T) {
	w, c := composerWorkspace(t, "composer-lost")
	send := runTestAction(t, w, mustRunCapability(t, w, "run/chat.send"), "d0112233445566778899aabbccddeeff", webboard.SettingPayload{Text: "once"})
	if r := w.webWorkCommand(send); r.Status != "done" {
		t.Fatalf("send: %+v", r)
	}
	composerSent(t, c)
	// The reply was lost and the same request arrives again: the receipt
	// answers, nothing is sent a second time.
	if r := w.webWorkCommand(send); r.Status != "refused" {
		t.Fatalf("replay: %+v", r)
	}
	w.reconcileWebWork(t.Context())
	composerNothingSent(t, c)
	if r := runReceipt(t, w, send); r.Status != "applied" {
		t.Fatalf("receipt after replay: %+v", r)
	}

	// A delivery started before a restart, with no acknowledgement, is held
	// for inspection and never sent again.
	cut := runTestAction(t, w, mustRunCapability(t, w, "run/chat.send"), "d1112233445566778899aabbccddeeff", webboard.SettingPayload{Text: "cut off"})
	if err := w.store.StartWebRequest(t.Context(), *cut.Command, cut.Command.AttemptID); err != nil {
		t.Fatal(err)
	}
	webMessagesInFlight.Store(cut.Command.RequestID, true)
	w.reconcileWebWork(t.Context())
	if r := runReceipt(t, w, cut); r.Status != "applying" {
		t.Fatalf("a live delivery was held: %+v", r)
	}
	webMessagesInFlight.Delete(cut.Command.RequestID)
	w.reconcileWebWork(t.Context())
	if r := runReceipt(t, w, cut); r.Status != "interrupted" || r.Code != "delivery_unknown" {
		t.Fatalf("held receipt: %+v", r)
	}
	composerNothingSent(t, c)
}

func TestLegacyReceiptDigestDoesNotHoldTheMessage(t *testing.T) {
	w, c := composerWorkspace(t, "composer-digest")
	i, ok, refusal := w.webLegacyIntent("the vault word is swordfish")
	if !ok || refusal != nil {
		t.Fatalf("not journaled: %v", refusal)
	}
	if strings.Contains(i.Digest, "swordfish") || len(i.Digest) != 64 {
		t.Fatalf("digest holds the message: %q", i.Digest)
	}
	if _, live := webMessagesInFlight.Load(i.RequestID); !live {
		t.Fatal("a started message is not live before its delivery")
	}
	w.reconcileWebMessage(t.Context(), i)
	if left, _ := w.store.UnfinishedWebRequests(t.Context(), i.ProjectID); len(left) != 1 {
		t.Fatalf("a live message was reconciled: %+v", left)
	}
	w.webOwnerMessage(i, marshal.Run{}, "x", w.webDeliverChat)
	composerSent(t, c)
}

func TestLegacySayRefusesWhenJournalFails(t *testing.T) {
	w, c := composerWorkspace(t, "composer-nojournal")
	first, ok, _ := w.webLegacyIntent("first")
	if !ok {
		t.Fatal("first not journaled")
	}
	// A message already in flight for the run holds the slot.
	if r := w.webSay("second"); r.Status != "refused" {
		t.Fatalf("an unjournaled message was delivered: %+v", r)
	}
	composerNothingSent(t, c)
	webMessagesInFlight.Delete(first.RequestID)
}

func TestComposerCommentsBindTheChatLaunch(t *testing.T) {
	w, c := composerWorkspace(t, "composer-bind")
	for _, op := range []string{"run/plan.comment", "run/goal.intake"} {
		cmd := runTestAction(t, w, mustRunCapability(t, w, op), "f0112233445566778899aabbccdd"+map[string]string{"run/plan.comment": "0001", "run/goal.intake": "0002"}[op], webboard.SettingPayload{Text: "hi"})
		w.chat.mu.Lock()
		w.chat.launch = "launch-" + op
		w.chat.mu.Unlock()
		if r := w.webWorkCommand(cmd); r.Status != "refused" {
			t.Fatalf("%s on a new launch: %+v", op, r)
		}
	}
	composerNothingSent(t, c)
}

func TestWebDeliverChatRefusesControlOnlyText(t *testing.T) {
	w, c := composerWorkspace(t, "composer-control")
	if r := w.webDeliverChat("\x07\x08"); r.Status != "refused" {
		t.Fatalf("control-only text: %+v", r)
	}
	composerNothingSent(t, c)
}

func TestRunDetailsRedactKnownSecrets(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "composer-details")
	m := w.marshalSession()
	m.mu.Lock()
	m.runID = "work"
	m.mu.Unlock()
	seedMarshalRunForReceiptTest(t, w, "work", marshal.Run{State: marshal.Dispatching, PlanID: "plan-1", PlanVersion: 1, Tasks: []marshal.Task{{PlanTaskID: "one", Title: "Rotate hunter2zz", Worker: "codex", State: marshal.Queued, Files: []string{"hunter2zz.txt"}, Checks: []marshal.Check{{Command: "echo hunter2zz"}}}}})
	w.mu.Lock()
	w.state.KnownSecrets = []string{"hunter2zz"}
	w.mu.Unlock()
	d := w.webRunDetails(t.Context())
	if len(d.Tasks) != 1 {
		t.Fatalf("tasks: %+v", d.Tasks)
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), "hunter2zz") {
		t.Fatalf("a known secret reached the details: %s", raw)
	}
}
