//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// launchEarlierWorkThroughQueue: the launch scan loads earlier sessions by
// queueing the ordinary read request (shipped decides it automatically);
// governed loads directly only under an operator grant already on record.
const launchEarlierWorkThroughQueue = true

const continuationMemoryHint = "Summarise completed work, unfinished work, decisions and conventions from the supplied project-scoped data. Use the candidate IDs and provenance supplied above; propose each candidate by atomically writing {\"action\":\"memory\",\"id\":\"candidate-id\"} to a fresh .marshal/proposals/<unique-request-id>.json file; do not print the payload in chat. MARSHAL applies your own memory proposals directly.\n"

// buildRoleAuthority is the private authority protocol exactly as delivered:
// the Marshal build serves private state from its own directory, so the
// protocol's state paths name that directory rather than ".marshal/".
var buildRoleAuthority = strings.ReplaceAll(constitution.ExtremeAuthorityProtocol, ".marshal/", statepath.DirName+"/")

type buildPermissionState struct {
	deliveryMu sync.Mutex
	pending    map[string]*buildPendingDecision
	decisions  []marshalDecisionRecord
	// awaiting holds cross-actor requests queued while no Marshal is live.
	awaiting map[string]bool
	// redeliver tracks requests the Marshal did not answer in time; they are
	// re-delivered with backoff instead of denied.
	redeliver map[string]*extremeRedelivery
}
type marshalDecisionRecord struct {
	requestID string
	request   string
	allow     bool
	reason    string
	provider  string
	binding   string
}
type buildPendingDecision struct {
	paneID     string
	text       string
	delivered  bool
	blocked    bool
	launchID   string
	occurrence string
	request    permission.Request
	deadline   time.Time
	provider   string
	answer     *bool
	reason     string
	// bound marks a private review deadline that must hold as set.
	bound bool
	// window is how long the Marshal has to answer once the request is in
	// its chat; zero keeps the deadline as set. A request whose delivery
	// waits (an unsent paste, a busy chat, a restart) is not dead on arrival.
	window time.Duration
}

// extendForDelivery gives an unbound request its whole answer window from
// now: on delivery, and while delivery is still pending.
func (p *buildPendingDecision) extendForDelivery(now time.Time) {
	if p.bound || p.window <= 0 {
		return
	}
	if d := now.Add(p.window); d.After(p.deadline) {
		p.deadline = d
	}
}

// markDelivered records that the request reached the Marshal chat.
func (p *buildPendingDecision) markDelivered(now time.Time) {
	p.delivered = true
	p.blocked = false
	p.extendForDelivery(now)
}

