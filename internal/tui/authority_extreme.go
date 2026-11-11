//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/tmux"
)

const buildMarker = "Marshal — approval authority"

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
	launchID   string
	occurrence string
	request    permission.Request
	deadline   time.Time
	provider   string
	answer     *bool
	reason     string
}

// Queue processing remains asynchronous: stop and proposal polling never wait
// for a model response. The request ID is fresh for each admitted occurrence.
func (w *Workspace) routeBuildPermission(ctx context.Context, req permission.Request) bool {
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
	launch := w.proposalLaunchID()
	w.permissions.mu.Lock()
	for id, p := range w.permissions.build.pending {
		if p.request.Key() != req.Key() {
			continue
		}
		// A request delivered to an earlier Marshal launch can no longer be
		// answered by it; treat it as unanswered so the live Marshal gets it.
		relaunched := p.launchID != "" && launch != "" && p.launchID != launch
		if p.answer == nil && time.Now().Before(p.deadline) && !relaunched {
			w.permissions.mu.Unlock()
			if p.provider == "codex" {
				if !deferPrivateDecisionDelivery(ctx) {
					w.deliverCodexDecision(ctx, id, p)
				}
			}
			w.queuePendingDecision(id, p)
			return true
		}
		delete(w.permissions.build.pending, id)
		if p.answer == nil {
			// This edition never denies work because the Marshal was slow.
			w.permissions.mu.Unlock()
			w.settleUnansweredDecision(ctx, req, id, p, "decision timeout")
			return true
		}
		delete(w.permissions.outstanding, req.Key())
		allow, reason := *p.answer, p.reason
		if p.occurrence != "" {
			original := req
			req.SourceLaunch = p.launchID
			req.SourceOccurrence = p.occurrence
			// Runtime run questions start without model provenance. Once an
			// authenticated decide receipt supplies it, retain the exact run
			// proposal under the resulting permission key. The canonical apply
			// path still checks the live lease, receipt and run revision.
			if allow && original == p.request && original.ProposalID == "" && original.SourceLaunch == "" && original.SourceOccurrence == "" && original.Kind == "marshal-command" && original.RunID != "" {
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
	p := &buildPendingDecision{request: req, provider: provider, paneID: pane, deadline: deadline, launchID: w.proposalLaunchID()}
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
	if pane == nativeChatPane {
		// The native chat takes the same text as one structured user turn.
		err = w.deliverNativeChatTurn(ctx, p.text)
	} else {
		// Bracketed paste keeps the description out of tmux key parsing, and the
		// bounded Enter retries submit it even when Claude Code accepts the paste
		// a moment after it lands (otherwise Enter races ahead and the message
		// sits unsent in the composer). The tmux transport stays a direct call
		// here so the effect inventory still sees it.
		run := func(ctx context.Context, args ...string) ([]byte, error) { return tmux.RunCommand(ctx, args...) }
		err = deliverChatTurn(ctx, run, pane, p.text, deliverEnterTries, deliverEnterWait)
	}
	if err != nil {
		w.permissions.mu.Lock()
		delete(w.permissions.build.pending, id)
		w.permissions.mu.Unlock()
		w.settleUnansweredDecision(ctx, req, id, p, "decision delivery failed")
	} else {
		w.RecordActivity("Marshal decision requested: " + id + " " + req.Kind + " " + req.Object)
		w.permissions.queue.Add(req)
	}
	return true
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
	err := w.decidePermission(ctx, req, allow, "marshal")
	if err == nil && allow && req.Kind == "marshal-command" && strings.HasPrefix(req.Object, "/marshal approve-task ") {
		w.queuePrivateProcess05Resume()
	}
	decision := "denied"
	if allow {
		decision = "allowed"
	}
	text := "Marshal " + decision + ": " + req.Object + " — " + reason
	if err != nil {
		text += " (application failed: " + err.Error() + ")"
	}
	w.RecordActivity(text)

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
// approve-task request names is no longer waiting for a decision.
func (w *Workspace) settledProcess05Approval(req permission.Request) bool {
	if w.runtime == nil || w.runtime.Execution().Engine() == nil {
		return false
	}
	id, ok := strings.CutPrefix(req.Object, "/marshal approve-task ")
	if !ok || id == "" {
		return false
	}
	a, err := w.runtime.Execution().Engine().ApprovalManager().GetApproval(id)
	return err == nil && a.Status != execution.ApprovalRequested
}
