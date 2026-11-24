package tui

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// The browser's message composer. Every message the owner types in the page
// goes through one writer (webOwnerMessage) under a typed op that binds the
// context the words were written against:
//
//	run/chat.send              the Marshal chat's current launch
//	run/plan.comment           the plan ID and digest the owner read
//	run/architecture.comment   the architecture card's current revision
//	run/goal.intake            the run's current revision and plan
//	run/goal.amend.propose     the same, and then the terminal's amendment
//
// A comment is the owner's words with its scope named; it cannot approve
// anything. The request is committed to the journal (its receipt) before
// the words leave, the receipt records that the message was admitted, never
// what the model replies, and a delivery that was started but never
// acknowledged is held for inspection after a restart: it is never sent
// again.
var webComposerOps = []string{"run/chat.send", "run/plan.comment", "run/architecture.comment", "run/goal.intake", "run/goal.amend.propose"}

// webMessagesInFlight are the requests this process started and has not
// finished; only a request found started without being in it was cut off.
var webMessagesInFlight sync.Map

func webComposerOp(op string) bool { return slices.Contains(webComposerOps, op) }

func webBindingHash(parts ...string) string {
	raw, _ := json.Marshal(parts)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// webChatReachable is whether a Marshal chat can take a message now.
func (w *Workspace) webChatReachable() bool {
	w.chat.mu.Lock()
	native := w.chat.tr != nil
	w.chat.mu.Unlock()
	return native || w.sayPane() != ""
}

// webComposerBinding is the revision and binding an op takes against run
// now, false when the op is not offered.
func (w *Workspace) webComposerBinding(op string, run marshal.Run, revision int64) (int64, string, bool) {
	if run.State == marshal.Closed {
		return 0, "", false
	}
	switch op {
	case "run/chat.send":
		if !w.webChatReachable() {
			return 0, "", false
		}
		return 0, webBindingHash("chat", w.currentAskLaunchID(marshalProposal{})), true
	case "run/plan.comment":
		if run.PlanID == "" || len(run.Tasks) == 0 || !w.webChatReachable() {
			return 0, "", false
		}
		return 0, webBindingHash("plan", run.PlanID, planPreviewOf(run).Digest), true
	case "run/architecture.comment":
		d, ok := w.v2DecisionNow(time.Now())
		if !ok || d.kind != decisionArchitecture || !w.webChatReachable() {
			return 0, "", false
		}
		return 0, webBindingHash("architecture", d.key, w.decisionRevision(d)), true
	case "run/goal.intake":
		if !w.webChatReachable() {
			return 0, "", false
		}
		return revision, webBindingHash("goal", run.GoalBinding, run.PlanID), true
	case "run/goal.amend.propose":
		if run.PlanID == "" || run.State == marshal.Drafting {
			return 0, "", false
		}
		return revision, webBindingHash("amend", run.GoalBinding, run.PlanID), true
	}
	return 0, "", false
}

func (w *Workspace) webComposerCapabilities(run marshal.Run, runID string, revision int64) []webboard.SettingCapability {
	var caps []webboard.SettingCapability
	for _, op := range webComposerOps {
		if rev, binding, ok := w.webComposerBinding(op, run, revision); ok {
			caps = append(caps, webboard.SettingCapability{Op: op, Target: webboard.Target{Kind: "run", ID: runID}, RunID: runID, Revision: rev, AttemptID: binding})
		}
	}
	return caps
}

// webComposerText is the owner's message with its scope named, so the
// Marshal reads a comment as a comment and not as an answer or approval.
func webComposerText(op string, run marshal.Run, text string) string {
	switch op {
	case "run/plan.comment":
		return fmt.Sprintf("Owner comment on plan %s (digest %s). This is a comment, not an approval: %s", run.PlanID, planPreviewOf(run).Digest, text)
	case "run/architecture.comment":
		return "Owner comment on the architecture under review. This is a comment, not an approval: " + text
	case "run/goal.intake":
		return "Owner goal details (run " + run.PlanID + "). Propose any Goal change through the proposal tool; nothing is approved by this message: " + text
	}
	return text
}

// webComposerCommand rechecks the admitted binding against the run now,
// records the start in the journal and returns the delivery to run after
// the commit.
func (w *Workspace) webComposerCommand(i store.WebIntent, p webboard.SettingPayload, service *app.MarshalService) (func() webboard.Result, webboard.Result) {
	record, err := service.Store.GetMarshalRun(context.Background(), service.ProjectID, i.RunID)
	if err != nil {
		return nil, refusedErr(err)
	}
	rev, binding, ok := w.webComposerBinding(i.Op, record.Value, record.Revision)
	if !ok || binding != i.AttemptID || rev != i.Revision {
		return nil, refused("The chat, plan, review or goal changed since the page showed it; refresh and write again.")
	}
	webMessagesInFlight.Store(i.RequestID, true)
	if err = w.store.StartWebRequest(context.Background(), i, i.AttemptID); err != nil {
		webMessagesInFlight.Delete(i.RequestID)
		return nil, refused("The message journal could not be recorded.")
	}
	return func() webboard.Result {
		res := w.webOwnerMessage(i, record.Value, p.Text, func(text string) webboard.Result {
			if i.Op == "run/goal.amend.propose" {
				// The existing amendment writer: a major change waits for the
				// owner's approval and the run is left as it is meanwhile.
				out, err := w.marshalAmend(w.workers.context(), text)
				if err != nil {
					return refusedErr(err)
				}
				return webboard.Result{Status: "done", Text: out}
			}
			return w.webDeliverChat(webComposerText(i.Op, record.Value, text))
		})
		if res.Status == "sent" || res.Status == "queued" {
			res.Status = "done"
		}
		return res
	}, webboard.Result{Status: "started"}
}

// webDeliverChat hands text to the Marshal chat as the owner's words.
func (w *Workspace) webDeliverChat(text string) webboard.Result {
	w.chat.mu.Lock()
	native := w.chat.tr != nil
	w.chat.mu.Unlock()
	if !native && w.sayPane() == "" {
		return refused("No Marshal chat is open, so nothing was sent. /marshal chat in the terminal starts one.")
	}
	w.sayToMarshal(w.workers.context(), text)
	if echo := w.sayEchoList(); len(echo) > 0 {
		switch echo[0].State {
		case sayQueued, saySending:
			if !native {
				return webboard.Result{Status: "queued", Text: "Queued: the Marshal's chat gets it as soon as it is free; Activity shows when it is in."}
			}
		case sayFailed:
			return refused("Not sent: " + echo[0].Why + ".")
		}
	}
	return webboard.Result{Status: "sent", Text: "Sent to the Marshal."}
}

// webOwnerMessage is the one writer for owner messages: the journal entry
// is already started; deliver runs once, and the receipt records the
// admission of the message, not any reply.
func (w *Workspace) webOwnerMessage(i store.WebIntent, run marshal.Run, text string, deliver func(string) webboard.Result) webboard.Result {
	webMessagesInFlight.Store(i.RequestID, true)
	defer webMessagesInFlight.Delete(i.RequestID)
	res := deliver(strings.TrimSpace(text))
	status, code := "applied", "admitted"
	switch res.Status {
	case "sent", "done":
	case "queued":
		code = "queued_for_chat"
	default:
		status, code = "refused", "refused"
	}
	if res.Status != "refused" {
		w.RecordActivity("From the web: a message to the Marshal (" + res.Status + ").")
	}
	effects := []string{"owner-message"}
	if status == "refused" {
		effects = []string{}
	}
	if st := w.store; st != nil {
		_ = st.FinishWebRequest(context.Background(), i, store.WebReceipt{RequestID: i.RequestID, Status: status, Code: code, Revision: i.Revision, Effects: effects, Result: res.Text})
	}
	w.requestRepaint()
	return res
}

// webSay is "Tell the Marshal…" from an older page: it enters the same
// writer as the typed chat op, under a request of its own.
func (w *Workspace) webSay(text string) webboard.Result {
	text = strings.TrimSpace(text)
	if text == "" {
		return refused("Write something to send.")
	}
	if utf8.RuneCountInString(text) > webboard.SayMax {
		return refused("The message is too long: at most " + fmt.Sprint(webboard.SayMax) + " characters.")
	}
	if !w.webChatReachable() {
		// Nothing is sent and the page keeps the text.
		return refused("No Marshal chat is open, so nothing was sent. /marshal chat in the terminal starts one.")
	}
	i, ok := w.webLegacyIntent(text)
	if !ok {
		res := w.webDeliverChat(text)
		w.RecordActivity("From the web: a message to the Marshal (" + res.Status + ").")
		return res
	}
	return w.webOwnerMessage(i, marshal.Run{}, text, w.webDeliverChat)
}

// webLegacyIntent journals a legacy message under the chat's current launch.
func (w *Workspace) webLegacyIntent(text string) (store.WebIntent, bool) {
	if w.store == nil || w.runtime == nil {
		return store.WebIntent{}, false
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return store.WebIntent{}, false
	}
	m := w.marshalSession()
	m.mu.Lock()
	runID := m.runID
	m.mu.Unlock()
	payload, _ := json.Marshal(webboard.SettingPayload{Text: text})
	w.mu.RLock()
	project, epoch := w.ultraProjectLocked(), webboard.SettingsEpoch(w.webSettingsFenceLocked())
	w.mu.RUnlock()
	binding := webBindingHash("chat", w.currentAskLaunchID(marshalProposal{}))
	i := store.WebIntent{ProjectID: project, ProjectEpoch: epoch, Owner: "owner", Session: "browser", RequestID: hex.EncodeToString(id[:]), Digest: binding + string(payload), Op: "run/chat.send", Key: runID, TargetKind: "run", Value: string(payload), RunID: runID, AttemptID: binding}
	if _, err := w.store.AdmitWebSetting(context.Background(), i); err != nil {
		return store.WebIntent{}, false
	}
	if err := w.store.StartWebRequest(context.Background(), i, binding); err != nil {
		return store.WebIntent{}, false
	}
	return i, true
}

// reconcileWebMessage holds a message whose delivery was started but never
// acknowledged: the words may or may not have reached the chat, so they are
// never sent again; the receipt says so for the owner to inspect.
func (w *Workspace) reconcileWebMessage(ctx context.Context, i store.WebIntent) {
	if _, live := webMessagesInFlight.Load(i.RequestID); live {
		return
	}
	_ = w.store.FinishWebRequest(ctx, i, store.WebReceipt{RequestID: i.RequestID, Status: "interrupted", Code: "delivery_unknown", Revision: i.Revision, Effects: []string{}, Result: "The Board restarted while this message was being sent. Check the Marshal chat before writing it again; it was not sent a second time."})
}