// Queue processing remains asynchronous: stop and proposal polling never wait
// for a model response. The request ID is fresh for each admitted occurrence.
func (w *Workspace) routeBuildPermission(ctx context.Context, req permission.Request) bool {
	// Reduced guarantees require the owner, even when the Marshal normally approves plans.
	if req.OwnerConfirmation {
		return false
	}
	if req.Kind == "network" && req.ProposalID == "" && req.SourceLaunch == "" {
		if provider, automatic := w.runtime.AutomaticProviderEgressRequest(ctx, req.RunID, req.Object); automatic {
			w.finishBuildDecision(ctx, req, "provider-egress:"+req.RunID, true, "Registered provider CLI vendor, telemetry or standard toolchain host", provider)
			return true
		}
	}
	// Provider credentials (standing or one bounded invocation) apply
	// automatically in this edition: never a popup or a Marshal decision turn.
	// The runtime normally grants them before any request is raised; this
	// covers an explicit /permission credential request.
	if (req.Kind == "credential" || req.Kind == "credential-bound") && req.ProposalID == "" && req.SourceLaunch == "" {
		w.finishBuildDecisionAs(ctx, req, "credential:"+req.Object, true, credentialRuntimeReason, req.Object, marshalRuntimeDecider)
		return true
	}
	// Network and reads never wait for anyone in this edition.
	if w.routeAutomaticWork(ctx, req) {
		return true
	}
	// A transport approval already decided can arrive again from the
	// question batch; it needs no second decision and no refusal record.
	if privateProcess05QuestionIntent(req) && w.settledProcess05Approval(req) {
		return true
	}
	// A run proposal bound to an older state of its run can no longer be
	// applied; it is settled here instead of being asked again.
	if w.expiredRunProposal(ctx, req) {
		return true
	}
	launch := w.proposalLaunchID()
	// The chat pane is read only when a decision for this request is due,
	// and before the lock.
	w.permissions.mu.Lock()
	due := false
	for _, p := range w.permissions.build.pending {
		if p.request.Key() == req.Key() && p.answer == nil && !p.bound && !time.Now().Before(p.deadline) {
			due = true
		}
	}
	w.permissions.mu.Unlock()
	working := due && w.marshalChatWorking(ctx)
	w.permissions.mu.Lock()
	for id, p := range w.permissions.build.pending {
		if p.request.Key() != req.Key() {
			continue
		}
		// A request delivered to an earlier Marshal launch can no longer be
		// answered by it; treat it as unanswered so the live Marshal gets it.
		relaunched := p.launchID != "" && launch != "" && p.launchID != launch
		// A Marshal still in the turn it was in when the request arrived has
		// not had a chance to answer. Re-issuing the request under a new id
		// then only grew a queue the Marshal spent its next turns
		// investigating, so the same request keeps waiting instead.
		if p.answer == nil && !p.bound && !relaunched && working && !time.Now().Before(p.deadline) {
			p.deadline = time.Now().Add(marshalBusyDecisionGrace)
		}
		// A request the chat has not been given yet cannot have been
		// answered late: its clock starts on delivery.
		// (Codex questions keep their own audited expiry and re-delivery.)
		if p.answer == nil && !relaunched && !p.delivered && p.provider != "codex" && !time.Now().Before(p.deadline) {
			p.extendForDelivery(time.Now())
		}
		if p.answer == nil && time.Now().Before(p.deadline) && !relaunched {
			w.permissions.mu.Unlock()
			if p.provider == "codex" {
				if !deferPrivateDecisionDelivery(ctx) {
					w.deliverCodexDecision(ctx, id, p)
				}
			} else if p.paneID != nativeChatPane {
				w.deliverQueuedChatDecision(ctx, id, p)
			}
			w.queuePendingDecision(id, p)
			return true
		}
		delete(w.permissions.build.pending, id)
		if p.answer == nil {
			// This edition never denies work because the Marshal was slow.
			w.permissions.mu.Unlock()
			w.chatRequestExpired(id)
			w.settleUnansweredDecision(ctx, req, id, p, "decision timeout")
			return true
		}
		delete(w.permissions.outstanding, req.Key())
		allow, reason := *p.answer, p.reason
		if p.occurrence != "" {
			original := req
			req.SourceLaunch = p.launchID
			req.SourceOccurrence = p.occurrence
			// Runtime run questions, and run proposals that came through the
			// file channel, start without model provenance. Once an
			// authenticated decide receipt supplies it, retain the exact run
			// proposal under the resulting permission key. The canonical apply
			// path still checks the live lease, receipt and run revision.
			if allow && original == p.request && original.SourceLaunch == "" && original.SourceOccurrence == "" && original.Kind == "marshal-command" && original.RunID != "" {
				if proposal, ok := w.permissions.runProposals[original.Key()]; ok && proposal.command() == original.Object {
					delete(w.permissions.runProposals, original.Key())
					w.permissions.runProposals[req.Key()] = proposal
				}
			}
		}
		w.permissions.mu.Unlock()
		w.finishBuildDecision(ctx, req, id, allow, reason, p.provider)
		return true
	}
	w.permissions.mu.Unlock()
	w.tmuxMu.Lock()
	chat := w.tmuxActiveWins["marshal-chat"]
	pane, provider := "", ""
	if chat != nil && chat.state != "done" && chat.state != "failed" {
		pane, provider = chat.paneID, chat.provider
	}
	w.tmuxMu.Unlock()
	// The native chat is the live Marshal when no tmux chat runs: decisions
	// go to it as a message turn instead of waiting for a pane.
	if pane == "" {
		if native, ok := w.nativeChatDecisionTarget(); ok {
			pane, provider = nativeChatPane, native
		}
	}
	// The Marshal is the approver of memory in this edition. Asking the same
	// model to approve its own write is a self-approval turn that provider
	// safety layers refuse, which left candidates pending until expiry. Its own
	// live-MCP candidate therefore applies directly with recorded provenance.
	// Candidates from any other author still go to the Marshal for a decision.
	if w.routeAutomaticRunAction(ctx, req, provider) {
		return true
	}
	if author, ok := w.marshalAuthoredMemory(ctx, req); ok {
		if provider == "" {
			provider = author
		}
		w.finishBuildDecisionAs(ctx, req, req.ProposalID, true, marshalMemoryRuntimeReason, provider, marshalRuntimeDecider)
		return true
	}
	// Bound model emissions already express approval. Runtime-generated reserved
	// merge requests still require an explicit answer, even about the Marshal.
	if pane != "" && req.ProposalID != "" && req.Kind != "memory" && req.Kind != "project-contract" {
		if req.SourceLaunch != "" {
			w.finishBuildDecision(ctx, req, req.ProposalID, true, "Apply exact live MCP proposal: "+req.Reason, provider)
			return true
		}
		if req.Who == "Marshal" && strings.HasPrefix(req.ProposalID, "runtime:") {
			w.finishBuildDecision(ctx, req, req.ProposalID, true, "Apply exact Marshal proposal: "+req.Reason, provider)
			return true
		}
		// Proposals not from the authenticated Marshal session (e.g. labelled
		// 'Local request (unverified)', or written by workers or other processes)
		// are never auto-applied in extreme mode. The Marshal decides them below,
		// rather than an operator popup.
	}
	if pane == "" {
		// Cross-actor decisions wait for the Marshal; only owner-only kinds
		// (project contract, credentials) can still reach the operator.
		return w.awaitMarshalDecision(req)
	}
	w.stopAwaitingMarshal(req)
	if w.redeliveryWaiting(req) {
		return true
	}
	id, err := model.NewID("decision-")
	if err != nil {
		w.finishBuildDecision(ctx, req, "", false, "request ID failed", provider)
		return true
	}
	// Record the exact request before delivering it. A failed durable audit
	// must not dispatch a request that could result in an unaudited grant.
	if w.store == nil {
		w.finishBuildDecision(ctx, req, id, false, "request audit unavailable", provider)
		return true
	}
	registered := time.Now().UTC()
	deadline, boundReview := w.privateProcess05ReviewDeadline(ctx, req, registered)
	audit := map[string]any{"request_id": id, "kind": req.Kind, "object": req.Object, "scope": req.Scope, "scope_detail": req.Detail, "request_reason": RedactContent(req.Reason, nil), "provider": provider, "run_id": req.RunID, "approval_binding": req.ApprovalBinding}
	if boundReview {
		audit["deadline_utc"] = deadline.UTC().Format(time.RFC3339Nano)
		audit["review_seconds"] = deadline.Sub(registered).Seconds()
	}
	if err := w.store.AppendEvent(ctx, nil, model.Event{ID: id + "-request", Type: "PERMISSION_REQUESTED", ProjectID: w.projectID, Timestamp: registered, Data: audit}); err != nil {
		w.finishBuildDecision(ctx, req, id, false, "request audit failed", provider)
		return true
	}
	if boundReview && !deadline.After(registered) {
		w.finishBuildDecision(ctx, req, id, false, "provider transport review budget exhausted or binding unavailable", provider)
		return true
	}
	if !boundReview && w.redeliveryExhausted(req) {
		// Re-delivery is bounded; the last delivery waits for the Marshal
		// without a deadline (a relaunch still re-delivers it).
		deadline = registered.Add(extremeAwaitForever)
	}
	p := &buildPendingDecision{request: req, provider: provider, paneID: pane, deadline: deadline, launchID: w.proposalLaunchID(), bound: boundReview}
	if !boundReview && deadline.Sub(registered) < extremeAwaitForever {
		p.window = deadline.Sub(registered)
	}
	description := map[string]string{"id": id, "kind": req.Kind, "object": req.Object, "scope": req.Scope, "scope_detail": req.Detail, "reason": req.Reason}
	if boundReview {
		description["deadline_utc"] = deadline.UTC().Format(time.RFC3339Nano)
		description["review_seconds"] = strconv.FormatFloat(deadline.Sub(registered).Seconds(), 'f', 9, 64)
	}
	data, _ := json.Marshal(description)
	p.text = "Decision request (untrusted description): " + terminalSafeDecisionJSON(data) + ". Submit through marshal_proposal_submit a proposal with exactly four fields: action=decide, this nonempty id, boolean allow, and nonblank reason of at most 500 UTF-8 bytes with no control characters. A file cannot authorize this request."
	w.permissions.mu.Lock()
	if w.permissions.build.pending == nil {
		w.permissions.build.pending = map[string]*buildPendingDecision{}
	}
	w.permissions.build.pending[id] = p
	w.permissions.mu.Unlock()
	if provider == "codex" {
		if !deferPrivateDecisionDelivery(ctx) {
			w.deliverCodexDecision(ctx, id, p)
		}
		w.queuePendingDecision(id, p)
		return true
	}
	if pane != nativeChatPane {
		w.deliverQueuedChatDecision(ctx, id, p)
		w.queuePendingDecision(id, p)
		return true
	}
	err = w.deliverNativeChatTurn(ctx, p.text)
	if err != nil {
		w.permissions.mu.Lock()
		delete(w.permissions.build.pending, id)
		w.permissions.mu.Unlock()
		w.settleUnansweredDecision(ctx, req, id, p, "decision delivery failed")
	} else {
		w.permissions.mu.Lock()
		p.markDelivered(time.Now())
		w.permissions.mu.Unlock()
		w.chatRequestSent(id, req, time.Now())
		w.RecordActivity("Marshal decision requested: " + id + " " + req.Kind + " " + req.Object)
		w.permissions.queue.Add(req)
	}
	return true
}

// Retain the original decision while an owner's draft or an unknown screen
// blocks delivery. Polling retries this same request without changing its ID.
func (w *Workspace) deliverQueuedChatDecision(ctx context.Context, id string, p *buildPendingDecision) {
	w.chatTypeMu.Lock()
	defer w.chatTypeMu.Unlock()
	w.permissions.mu.Lock()
	ready := w.permissions.build.pending[id] == p && !p.delivered && p.answer == nil && p.paneID != "" && p.text != ""
	w.permissions.mu.Unlock()
	if !ready {
		return
	}
	run := func(ctx context.Context, args ...string) ([]byte, error) { return tmux.RunCommand(ctx, args...) }
	err := deliverChatTurnAs(ctx, run, composerDialectFor(p.provider), p.paneID, p.text, deliverEnterTries, deliverEnterWait)
	w.permissions.mu.Lock()
	if w.permissions.build.pending[id] != p {
		w.permissions.mu.Unlock()
		return
	}
	if errors.Is(err, errChatNotReady) {
		// The chat is mid-turn over an empty input, or has not drawn its
		// input yet: nothing for the owner to send or clear. The request
		// waits for the chat to be ready.
		p.blocked = false
		p.extendForDelivery(time.Now())
		w.permissions.mu.Unlock()
		return
	}
	if errors.Is(err, errChatBusy) || errors.Is(err, errChatUnsubmitted) {
		// Waiting on the chat is not the Marshal being slow.
		p.blocked = true
		p.extendForDelivery(time.Now())
		w.permissions.mu.Unlock()
		if errors.Is(err, errChatBusy) {
			w.noteChatOnce(chatQueuedOwnerInput)
		} else {
			w.noteChatOnce(chatQueuedUnsent)
		}
		return
	}
	if err == nil {
		p.markDelivered(time.Now())
	}
	if err != nil {
		delete(w.permissions.build.pending, id)
	}
	w.permissions.mu.Unlock()
	if err != nil {
		w.settleUnansweredDecision(ctx, p.request, id, p, "decision delivery failed")
		return
	}
	w.chatDeliveryUnblocked()
	w.chatRequestSent(id, p.request, time.Now())
	w.RecordActivity("Marshal decision requested: " + id + " " + p.request.Kind + " " + p.request.Object)
}

// Only consume the new action here. All other proposals keep their existing
// parser, durable admission, snapshot binding and constitutional checks.
func (w *Workspace) consumeBuildDecision(data []byte) bool {
	// Files have no launch provenance and cannot answer an authority request.
	return false
}

const (
	marshalRuntimeDecider      = "marshal-runtime"
	marshalMemoryRuntimeReason = "Marshal: Marshal-authored memory applies without a self-decision turn"
	credentialRuntimeReason    = "Marshal: provider credential applies automatically"
)

// marshalAuthoredMemory reports whether req retains a candidate the bound live
// Marshal itself submitted through memory_remember or
// marshal_memory_candidate_submit, and returns its provider. Identity comes
// from the journaled broker candidate and launch receipt, never from labels:
// a Marshal proposal naming another author's candidate is not covered.
func (w *Workspace) marshalAuthoredMemory(ctx context.Context, req permission.Request) (string, bool) {
	if req.Kind != "memory" || req.Who != "Marshal" || req.SourceLaunch == "" || req.SourceOccurrence != req.ProposalID || !strings.HasPrefix(req.ProposalID, "mcp:") || w.store == nil {
		return "", false
	}
	c, err := w.store.MCPMemory(ctx, w.projectID, req.ProposalID)
	if err != nil || c.Occurrence != req.ProposalID || c.Record.ID != req.Object || c.Record.ProjectID != w.projectID || c.Record.Source.Kind != "live_mcp" {
		return "", false
	}
	if launch, _ := c.Record.ExtMeta["launch_id"].(string); launch != req.SourceLaunch {
		return "", false
	}
	p, err := w.store.MCPProposal(ctx, w.projectID, req.ProposalID)
	if err != nil || p.Launch != req.SourceLaunch || p.Owner != c.Record.Source.Reference {
		return "", false
	}
	provider, _ := c.Record.ExtMeta["provider"].(string)
	return provider, true
}

func (w *Workspace) finishBuildDecision(ctx context.Context, req permission.Request, id string, allow bool, reason, provider string) {
	w.finishBuildDecisionAs(ctx, req, id, allow, reason, provider, "")
}

// finishBuildDecisionAs applies a decision. An empty decidedBy is a model
// decision; otherwise the runtime decided and the audit names that actor.
func (w *Workspace) finishBuildDecisionAs(ctx context.Context, req permission.Request, id string, allow bool, reason, provider, decidedBy string) {
	// Carry decision provenance through the existing durable permission audit.
	if decidedBy == "" {
		ctx = app.WithExtremePermissionDecision(ctx, id, reason, req.Reason, provider, req.ApprovalBinding)
	} else {
		ctx = app.WithExtremeRuntimeDecision(ctx, id, reason, req.Reason, provider, req.ApprovalBinding, decidedBy)
	}
	beforeNotes := w.noteSeq()
	err := w.decidePermission(ctx, req, allow, "marshal")
	if errors.Is(err, errProposalSettled) {
		// A duplicate delivery of a proposal already being applied or
		// applied: the first decision recorded the outcome and its line
		// ("Antigravity verified."); this one adds neither.
		w.permissions.mu.Lock()
		delete(w.permissions.build.redeliver, req.Key())
		w.permissions.mu.Unlock()
		return
	}
	if err == nil && allow && req.Kind == "marshal-command" && strings.HasPrefix(req.Object, "/marshal approve-task ") {
		w.queuePrivateProcess05Resume()
	}
	if marshalSelfApprovedPlan(req, allow, err) {
		// This edition's Marshal approves its own plans (ultra mode): the
		// owner is told once, plainly, that the plan ran without them.
		w.noteSelfApprovedPlan(req.RunID + "/" + req.TaskID)
	}
	// If handleHarness already wrote the detailed verification Activity note,
	// avoid writing a second summary line for the same verification.
	_, isVerify := cutHarnessVerifyProvider(strings.TrimSpace(req.Object))
	verifyNoted := isVerify && allow && w.noteSeq() != beforeNotes
	if marshalSelfAcceptRefused(req, allow, err) {
		// The Marshal tried to record the owner's own decision; policy
		// refused it. The owner reads that plainly, not the policy text.
		w.noteRefusedArchitecture(req.Object, 0)
	} else if text := marshalDecisionActivity(req.Object, allow, err); err != nil && allow && req.Kind == "read" && req.ContinuationProvider != "" {
		// Loading earlier work is the runtime's own reading of the
		// provider's history, not a Marshal decision the person made.
		w.RecordActivity(earlierWorkFailedLine(req.ContinuationProvider, err))
	} else if text != "" && !verifyNoted && !marshal.IsReplayedEscalation(err) {
		w.RecordActivity(text)
	}

	reqDesc := req.Kind
	if req.Object != "" {
		if reqDesc != "" {
			reqDesc += " " + req.Object
		} else {
			reqDesc = req.Object
		}
	}
	w.permissions.mu.Lock()
	delete(w.permissions.build.redeliver, req.Key())
	w.permissions.build.decisions = append(w.permissions.build.decisions, marshalDecisionRecord{
		requestID: id,
		request:   reqDesc,
		allow:     allow,
		reason:    reason,
		provider:  provider,
		binding:   req.ApprovalBinding,
	})
	w.permissions.mu.Unlock()
}

// marshalSelfAcceptRefused reports whether a decision the Marshal made was
// its own acceptance of the project architecture that policy refused because
// the owner never accepted it (only the owner accepts, in the browser).
func marshalSelfAcceptRefused(req permission.Request, allow bool, err error) bool {
	return allow && req.Kind == "project-contract" && errors.Is(err, store.ErrProjectContractInstructionMissing)
}

// marshalSelfApprovedPlan reports whether a decision the Marshal made
// approved a drafted plan, with no owner in the loop.
func marshalSelfApprovedPlan(req permission.Request, allow bool, err error) bool {
	return err == nil && allow && !req.OwnerConfirmation && req.Kind == "marshal-command" && req.Object == "/marshal approve"
}

// marshalDecisionActivity is the Activity line for a decision the Marshal
// made on the owner's behalf, in plain words, or "" for none. A step that was
// allowed and applied is the Marshal's own machinery, so it adds no line (the
// permission audit and the decision record keep it); a verified harness says
// so as an outcome. A denial or a failed application is shown, since the run
// may now wait on it. The model's reason is never shown: it is protocol text.
// cutHarnessVerifyProvider extracts the provider from a "/harness verify ..."
// or "/harness verify-native ..." command object.
func cutHarnessVerifyProvider(object string) (string, bool) {
	for _, prefix := range []string{"/harness verify ", "/harness verify-native "} {
		if rest, ok := strings.CutPrefix(object, prefix); ok {
			provider, _, _ := strings.Cut(strings.TrimSpace(rest), " ")
			if provider != "" {
				return provider, true
			}
		}
	}
	return "", false
}

func marshalDecisionActivity(object string, allow bool, err error) string {
	object = strings.TrimSpace(object)
	verify, isVerify := cutHarnessVerifyProvider(object)
	if isVerify {
		verify = marshalDisplayName(verify)
	}
	switch {
	case err != nil && isVerify:
		return "Verifying " + verify + " failed: " + RedactContent(err.Error(), nil)
	case err != nil:
		return "The Marshal's decision on " + object + " could not be applied: " + RedactContent(err.Error(), nil)
	case !allow && isVerify:
		return "The Marshal did not verify " + verify + "."
	case !allow:
		return "The Marshal turned down " + object + "."
	case isVerify:
		return verify + " verified."
	}
	return ""
}

func (w *Workspace) collectRecentMarshalDecisions(ctx context.Context) []marshalDecisionRecord {
	var records []marshalDecisionRecord
	if w.store != nil {
		events, err := w.store.ListEvents(ctx)
		if err == nil {
			for _, e := range events {
				if e.Type != "PERMISSION_DECIDED" {
					continue
				}
				source, _ := e.Data["source"].(string)
				if source != "marshal" {
					continue
				}
				reqID, _ := e.Data["request_id"].(string)
				if reqID == "" {
					reqID = e.ID
				}
				kind, _ := e.Data["kind"].(string)
				object, _ := e.Data["object"].(string)
				reqDesc := kind
				if object != "" {
					if reqDesc != "" {
						reqDesc += " " + object
					} else {
						reqDesc = object
					}
				}
				allow, _ := e.Data["allow"].(bool)
				reason, _ := e.Data["decision_reason"].(string)
				if reason == "" {
					reason, _ = e.Data["reason"].(string)
				}
				provider, _ := e.Data["provider"].(string)
				binding, _ := e.Data["approval_binding"].(string)
				records = append(records, marshalDecisionRecord{
					requestID: reqID,
					request:   reqDesc,
					allow:     allow,
					reason:    reason,
					provider:  provider,
					binding:   binding,
				})
			}
		}
	}
	if len(records) == 0 {
		w.permissions.mu.Lock()
		if len(w.permissions.build.decisions) > 0 {
			records = make([]marshalDecisionRecord, len(w.permissions.build.decisions))
			copy(records, w.permissions.build.decisions)
		}
		w.permissions.mu.Unlock()
	}
	if len(records) > 20 {
		records = records[len(records)-20:]
	}
	return records
}

func (w *Workspace) decorateMarshalStatus(ctx context.Context, base string) string {
	decisions := w.collectRecentMarshalDecisions(ctx)
	if len(decisions) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\nrecent Marshal decisions:\n")
	for _, d := range decisions {
		verdict := "deny"
		if d.allow {
			verdict = "allow"
		}
		fmt.Fprintf(&b, "  %s %s: %s — %s", d.requestID, d.request, verdict, d.reason)
		if d.provider != "" {
			fmt.Fprintf(&b, " (provider: %s)", d.provider)
		}
		if d.binding != "" {
			fmt.Fprintf(&b, " [binding: %s]", d.binding)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// settledProcess05Approval reports whether the approval a Process 05
// approve-task request names is no longer waiting for a decision (settled or expired).
func (w *Workspace) settledProcess05Approval(req permission.Request) bool {
	if w.runtime == nil || w.runtime.Execution().Engine() == nil {
		return false
	}
	id, ok := strings.CutPrefix(req.Object, "/marshal approve-task ")
	if !ok || id == "" {
		return false
	}
	a, err := w.runtime.Execution().Engine().ApprovalManager().GetApproval(id)
	if err != nil {
		return false
	}
	if a.Status != execution.ApprovalRequested {
		return true
	}
	if a.ExpiresAt != nil && !time.Now().UTC().Before(*a.ExpiresAt) {
		if _, _, err := w.runtime.ExpireApproval(context.Background(), req.RunID, id, time.Now().UTC()); err != nil {
			w.logTUIError("approval expiry notice", err)
		}
		return true
	}
	return false
}

// expiredRunProposal settles a run proposal whose bound plan version and
// state revision no longer match its run, and reports whether it did. Its
// application would only refuse ("Marshal proposal expired"), so asking the
// Marshal again re-opened a question it could not usefully answer: a stale
// amend kept coming back on every re-delivery and restart. A delivery
// confirmation is bound to the approval scope instead and is left alone.
func (w *Workspace) expiredRunProposal(ctx context.Context, req permission.Request) bool {
	if req.Kind != "marshal-command" || req.RunID == "" || req.ProposalID == "" || req.Object == "/marshal deliver" || w.runtime == nil || w.store == nil {
		return false
	}
	planVersion, revision, ok := parseRunProposalBinding(req.TaskID)
	if !ok {
		return false
	}
	service := w.runtime.Marshal()
	if service == nil {
		return false
	}
	record, err := service.Store.GetMarshalRun(ctx, service.ProjectID, req.RunID)
	if err != nil || (record.Value.PlanVersion == planVersion && record.Revision == revision) {
		return false
	}
	if err := w.store.ResolveMarshalProposalRequest(context.WithoutCancel(ctx), w.projectID, req.Key(), req.ProposalID, "expired"); err != nil {
		return false
	}
	w.permissions.mu.Lock()
	delete(w.permissions.runProposals, req.Key())
	delete(w.permissions.outstanding, req.Key())
	delete(w.permissions.build.redeliver, req.Key())
	delete(w.permissions.build.awaiting, req.Key())
	for id, p := range w.permissions.build.pending {
		if p.request.Key() == req.Key() {
			delete(w.permissions.build.pending, id)
		}
	}
	w.permissions.mu.Unlock()
	return true
}

// parseRunProposalBinding reads the plan version and state revision a run
// proposal was bound to (marshalRunProposalBinding).
func parseRunProposalBinding(binding string) (planVersion, revision int64, ok bool) {
	parts := strings.SplitN(binding, ":", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	planVersion, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	revision, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return planVersion, revision, true
}

// marshalBusyDecisionGrace is how much longer a decision waits each time it
// falls due while the Marshal chat is still working.
const marshalBusyDecisionGrace = time.Minute

// marshalChatWorking reports whether the Marshal chat is in the middle of a
// turn (its pane is not idle at an empty prompt).
func (w *Workspace) marshalChatWorking(ctx context.Context) bool {
	if marshalChatWorkingOverride != nil {
		return marshalChatWorkingOverride()
	}
	pane := w.sayPane()
	if pane == "" {
		return false
	}
	// Only a pane that is really there and shows a turn running counts:
	// one that cannot be read must not hold a decision back.
	status, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", pane, "#{pane_id} #{pane_dead}")
	if err != nil || strings.TrimSpace(string(status)) != pane+" 0" {
		return false
	}
	frame, err := tmux.RunCommand(ctx, "capture-pane", "-p", "-t", pane)
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(frame))
	for _, word := range []string{"esc to interrupt", "esc to cancel", "ctrl+c to interrupt"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// marshalChatWorkingOverride replaces the pane check in tests.
var marshalChatWorkingOverride func() bool
