package tui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

const marshalProposalPrefix = "MARSHAL_PROPOSAL "

type marshalProposal struct {
	reviewed                                                  *app.PlanApprovalSnapshot
	occurrence                                                string
	who                                                       string
	sourceSession, revision, digest, instructionRef, criteria string
	action, key, value, provider, path, id, reason, language  string
	taskID, targetWorker, model                               string
	question, choices, card                                   string
	ids, until                                                string
	freshSession                                              bool
}

// Parse a single visible assistant line, never shell source. Reject unknown,
// duplicate, non-string and action-inappropriate fields and trailing data.
func parseMarshalProposal(line string) (marshalProposal, error) {
	p, err := parseMarshalProposalRaw(line)
	if err != nil {
		return p, proposalParseRefusal(err)
	}
	return p, nil
}

func parseMarshalProposalRaw(line string) (marshalProposal, error) {
	var p marshalProposal
	invalid := errors.New("invalid or non-allow-listed Marshal proposal")
	if !strings.HasPrefix(line, marshalProposalPrefix) || len(line) > 4096 {
		return p, invalid
	}
	d := json.NewDecoder(strings.NewReader(strings.TrimPrefix(line, marshalProposalPrefix)))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return p, invalid
	}
	fields := map[string]string{}
	var hasFreshSession bool
	var freshSessionVal bool
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return p, invalid
		}
		key, ok := token.(string)
		if !ok {
			return p, invalid
		}
		if _, exists := fields[key]; exists {
			return p, invalid
		}
		if key == "fresh_session" {
			var b bool
			if d.Decode(&b) != nil {
				return p, invalid
			}
			hasFreshSession = true
			freshSessionVal = b
			fields[key] = strconv.FormatBool(b)
			continue
		}
		var value string
		if d.Decode(&value) != nil || strings.ContainsFunc(value, unicode.IsControl) {
			return p, invalid
		}
		fields[key] = value
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return p, invalid
	}
	if _, err = d.Token(); err != io.EOF {
		return p, invalid
	}
	p = marshalProposal{action: fields["action"], key: fields["key"], value: fields["value"], provider: fields["provider"], path: fields["path"], id: fields["id"], reason: fields["reason"], language: fields["language"], taskID: fields["task_id"], targetWorker: fields["target_worker"], model: fields["model"], freshSession: freshSessionVal}
	p.revision, p.digest, p.instructionRef, p.criteria = fields["revision"], fields["digest"], fields["instruction_ref"], fields["criteria"]
	p.question, p.choices, p.card = fields["question"], fields["choices"], fields["card"]
	p.ids, p.until = fields["ids"], fields["until"]
	var keys []string
	switch p.action {
	case "intake":
		keys = []string{"action", "language"}
		if strings.TrimSpace(p.language) == "" || len(p.language) > 100 {
			return p, invalid
		}
	case "ask":
		keys = []string{"action", "question", "choices"}
		if strings.TrimSpace(p.question) == "" || len(p.question) > 300 {
			return p, invalid
		}
		if _, err := parseAskChoices(p.choices); err != nil {
			return p, invalid
		}
	case "requirements":
		keys = []string{"action", "card"}
		if _, err := parseRequirementsCard(p.card); err != nil {
			// The refusal names the line and the rule, so the Marshal
			// can correct the card instead of guessing why it failed.
			return p, fmt.Errorf("%w: %w", invalid, err)
		}
	case "hold":
		keys = []string{"action", "ids", "reason"}
		// "until":"release" is the owner's own hold (no goal change): it
		// stays until a release, never released by a change answer.
		if _, hasUntil := fields["until"]; hasUntil {
			if p.until != "release" {
				return p, invalid
			}
			keys = append(keys, "until")
		}
		if _, err := parseHoldIDs(p.ids); err != nil {
			return p, invalid
		}
		if err := proposalReasonError(invalid, p.reason); err != nil {
			return p, err
		}
	case "release":
		keys = []string{"action", "ids"}
		if _, err := parseHoldIDs(p.ids); err != nil {
			return p, invalid
		}
	case "setting":
		keys = []string{"action", "key", "value"}
		valid := false
		switch p.key {
		case "acceptance-mode":
			valid = p.value == "marshal" || p.value == "marshal-then-user" || p.value == "user"
		case "execution-rights":
			valid = p.value == "none" || p.value == "read-only" || p.value == "small-tasks"
		case "control":
			valid = p.value == "free" || p.value == "strict"
		case "rework-limit", "ultra-concurrency", "task-tokens", "plan-tokens", "task-money", "plan-money", "task-wall-seconds", "plan-wall-seconds":
			n, e := strconv.ParseInt(p.value, 10, 64)
			valid = e == nil && n >= 0 && strconv.FormatInt(n, 10) == p.value
			if p.key == "ultra-concurrency" {
				valid = valid && n > 0
			}
			if p.key == "ultra-concurrency" || p.key == "rework-limit" {
				_, e = strconv.Atoi(p.value)
				valid = valid && e == nil
			}
		}
		if !valid {
			return p, invalid
		}
	case "continue", "read":
		keys = []string{"action", "path"}
		if p.action == "continue" {
			keys = append(keys, "provider")
			if p.provider != "codex" && p.provider != "claude" {
				return p, invalid
			}
		}
		if !filepath.IsAbs(p.path) || filepath.Clean(p.path) != p.path {
			return p, invalid
		}
	case "memory", "accept", "return":
		keys = []string{"action", "id"}
		if p.id == "" || len(p.id) > 200 || strings.ContainsFunc(p.id, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.'
		}) {
			return p, invalid
		}
	case "approve", "amend-approve", "amend-deny", "close", "resume", "deliver":
		keys = []string{"action"}
	case "amend", "architecture":
		keys = []string{"action", "reason"}
	case "retry":
		keys = []string{"action", "task_id", "fresh_session"}
		if !hasFreshSession {
			return p, invalid
		}
		if p.taskID == "" || len(p.taskID) > 200 || strings.ContainsFunc(p.taskID, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.'
		}) {
			return p, invalid
		}
	case "reassign":
		keys = []string{"action", "task_id", "target_worker"}
		if p.taskID == "" || len(p.taskID) > 200 || strings.ContainsFunc(p.taskID, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.'
		}) {
			return p, invalid
		}
		if p.targetWorker == "" || len(p.targetWorker) > 100 || strings.ContainsFunc(p.targetWorker, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_'
		}) {
			return p, invalid
		}
	case "harness-verify":
		if _, hasModel := fields["model"]; hasModel {
			keys = []string{"action", "provider", "model"}
		} else {
			keys = []string{"action", "provider"}
		}
		if p.provider == "" || len(p.provider) > 100 || strings.ContainsFunc(p.provider, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_'
		}) {
			return p, invalid
		}
		if p.model != "" && (len(p.model) > 200 || strings.ContainsFunc(p.model, unicode.IsControl)) {
			return p, invalid
		}
	default:
		var ok bool
		keys, ok = privateMarshalProposalKeys(p)
		if !ok {
			return p, invalid
		}
	}
	if p.action == "return" {
		keys = append(keys, "reason")
	}
	if p.action == "amend" && (strings.EqualFold(p.reason, "approve") || strings.EqualFold(p.reason, "deny")) {
		return p, invalid
	}
	if p.action == "return" || p.action == "amend" || p.action == "architecture" {
		if err := proposalReasonError(invalid, p.reason); err != nil {
			return p, err
		}
	}
	if len(fields) != len(keys) {
		return p, invalid
	}
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			return p, invalid
		}
	}
	return p, nil
}

// maxProposalReason is the longest reason a proposal may carry, in bytes.
const maxProposalReason = 500

// proposalReasonError says why a proposal reason is refused, so the Marshal
// can correct it: an empty reason, or one over the limit with its length.
func proposalReasonError(invalid error, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: reason is empty", invalid)
	}
	if len(reason) > maxProposalReason {
		return fmt.Errorf("%w: reason is %d bytes; the limit is %d, shorten it and submit again", invalid, len(reason), maxProposalReason)
	}
	return nil
}

func (p marshalProposal) command() string {
	switch p.action {
	case "setting":
		return "/marshal settings " + p.key + " " + p.value
	case "continue":
		return "/continue " + p.provider + " " + p.path
	case "read":
		return "/permission read allow " + p.path
	case "memory":
		return "/memory allow " + p.id
	case "approve", "close", "resume", "deliver":
		return "/marshal " + p.action
	case "amend-approve":
		return "/marshal amend approve"
	case "amend-deny":
		return "/marshal amend deny"
	case "retry":
		if p.freshSession {
			return "/marshal retry " + p.taskID + " fresh-session"
		}
		return "/marshal retry " + p.taskID
	case "accept":
		return "/marshal accept " + p.id
	case "return":
		return "/marshal return " + p.id + " " + p.reason
	case "amend":
		return "/marshal amend " + p.reason
	case "architecture":
		return "/marshal architecture"
	case "approve-task":
		return "/marshal approve-task " + p.id
	case "reassign":
		return "/marshal reassign " + p.taskID + " " + p.targetWorker
	case "harness-verify":
		if p.model != "" {
			return "/harness verify " + p.provider + " " + p.model
		}
		return "/harness verify " + p.provider
	}
	return ""
}

// Called only by the bound Marshal history consumer, not peer/tool/user output.
// History is polled repeatedly; old message occurrences are not replayed; new emissions may be proposed again.
func (w *Workspace) observeMarshalProposals(tr importer.SessionTranscript) {
	w.observeMarshalProposalOccurrence(tr, "")
}
func (w *Workspace) observeMarshalProposalOccurrence(tr importer.SessionTranscript, occurrence string) {
	w.observeMarshalProposalSource(tr, occurrence, "Local request (unverified)")
}

// Only the runtime consumer of the bound live Marshal conversation calls this.
func (w *Workspace) observeAuthenticatedMarshalProposals(tr importer.SessionTranscript) {
	w.tmuxMu.Lock()
	active := w.tmuxActiveWins["marshal-chat"]
	bound := active != nil && active.sessionID != "" && active.sessionID == tr.SessionID
	w.tmuxMu.Unlock()
	if bound {
		w.observeMarshalProposalSource(tr, "", "Marshal")
	}
}

func (w *Workspace) observeMarshalProposalSource(tr importer.SessionTranscript, occurrence, who string) {
	for messageIndex, message := range tr.Messages {
		if message.Role != "assistant" || message.Kind != importer.MessageKindText {
			continue
		}
		inFence := false
		for _, line := range strings.Split(message.Content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				inFence = !inFence
				continue
			}
			if !strings.HasPrefix(trimmed, "MARSHAL_PROPOSAL") {
				continue
			}
			if inFence {
				w.RecordActivity("Ignored Marshal proposal: fenced example is not a proposal.")
				continue
			}
			p, err := parseMarshalProposal(line)
			if err == nil && (p.action == "read" || p.action == "continue") && !w.earlierWorkFolderFound(p.path) {
				w.RecordActivity("History proposal deferred until the operator chooses to continue earlier work.")
				continue
			}
			fingerprint := fmt.Sprintf("%s\x00%d\x00%s\x00%s", tr.SessionID, messageIndex, message.Timestamp, line)
			p.who = who
			if who == "Marshal" && occurrence == "" {
				p.sourceSession = tr.SessionID
			}
			p.occurrence = fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprint)))
			if who == "Marshal" {
				p.occurrence = "runtime:" + p.occurrence
			}
			if occurrence != "" {
				p.occurrence = occurrence
				fingerprint = occurrence
			}
			if w.store != nil && w.projectID != "" {
				pending, admitErr := w.store.AdmitMarshalProposal(context.Background(), w.projectID, p.occurrence, line)
				if admitErr != nil || !pending {
					continue
				}
			}

			w.permissions.mu.Lock()
			if w.permissions.proposalsSeen == nil {
				w.permissions.proposalsSeen = map[string]bool{}
			}
			seen := w.permissions.proposalsSeen[p.occurrence]
			w.permissions.proposalsSeen[p.occurrence] = true
			w.permissions.mu.Unlock()
			if seen {
				continue
			}
			if err != nil {
				if w.store != nil {
					_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, "rejected")
				}
				w.saveProposalRefusalReceipt(context.Background(), p.occurrence, err)
				w.RecordActivity("Ignored Marshal proposal: " + RedactContent(err.Error(), nil))
				if who == "Marshal" {
					w.writeMarshalInboxRefusal("proposal parse", err)
				}
				continue
			}
			w.enqueueValidatedMarshalProposal(p)
		}
	}
}

// A draft may be written before its observer imports it. Keep its proposal
// pending until the runtime has a plan identity and version to display and bind.
// It reports whether an approval of the imported plan was queued.
func (w *Workspace) queueMarshalPlanApproval() bool {
	w.permissions.mu.Lock()
	pending := w.permissions.planApprovalPending
	proposalRunID := w.permissions.planApprovalRunID
	occurrence := w.permissions.planApprovalOccurrence
	w.permissions.mu.Unlock()
	if !pending {
		return false
	}
	m := w.marshalSession()
	m.mu.Lock()
	service, runID, amendment := m.service, m.runID, m.pending
	m.mu.Unlock()
	if service == nil || runID == "" || (proposalRunID != "" && proposalRunID != runID) {
		return false
	}
	p := marshalProposal{action: "approve", occurrence: occurrence}
	if amendment != nil {
		p.action = "amend-approve"
	}
	if err := w.queueMarshalRunProposal(p); err != nil {
		if !errors.Is(err, model.ErrNotFound) {
			w.permissions.mu.Lock()
			if w.permissions.planApprovalOccurrence == occurrence && w.permissions.planApprovalRunID == proposalRunID {
				w.permissions.planApprovalPending = false
			}
			w.permissions.mu.Unlock()
			if w.store != nil && occurrence != "" {
				_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, occurrence, "rejected")
			}
			w.RecordActivity("Ignored Marshal proposal: " + err.Error())
		}
		return false
	}
	w.permissions.mu.Lock()
	if w.permissions.planApprovalOccurrence == occurrence && w.permissions.planApprovalRunID == proposalRunID {
		w.permissions.planApprovalPending = false
	}
	w.permissions.mu.Unlock()
	return true
}

// Bind run actions to the exact stored plan and result state being displayed.
func (w *Workspace) queueMarshalRunProposal(p marshalProposal) error {
	m := w.marshalSession()
	m.mu.Lock()
	service, runID, pending, busy := m.service, m.runID, m.pending, m.busy
	m.mu.Unlock()
	// A change card is open and the owner pressed "apply this change": the
	// Marshal answers with an amend proposal. If the run operation is busy,
	// reserveContext would refuse the amend. Instead of failing, the TUI
	// pauses the run briefly, applies the amendment and resumes (board_change.go).
	// Only in the shipped build, where amend applies without a popup anyway;
	// the governed edition keeps its operator approval path unchanged.
	if editionExtreme && p.action == "amend" && busy && w.currentChange() != nil {
		w.beginChangeApply(p)
		return nil
	}
	if service == nil || runID == "" || (pending != nil && p.action != "amend-approve" && p.action != "amend-deny") || (pending == nil && (p.action == "amend-approve" || p.action == "amend-deny")) {
		return proposalStaleBindingRefusal(errors.New("no matching active plan"))
	}
	record, err := service.Store.GetMarshalRun(context.Background(), service.ProjectID, runID)
	if err != nil {
		return err
	}
	run := record.Value
	if p.action == "approve" || p.action == "amend-approve" {
		if err := refuseUnimportedMarshalDraft(service.Repository); err != nil {
			return proposalStaleBindingRefusal(err)
		}
	}
	if p.action == "approve" && run.State != marshal.Drafting {
		return proposalStaleBindingRefusal(errors.New("plan is not awaiting approval"))
	}
	if p.action == "accept" || p.action == "return" || p.action == "reassign" || p.action == "retry" {
		targetID := p.id
		if p.action == "reassign" || p.action == "retry" {
			targetID = p.taskID
		}
		found := false
		for _, task := range run.Tasks {
			if task.PlanTaskID == targetID {
				found = true
				break
			}
		}
		if !found {
			return proposalStaleBindingRefusal(errors.New("task is not in the active plan"))
		}
	}
	if p.action == "approve-task" {
		if !w.pendingProcess05Approval(context.Background(), run, p.id) {
			return proposalStaleBindingRefusal(errors.New("approval does not belong to the active Marshal plan"))
		}
	}
	if p.action == "approve" || p.action == "amend-approve" {
		snapshot, err := service.PlanApprovalSnapshot(context.Background(), runID)
		if err != nil {
			return err
		}
		if p.action == "approve" && statepath.PublicEngine {
			if marshalRunHasGovernedTask(run) && (snapshot.PlanID != run.PlanID || snapshot.GoalID == "" || snapshot.GoalRevision < 1 || snapshot.ContractDigest == "" || snapshot.CanonicalScopeDigest == "" || snapshot.PlanningMode == "" || snapshot.MarshalBudgetSummary == "" || snapshot.RouteSummary == "" || snapshot.AssignmentSummary == "" || snapshot.TeamSummary == "" || snapshot.VerificationSummary == "" || snapshot.TaskCheckSummary == "") {
				return errors.New("governed Marshal draft lacks a reviewable canonical Process 04 scope; verify its Goal, project context, and worker profiles before approval")
			}
		}
		p.reviewed = &snapshot
	}
	if p.action == "deliver" && p.occurrence == "" {
		// Runtime-generated confirmations use the same durable popup journal as
		// model proposals, including renewed consent after a scoped amendment.
		id, err := model.NewID("marshal-delivery-")
		if err != nil {
			return err
		}
		p.occurrence = id
		if _, err = w.store.AdmitMarshalProposal(context.Background(), w.projectID, id, marshalProposalPrefix+`{"action":"deliver"}`); err != nil {
			return err
		}
	}
	binding := marshalRunProposalBinding(run.PlanVersion, record.Revision, pending)
	var resumeDescription permission.Request
	if !w.describePrivateProcess05Resume(&resumeDescription, p) {
		return proposalStaleBindingRefusal(errors.New("Process 05 continuation gates are not ready"))
	}
	w.permissions.mu.Lock()
	if w.permissions.runProposals == nil {
		w.permissions.runProposals = map[string]marshalProposal{}
	}
	// Scope and Reason are plain sentences for the operator; every technical
	// value (IDs, revisions, digests) goes to Detail, which is never rendered.
	scope, reason := marshalRunProposalPopupText(p, run)
	if p.reviewed != nil {
		scope += " Tasks and checks are on the Plan view (press 7)."
	}
	req := permission.Request{ProposalID: p.occurrence, Kind: "marshal-command", Object: p.command(), Scope: scope, Who: p.requester(), Reason: reason, RunID: runID, TaskID: binding, Detail: fmt.Sprintf("run %s, plan %s version %d, state revision %d", runID, run.PlanID, run.PlanVersion, record.Revision)}
	if p.action == "approve-task" {
		var transport permission.Request
		w.describePrivateTransportApproval(&transport, p.id)
		req.Detail += transport.Scope
		if transport.Reason != "" {
			req.Reason = transport.Reason
		}
		if transport.ApprovalBinding != "" {
			req.ApprovalBinding = transport.ApprovalBinding
		}
	}
	if resumeDescription.ApprovalBinding != "" {
		req.ApprovalBinding = resumeDescription.ApprovalBinding
		req.Detail += resumeDescription.Scope
		req.Reason = resumeDescription.Reason
	}
	if p.action == "accept" {
		purpose, e := service.TaskAcceptance(context.Background(), runID, p.id)
		if e != nil {
			w.permissions.mu.Unlock()
			return e
		}
		req.Detail += "; result " + purpose
	}
	if p.reviewed != nil {
		data, _ := json.Marshal(p.reviewed)
		req.ApprovalBinding = string(data)
		req.Detail += fmt.Sprintf("; pack %s; repository %s; base %s; target %s", p.reviewed.PackDigest, p.reviewed.Repository, p.reviewed.BaseCommit, p.reviewed.TargetRef)
		if p.reviewed.CanonicalScopeDigest != "" {
			// Permission metadata is one line: Render rejects control characters.
			// Keep every reviewed task route and check visible, separated clearly.
			req.Detail += fmt.Sprintf("; Process 04 %s version %d; Goal %s revision %d; project contract %s; approval scope %s; mode %s; Marshal budget %s; task routes: %s; harness assignments: %s; team: %s; verification: %s; executable checks: %s", p.reviewed.PlanID, p.reviewed.PlanVersion, p.reviewed.GoalID, p.reviewed.GoalRevision, p.reviewed.ContractDigest, p.reviewed.CanonicalScopeDigest, p.reviewed.PlanningMode, popupReviewText(p.reviewed.MarshalBudgetSummary), popupReviewText(p.reviewed.RouteSummary), popupReviewText(p.reviewed.AssignmentSummary), popupReviewText(p.reviewed.TeamSummary), popupReviewText(p.reviewed.VerificationSummary), popupReviewText(p.reviewed.TaskCheckSummary))
		}
	}
	if p.action == "deliver" {
		p.value = run.ApprovalScopeDigest
		req.ApprovalBinding = run.ApprovalScopeDigest
		req.Detail += "; target " + run.TargetRef + "; approval " + run.ApprovalScopeDigest
	}
	if pending != nil {
		req.Detail += "; proposed amendment " + binding[strings.LastIndex(binding, ":")+1:]
		if p.action == "amend-deny" {
			req.Reason = "Don't make this change to the plan: " + pending.reason
		} else {
			req.Reason = "Make this change to the plan: " + pending.reason
		}
	}
	if p.action == "accept" || p.action == "return" || p.action == "retry" || p.action == "reassign" {
		for _, task := range run.Tasks {
			if task.PlanTaskID == proposalTarget(p) {
				req.ApprovalBinding = app.TaskCommandDigest(task)
			}
		}
	}
	bound, bindErr := w.bindProposalMCPRequest(req)
	if bindErr != nil {
		w.permissions.mu.Unlock()
		return bindErr
	}
	req = bound
	w.permissions.runProposals[req.Key()] = p
	w.permissions.mu.Unlock()
	w.queuePermission(req)
	return nil
}

func marshalRunHasGovernedTask(run marshal.Run) bool {
	for _, task := range run.Tasks {
		if task.Mode == marshal.Governed {
			return true
		}
	}
	return false
}

// marshalRunProposalPopupText is the plain Scope and Reason the operator sees
// for a run proposal. The amendment Reason is finished by the caller, which has
// the amendment text.
func marshalRunProposalPopupText(p marshalProposal, run marshal.Run) (scope, reason string) {
	switch p.action {
	case "approve":
		return fmt.Sprintf("The plan the Marshal drafted (version %d).", run.PlanVersion), "Please check this plan before it runs."
	case "approve-task":
		return "One task of the current plan.", "Please check this task before it runs."
	case "accept":
		return "One task's finished work.", "Please check this work before it joins the run."
	case "close":
		return "Finishing the current run.", "Please check the result before the run is closed."
	case "resume":
		return "Continuing the current run.", "Please confirm the run should continue."
	case "deliver":
		return "Delivering the finished work to " + run.TargetRef + ".", "After the checks pass, deliver the work automatically."
	case "amend-approve", "amend-deny":
		return "A change to the approved plan.", "Please check this change before it runs."
	default:
		return "The current run.", "Please check this before it runs."
	}
}

func popupReviewText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

func (w *Workspace) applyMarshalProposal(ctx context.Context, req permission.Request) error {
	automatic := isAutomaticProposalContext(ctx) || (!isOwnerActionContext(ctx) && (req.Who == "Marshal" || req.SourceLaunch != ""))
	if automatic {
		ctx = withAutomaticProposalContext(ctx)
	}
	if strings.HasPrefix(req.TaskID, "reserved:") {
		m := w.marshalSession()
		m.mu.Lock()
		service, runID := m.service, m.runID
		m.mu.Unlock()
		if service == nil || runID != req.RunID {
			return w.finishProposalRefusal(ctx, automatic, req.RunID, req.TaskID, "reserved-merge", req.TaskID, req.ProposalID, proposalStaleBindingRefusal(errors.New("reserved merge approval expired")))
		}
		record, err := service.Store.GetMarshalRun(ctx, service.ProjectID, runID)
		if err != nil {
			return err
		}
		matched := false
		for _, task := range record.Value.Tasks {
			if task.State == marshal.Accepted && app.ReservedMergePurpose(task.PlanTaskID, task.ResultCommit, record.Revision) == req.TaskID {
				matched = true
			}
		}
		if !matched {
			return w.finishProposalRefusal(ctx, automatic, runID, req.TaskID, "reserved-merge", req.TaskID, req.ProposalID, proposalStaleBindingRefusal(errors.New("reserved merge approval expired")))
		}
		m.grant(runID, req.TaskID)
		_, err = w.cmd.handleMarshal(ctx, []string{"resume"})
		if err != nil {
			m.mu.Lock()
			delete(m.approvals, runID+"/"+req.TaskID)
			m.mu.Unlock()
			return w.finishProposalRefusal(ctx, automatic, runID, req.TaskID, "reserved-merge", fmt.Sprintf("%d:%d", record.Value.PlanVersion, record.Revision), req.ProposalID, err)
		}
		return nil
	}
	if req.RunID != "" {
		w.permissions.mu.Lock()
		p, ok := w.permissions.runProposals[req.Key()]
		w.permissions.mu.Unlock()
		if !ok || p.command() != req.Object {
			return w.finishProposalRefusal(ctx, automatic, req.RunID, "", "run-proposal", req.TaskID, req.ProposalID, proposalStaleBindingRefusal(errors.New("invalid Marshal run proposal")))
		}
		target := proposalTarget(p)
		m := w.marshalSession()
		m.mu.Lock()
		service, runID, pending := m.service, m.runID, m.pending
		m.mu.Unlock()
		if service == nil || runID != req.RunID || (pending != nil && p.action != "amend-approve" && p.action != "amend-deny") || (pending == nil && (p.action == "amend-approve" || p.action == "amend-deny")) {
			return w.finishProposalRefusal(ctx, automatic, req.RunID, target, p.action, req.TaskID, req.ProposalID, proposalStaleBindingRefusal(errors.New("Marshal proposal expired")))
		}
		binding := req.TaskID
		if p.action == "approve" || p.action == "amend-approve" {
			var snapshot app.PlanApprovalSnapshot
			if req.ApprovalBinding == "" || json.Unmarshal([]byte(req.ApprovalBinding), &snapshot) != nil {
				return w.finishProposalRefusal(ctx, automatic, runID, target, p.action, binding, req.ProposalID, proposalStaleBindingRefusal(errors.New("plan snapshot missing; review and approve again")))
			}
			p.reviewed = &snapshot
		}
		var request marshalRunRequest
		journaledTask := w.runtime != nil && (p.action == "accept" || p.action == "return" || p.action == "retry" || p.action == "reassign")
		if journaledTask {
			if req.ApprovalBinding == "" {
				return proposalStaleBindingRefusal(errors.New("task request binding is missing"))
			}
			request.ID = fmt.Sprintf("proposal-%x", sha256.Sum256([]byte(req.RunID+"\x00"+req.ProposalID+"\x00"+req.Key())))
			request.TaskDigest = req.ApprovalBinding
			if _, err := fmt.Sscanf(req.TaskID, "%d:%d", new(int64), &request.Revision); err != nil {
				return proposalStaleBindingRefusal(err)
			}
			ctx = context.WithValue(ctx, marshalRunRequestKey{}, request)
		}
		record, err := service.Store.GetMarshalRun(ctx, service.ProjectID, runID)
		replay := false
		if journaledTask && err == nil {
			for _, command := range record.Value.Commands {
				if command.ID == request.ID {
					replay = true
				}
			}
		}
		if err != nil || (p.action != "deliver" && !replay && marshalRunProposalBinding(record.Value.PlanVersion, record.Revision, pending) != binding) || (p.action == "deliver" && record.Value.ApprovalScopeDigest != req.ApprovalBinding) {
			return w.finishProposalRefusal(ctx, automatic, runID, target, p.action, binding, req.ProposalID, proposalStaleBindingRefusal(errors.New("Marshal proposal expired")))
		}
		if p.action == "approve" || p.action == "amend-approve" {
			_, err = w.marshalApproveReviewed(ctx, p.reviewed)
			return w.finishProposalRefusal(ctx, automatic, runID, target, p.action, binding, req.ProposalID, err)
		}
		if p.action == "deliver" {
			_, holderID, cancel, err := m.reserveWithHolder(ctx, marshal.OperationHolder{
				RunID: runID,
				Kind:  "deliver",
				Title: "Authorizing automatic delivery",
			})
			if err != nil {
				return w.finishProposalRefusal(ctx, automatic, runID, target, p.action, binding, req.ProposalID, err)
			}
			purpose := "standing-close:" + record.Value.ApprovalScopeDigest
			m.grant(runID, purpose)
			authErr := service.AuthorizeDelivery(ctx, runID, record.Value.ApprovalScopeDigest)
			m.mu.Lock()
			delete(m.approvals, runID+"/"+purpose)
			m.mu.Unlock()
			m.finishHolder(holderID, cancel, false)
			if authErr != nil {
				return w.finishProposalRefusal(ctx, automatic, runID, target, p.action, binding, req.ProposalID, authErr)
			}
			_, err = w.marshalResume(ctx)
			return w.finishProposalRefusal(ctx, automatic, runID, target, p.action, binding, req.ProposalID, err)
		}
		if err := w.validatePrivateProcess05Resume(ctx, req, p); err != nil {
			return w.finishProposalRefusal(ctx, automatic, runID, target, p.action, binding, req.ProposalID, err)
		}
		if journaledTask {
			if service.ActiveExecution(runID) == "" {
				if err := m.busyErrorIfBusy(p.action); err != nil {
					return w.finishProposalRefusal(ctx, automatic, runID, target, p.action, binding, req.ProposalID, err)
				}
			}
			input := app.RunCommandInput{Operation: p.action, TaskID: target, Worker: p.targetWorker, Reason: p.reason, FreshSession: p.freshSession}
			_, err = w.marshalRunCommand(ctx, service, runID, input)
			return w.finishProposalRefusal(ctx, automatic, runID, target, p.action, binding, req.ProposalID, err)
		}
		// Dispatch arguments directly so spaces in free-text reasons are exact.
		_, err = w.cmd.handleMarshal(ctx, marshalProposalArgs(p))
		return w.finishProposalRefusal(ctx, automatic, runID, target, p.action, binding, req.ProposalID, err)
	}
	if req.Who == hostWatchActor {
		// A host-watch temporary cap is applied only once, only while the
		// guard that posted it still stands behind it, and in memory only.
		return w.takeHostCapRequest(req)
	}
	parts := strings.Fields(req.Object)
	if len(parts) >= 3 && parts[0] == "/harness" && parts[1] == "verify" {
		fields := map[string]string{"action": "harness-verify", "provider": parts[2]}
		if len(parts) >= 4 {
			fields["model"] = parts[3]
		}
		data, _ := json.Marshal(fields)
		if _, err := parseMarshalProposal(marshalProposalPrefix + string(data)); err != nil {
			return err
		}
		_, err := w.cmd.Handle(ctx, req.Object)
		return err
	}
	if len(parts) != 4 || parts[0] != "/marshal" || parts[1] != "settings" {
		return proposalParseRefusal(errors.New("invalid Marshal command proposal"))
	}
	data, _ := json.Marshal(map[string]string{"action": "setting", "key": parts[2], "value": parts[3]})
	if _, err := parseMarshalProposal(marshalProposalPrefix + string(data)); err != nil {
		return err
	}
	_, err := w.cmd.Handle(context.WithValue(ctx, settingsActorKey{}, req.Who), req.Object)
	return err
}

func marshalRunProposalBinding(planVersion, revision int64, pending *marshalAmendment) string {
	binding := fmt.Sprintf("%d:%d", planVersion, revision)
	if pending != nil {
		data, _ := json.Marshal([]any{pending.planVersion, pending.reason, pending.draft})
		binding += fmt.Sprintf(":%x", sha256.Sum256(data))
	}
	return binding
}

func marshalProposalArgs(p marshalProposal) []string {
	switch p.action {
	case "amend-approve":
		return []string{"amend", "approve"}
	case "amend-deny":
		return []string{"amend", "deny"}
	case "reassign":
		return []string{"reassign", p.taskID, p.targetWorker}
	case "retry":
		if p.freshSession {
			return []string{"retry", p.taskID, "fresh-session"}
		}
		return []string{"retry", p.taskID}
	case "accept", "approve-task":
		return []string{p.action, p.id}
	case "return":
		return []string{p.action, p.id, p.reason}
	case "amend":
		return []string{p.action, p.reason}
	default:
		return []string{p.action}
	}
}

func (w *Workspace) marshalProposalDecisionNotice(req permission.Request, allow bool, err error) {
	if req.Who != "Marshal" && req.ProposalID == "" && req.Kind != "memory" {
		return
	}
	decision := "declined"
	if allow && err == nil {
		decision = "applied"
	}
	text := fmt.Sprintf("## MARSHAL operator popup decision\n\n%s: %s\n", decision, req.Object)
	if err != nil {
		text = "## MARSHAL operator popup decision\n\nApplication failed; do not continue as approved.\n" + formatInboxRefusalDetails(err)
	}
	if w.runtime == nil {
		return
	}
	view, e := openMarshalInbox(w.runtime.ProjectRoot())
	if e == nil {
		e = view.write(text)
	}
	if e != nil {
		w.RecordActivity("Marshal popup decision delivery failed: " + e.Error())
	}
}

var workspaceInstanceSeq atomic.Uint64

func (w *Workspace) workspaceInstanceID() string {
	w.permissions.mu.Lock()
	defer w.permissions.mu.Unlock()
	if w.permissions.instanceID == "" {
		w.permissions.instanceID = fmt.Sprintf("ws-%d-%d-%d", os.Getpid(), time.Now().UnixNano(), workspaceInstanceSeq.Add(1))
	}
	return w.permissions.instanceID
}

const (
	markerAskAnsweredPrefix  = "card:ask-answered:"
	markerReqConfirmedPrefix = "card:requirements-confirmed:"
	markerCardLatestPrefix   = "card:latest:"
)

func (w *Workspace) recordAskAnswered(digest string) {
	if digest == "" || w.store == nil || w.projectID == "" {
		return
	}
	_ = w.store.RecordMarshalProposalMarker(context.Background(), w.projectID, markerAskAnsweredPrefix+digest, "", "answered")
}

func (w *Workspace) askAnsweredInStore(digest string) bool {
	if digest == "" || w.store == nil || w.projectID == "" {
		return false
	}
	_, ok, err := w.store.MarshalProposalMarker(context.Background(), w.projectID, markerAskAnsweredPrefix+digest)
	return err == nil && ok
}

func (w *Workspace) recordRequirementsConfirmed(texts [8]string) {
	if w.store == nil || w.projectID == "" {
		return
	}
	_ = w.store.RecordMarshalProposalMarker(context.Background(), w.projectID, markerReqConfirmedPrefix+reqTextsDigest(texts), "", "confirmed")
}

func (w *Workspace) requirementsConfirmedInStore(texts [8]string) bool {
	if w.store == nil || w.projectID == "" {
		return false
	}
	_, ok, err := w.store.MarshalProposalMarker(context.Background(), w.projectID, markerReqConfirmedPrefix+reqTextsDigest(texts))
	return err == nil && ok
}

func (w *Workspace) recordProposalCardLatest(kind string) {
	if kind == "" || w.store == nil || w.projectID == "" {
		return
	}
	val := fmt.Sprintf("%s\t%d", w.workspaceInstanceID(), time.Now().UnixNano())
	_ = w.store.RecordMarshalProposalMarker(context.Background(), w.projectID, markerCardLatestPrefix+kind, val, "recorded")
}

// supersededByEarlierSessionCard reports whether a card proposal file from
// before a control-centre restart is older than a card of kind that an earlier
// Workspace instance already applied or confirmed.
func (w *Workspace) supersededByEarlierSessionCard(kind string, modTime time.Time) bool {
	if modTime.IsZero() || w.store == nil || w.projectID == "" {
		return false
	}
	raw, ok, err := w.store.MarshalProposalMarker(context.Background(), w.projectID, markerCardLatestPrefix+kind)
	if err != nil || !ok {
		return false
	}
	inst, nsText, ok := strings.Cut(raw, "\t")
	if !ok || inst == "" || inst == w.workspaceInstanceID() {
		return false
	}
	ns, err := strconv.ParseInt(nsText, 10, 64)
	if err != nil {
		return false
	}
	return modTime.UnixNano() < ns
}

// proposalCardAlreadyHandled reports whether an ask or requirements proposal
// was already answered, confirmed, resolved through another channel, or
// superseded before a restart.
func (w *Workspace) proposalCardAlreadyHandled(p marshalProposal, line string, modTime time.Time) bool {
	if p.action != "ask" && p.action != "requirements" {
		return false
	}
	if w.store != nil && w.projectID != "" {
		if resolved, err := w.store.HasResolvedMarshalProposalLine(context.Background(), w.projectID, line); err == nil && resolved {
			return true
		}
	}
	switch p.action {
	case "ask":
		if choices, err := parseAskChoices(p.choices); err == nil {
			if w.askAnsweredInStore(askCardDigest(strings.TrimSpace(p.question), choices)) {
				return true
			}
		}
		if w.supersededByEarlierSessionCard("ask", modTime) || w.supersededByEarlierSessionCard("requirements", modTime) {
			return true
		}
	case "requirements":
		if lines, err := parseRequirementsCard(p.card); err == nil {
			if w.requirementsConfirmedInStore(reqTexts(lines)) {
				return true
			}
		}
		if w.supersededByEarlierSessionCard("requirements", modTime) {
			return true
		}
	}
	return false
}

type pendingProposalFile struct {
	name      string
	modTime   time.Time
	sessionID string
	id        string
	who       string
	line      string
	proposal  marshalProposal
}

// Files are suggestions, never authority. Only the live Marshal monitor consumes
// them; parsing and the ordinary operator popup remain mandatory.
func (w *Workspace) observeMarshalProposalFiles(root string) {
	w.monitorGoverningIntegrity()
	w.recoverMarshalProposals()
	project, err := os.OpenRoot(root)
	if err != nil {
		return
	}
	defer project.Close()
	info, err := project.Lstat(statepath.DirName + "/proposals")
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	fs, err := project.OpenRoot(statepath.DirName + "/proposals")
	if err != nil {
		return
	}
	defer fs.Close()
	directory, err := fs.Open(".")
	if err != nil {
		return
	}
	opened, err := directory.Stat()
	if err != nil || !os.SameFile(info, opened) {
		directory.Close()
		return
	}
	entries, err := directory.ReadDir(-1)
	directory.Close()
	if err != nil {
		return
	}

	var files []pendingProposalFile
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || !entry.Type().IsRegular() {
			continue
		}
		info, err := fs.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
			continue
		}
		file, err := openProposalFile(fs, name)
		if err != nil {
			continue
		}
		current, err := file.Stat()
		if err != nil || !current.Mode().IsRegular() || !os.SameFile(info, current) {
			file.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(file, 4097))
		file.Close()
		if err != nil || len(data) > 4096 {
			continue
		}
		if w.consumeBuildDecision(data) {
			_ = fs.Rename(name, name+".consumed")
			continue
		}
		line := marshalProposalPrefix + strings.TrimSpace(string(data))
		proposal, parseErr := parseMarshalProposal(line)
		if parseErr != nil {
			// A complete proposal that is refused says why, once: the file
			// is set aside with the cause beside it, so it is not read again
			// and the Marshal can correct it. An incomplete write stays.
			if json.Valid(data) {
				w.rejectMarshalProposalFile(fs, name, parseErr)
			}
			continue
		}
		sessionID := fmt.Sprintf("proposal-file:%s:%d:%x", name, info.ModTime().UnixNano(), sha256.Sum256(data))
		fingerprint := fmt.Sprintf("%s\x00%d\x00%s\x00%s", sessionID, 0, "", line)
		id := fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprint)))
		who := "Local request (unverified)"
		if proposal.action == "architecture" && root == w.providerRoot() {
			if bound := w.boundMarshalChatSession(); bound != "" {
				id = fmt.Sprintf("runtime:%x", sha256.Sum256([]byte(bound+"\x00"+fingerprint)))
				who = "Marshal"
			}
		}
		if w.store == nil {
			continue
		}
		files = append(files, pendingProposalFile{
			name:      name,
			modTime:   info.ModTime(),
			sessionID: sessionID,
			id:        id,
			who:       who,
			line:      line,
			proposal:  proposal,
		})
	}

	// Process proposal files in chronological order rather than filename order
	// so an edited requirements card is never overwritten by an older version,
	// and skip cards already superseded within the batch or before a restart.
	sort.Slice(files, func(i, j int) bool {
		if !files[i].modTime.Equal(files[j].modTime) {
			return files[i].modTime.Before(files[j].modTime)
		}
		return files[i].name < files[j].name
	})

	lastAsk, lastReq, lastIntake := -1, -1, -1
	for i, f := range files {
		switch f.proposal.action {
		case "ask":
			lastAsk = i
		case "requirements":
			lastReq = i
		case "intake":
			lastIntake = i
		}
	}

	for i, f := range files {
		pending, admitErr := w.store.AdmitMarshalProposal(context.Background(), w.projectID, f.id, f.line)
		if admitErr != nil {
			continue
		}
		// Durable admission precedes source consumption. A crash on either
		// side of the rename replays the same occurrence, not a new request.
		if err := fs.Rename(f.name, f.name+".consumed"); err != nil {
			continue
		}
		if !pending {
			continue
		}
		superseded := (f.proposal.action == "ask" && (i < lastAsk || i < lastReq)) ||
			(f.proposal.action == "requirements" && i < lastReq) ||
			(f.proposal.action == "intake" && i < lastIntake)
		if superseded || w.proposalCardAlreadyHandled(f.proposal, f.line, f.modTime) {
			_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, f.id, "superseded")
			continue
		}
		w.observeMarshalProposalSource(importer.SessionTranscript{SessionID: f.sessionID, Messages: []importer.Message{{Role: "assistant", Content: f.line}}}, f.id, f.who)
	}
}

// rejectMarshalProposalFile sets a refused proposal file aside as
// <name>.rejected, writes the refusal to <name>.error and records it.
func (w *Workspace) rejectMarshalProposalFile(fs *os.Root, name string, cause error) {
	if fs.Rename(name, name+".rejected") != nil {
		return
	}
	content := RedactContent(cause.Error(), nil) + "\n"
	if receipt, ok := marshal.RefusalOf(cause); ok {
		sanitized := receipt.Sanitized()
		sanitized.Message = RedactContent(sanitized.Message, nil)
		if encoded, err := json.Marshal(sanitized); err == nil {
			content += "receipt: " + string(encoded) + "\n"
		}
	}
	_ = fs.WriteFile(name+".error", []byte(content), 0o600)
	w.RecordActivity("Marshal proposal " + name + " refused: " + RedactContent(cause.Error(), nil))
}

// Recovery queues the original bound request. Reconstructing a fresh binding
// would authorize a different plan/result than the one originally displayed.
func (w *Workspace) recoverMarshalProposals() {
	if w.store == nil || w.projectID == "" {
		return
	}
	pending, err := w.store.PendingMarshalProposals(context.Background(), w.projectID)
	if err != nil {
		return
	}
	// Pre-scan unbound pending records (ordered by rowid) so superseded ask,
	// requirements, or intake admissions from before a restart are resolved
	// instead of replayed.
	lastAsk, lastReq, lastIntake := -1, -1, -1
	parsedUnbound := make([]marshalProposal, len(pending))
	hasParsed := make([]bool, len(pending))
	for i, record := range pending {
		if record.RequestJSON != "" || w.staleProposalMCP(record.ID) {
			continue
		}
		if p, err := parseMarshalProposal(record.Line); err == nil {
			parsedUnbound[i] = p
			hasParsed[i] = true
			switch p.action {
			case "ask":
				lastAsk = i
			case "requirements":
				lastReq = i
			case "intake":
				lastIntake = i
			}
		}
	}
	for i, record := range pending {
		if w.staleProposalMCP(record.ID) {
			continue
		}
		if w.recoverBoundMCPMemory(record) {
			continue
		}
		if record.RequestJSON == "" {
			if hasParsed[i] {
				p := parsedUnbound[i]
				superseded := (p.action == "ask" && (i < lastAsk || i < lastReq)) ||
					(p.action == "requirements" && i < lastReq) ||
					(p.action == "intake" && i < lastIntake)
				if superseded || w.proposalCardAlreadyHandled(p, record.Line, time.Time{}) {
					_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, record.ID, "superseded")
					continue
				}
			}
			// Admissions interrupted before binding still need parsing and a popup.
			w.observePendingMarshalProposal(record.ID, record.Line)
			continue
		}
		var req permission.Request
		if json.Unmarshal([]byte(record.RequestJSON), &req) != nil {
			continue
		}
		if !strings.HasPrefix(record.ID, "runtime:") {
			req.Who = "Local request (unverified)"
		}
		if req.RunID != "" {
			p, err := parseMarshalProposal(record.Line)
			if err != nil {
				continue
			}
			p.occurrence = record.ID
			w.permissions.mu.Lock()
			if w.permissions.runProposals == nil {
				w.permissions.runProposals = map[string]marshalProposal{}
			}
			w.permissions.runProposals[req.Key()] = p
			w.permissions.mu.Unlock()
		}
		w.queuePermission(req)
	}
}

func (w *Workspace) observePendingMarshalProposal(id, line string) {
	// Supply the persisted occurrence ID while reusing the normal validator.
	who := "Local request (unverified)"
	if strings.HasPrefix(id, "runtime:") {
		who = "Marshal"
	}
	w.observeMarshalProposalSource(importer.SessionTranscript{SessionID: "recovered:" + id, Messages: []importer.Message{{Role: "assistant", Content: line}}}, id, who)
}

func (p marshalProposal) requester() string {
	if p.who != "" {
		return p.who
	}
	if strings.HasPrefix(p.occurrence, "runtime:") {
		return "Marshal"
	}
	return "Local request (unverified)"
}

func (w *Workspace) queueReservedMergeApproval(_ context.Context, runID, taskID string, revision int64, result string, files []string) {
	w.queuePermission(permission.Request{Kind: "marshal-command", Object: "Merge changes to Marshal's own rule files: " + strings.Join(files, ", "), RunID: runID, TaskID: app.ReservedMergePurpose(taskID, result, revision), Who: "MARSHAL runtime", Scope: "One task's finished work.", Reason: "These files control how Marshal works. A merges them and continues; D keeps them out.", Detail: fmt.Sprintf("run %s task %s revision %d result %s", runID, taskID, revision, result)})
}

func (w *Workspace) monitorGoverningIntegrity() {
	m := w.marshalSession()
	m.mu.Lock()
	service, runID, alerted := m.service, m.runID, m.integrityAlertRunID
	m.mu.Unlock()
	if service == nil || runID == "" || alerted == runID {
		return
	}
	if err := service.CheckGoverningIntegrity(context.Background(), runID); err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return
		}
		m.mu.Lock()
		m.integrityAlertRunID = runID
		m.mu.Unlock()
		w.RecordActivity("Governance integrity alert: " + err.Error())
	}
}

// applyMarshalProposal reuses the validator-bound action pipeline for authenticated sources.
func (w *Workspace) enqueueValidatedMarshalProposal(p marshalProposal) {
	switch p.action {
	case "architecture":
		var openErr error
		if p.who != "Marshal" || !strings.HasPrefix(p.occurrence, "runtime:") {
			openErr = proposalAuthRefusal(errors.New("architecture auto-open requires the bound live Marshal source; use /marshal architecture"), "/marshal architecture")
		} else if w.currentReq() != nil {
			// Wait until the owner confirms the open requirements card so we
			// do not drop this request with "goal contract not found".
			w.holdArchitectureRequest([]string{p.occurrence}, p.reason)
		} else {
			_, openErr = w.marshalArchitectureProposal(w.workers.context(), p.occurrence, p.reason)
		}
		if openErr != nil {
			w.saveProposalRefusalReceipt(context.Background(), p.occurrence, openErr)
			w.resolveArchitectureProposal(p.occurrence, openErr)
			w.RecordActivity("Ignored Marshal architecture proposal: " + RedactContent(openErr.Error(), nil))
		}
	case "ask":
		w.openAsk(p)
	case "requirements":
		w.openRequirements(p)
	case "hold":
		w.applyHold(p)
	case "release":
		w.applyRelease(p)
	case "intake":
		if err := w.saveMarshalIntake(marshalIntake{Language: p.language}); err != nil {
			w.permissions.mu.Lock()
			delete(w.permissions.proposalsSeen, p.occurrence)
			w.permissions.mu.Unlock()
			return
		}
		w.RecordActivity(fmt.Sprintf("%s: intake updated (language %s)", p.requester(), p.language))
		if w.store != nil {
			_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, "applied")
		}
	case "continue":
		w.queuePermission(permission.Request{ProposalID: p.occurrence, Kind: "read", Object: p.path, Scope: "this session only, read-only", Who: p.requester(), Reason: p.command(), ContinuationProvider: p.provider})
	case "read":
		w.queuePermission(permission.Request{ProposalID: p.occurrence, Kind: "read", Object: p.path, Scope: "this session only, read-only", Who: p.requester(), Reason: p.command()})
	case "memory":
		found := false
		if w.runtime != nil {
			for _, rec := range w.runtime.ContinuationCandidates() {
				if rec.ID == p.id {
					req := memoryPermission(rec)
					req.ProposalID = p.occurrence
					req.Who = p.requester()
					w.queuePermission(req)
					found = true
					break
				}
			}
		}
		if !found {
			w.RecordActivity("Ignored Marshal memory proposal: candidate does not exist.")
		}
	case "harness-verify":
		w.queuePermission(permission.Request{ProposalID: p.occurrence, Kind: "marshal-command", Object: p.command(), Scope: "This project.", Who: p.requester(), Reason: "Check this provider's setup."})
	case "setting":
		w.queuePermission(permission.Request{ProposalID: p.occurrence, Kind: "marshal-command", Object: p.command(), Scope: "This project, from the next run.", Who: p.requester(), Reason: "Change this setting as proposed."})
	case "approve":
		m := w.marshalSession()
		m.mu.Lock()
		proposalRunID := m.runID
		m.mu.Unlock()
		w.permissions.mu.Lock()
		w.permissions.planApprovalPending = true
		w.permissions.planApprovalRunID = proposalRunID
		w.permissions.planApprovalOccurrence = p.occurrence
		w.permissions.mu.Unlock()
		w.queueMarshalPlanApproval()
	case "goal":
		w.applyMarshalGoalProposal(p)
	case "accept", "return", "amend", "amend-approve", "amend-deny", "close", "resume", "deliver", "approve-task", "reassign", "retry":
		if err := w.queueMarshalRunProposal(p); err != nil {
			automatic := p.who == "Marshal" || strings.HasPrefix(p.occurrence, "mcp:") || strings.HasPrefix(p.occurrence, "runtime:")
			runID, binding := w.currentRunBinding(context.Background())
			finalErr := w.finishProposalRefusal(context.Background(), automatic, runID, proposalTarget(p), p.action, binding, p.occurrence, err)
			if w.store != nil && p.occurrence != "" {
				_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, "rejected")
			}
			if !marshal.IsReplayedEscalation(finalErr) {
				w.RecordActivity("Ignored Marshal proposal: " + RedactContent(finalErr.Error(), nil))
			}
			if p.who == "Marshal" {
				w.writeMarshalInboxRefusal(p.command(), finalErr)
			}
		}
	}
}

type automaticProposalCtxKey struct{}
type ownerActionCtxKey struct{}

func withAutomaticProposalContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, automaticProposalCtxKey{}, true)
}

func isAutomaticProposalContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(automaticProposalCtxKey{}).(bool)
	return v
}

func withOwnerActionContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, ownerActionCtxKey{}, true)
}

func isOwnerActionContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(ownerActionCtxKey{}).(bool)
	return v
}

func proposalParseRefusal(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := marshal.RefusalOf(err); ok {
		return err
	}
	return marshal.NewRefusalError(marshal.RefusalReceipt{
		Code:         marshal.RefusalCodeParseInvalid,
		Effects:      marshal.EffectsNone,
		NextCommands: []string{"/marshal help"},
		Message:      err.Error(),
	}, err)
}

func proposalAuthRefusal(err error, next ...string) error {
	if err == nil {
		return nil
	}
	if _, ok := marshal.RefusalOf(err); ok {
		return err
	}
	if len(next) == 0 {
		next = []string{"/marshal status"}
	}
	return marshal.NewRefusalError(marshal.RefusalReceipt{
		Code:         marshal.RefusalCodeUnauthorized,
		Effects:      marshal.EffectsNone,
		NextCommands: next,
		Message:      err.Error(),
	}, err)
}

func proposalStaleBindingRefusal(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := marshal.RefusalOf(err); ok {
		return err
	}
	return marshal.NewRefusalError(marshal.RefusalReceipt{
		Code:         marshal.RefusalCodeStaleBinding,
		Effects:      marshal.EffectsNone,
		NextCommands: []string{"/marshal status"},
		Message:      err.Error(),
	}, err)
}

func proposalTarget(p marshalProposal) string {
	switch p.action {
	case "reassign", "retry":
		return p.taskID
	case "accept", "return", "approve-task", "memory":
		return p.id
	default:
		return ""
	}
}

func (w *Workspace) currentRunBinding(ctx context.Context) (string, string) {
	m := w.marshalSession()
	m.mu.Lock()
	service, runID, pending := m.service, m.runID, m.pending
	m.mu.Unlock()
	if service == nil || runID == "" {
		return runID, "0:0"
	}
	record, err := service.Store.GetMarshalRun(ctx, service.ProjectID, runID)
	if err != nil {
		return runID, "0:0"
	}
	return runID, marshalRunProposalBinding(record.Value.PlanVersion, record.Revision, pending)
}

const (
	automaticRefusalEscalationThreshold = 3
	maxRefusalSuppressionEntries        = 256
	maxRefusalReceiptEntries            = 256
	markerRefusalSuppressionPrefix      = "refusal:suppression:"
	markerRefusalSuppressionIndex       = "refusal:suppression:index"
	markerRefusalReceiptPrefix          = "refusal:receipt:"
	markerRefusalReceiptIndex           = "refusal:receipt:index"
)

type refusalResetScope struct {
	RunID  string
	Target string
}

type refusalSuppressionKey struct {
	RunID           string `json:"runId"`
	Target          string `json:"target"`
	Op              string `json:"op"`
	Blocker         string `json:"blocker"`
	BindingRevision string `json:"bindingRevision"`
}

type refusalSuppressionRecord struct {
	Key               refusalSuppressionKey   `json:"key"`
	Count             int                     `json:"count"`
	Escalated         bool                    `json:"escalated"`
	EscalationID      string                  `json:"escalationId,omitempty"`
	ChatNotified      bool                    `json:"chatNotified,omitempty"`
	EscalationReceipt *marshal.RefusalReceipt `json:"escalationReceipt,omitempty"`
	UpdatedAt         time.Time               `json:"updatedAt"`
}

type refusalSuppressionIndexEntry struct {
	MarkerID string `json:"markerId"`
	RunID    string `json:"runId"`
	Target   string `json:"target"`
}

func suppressionMarkerID(key refusalSuppressionKey) string {
	raw, _ := json.Marshal(key)
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%s%x", markerRefusalSuppressionPrefix, sum)
}

func blockerSuppressionIdentity(receipt marshal.RefusalReceipt) string {
	if receipt.Blocker != nil {
		if receipt.Code == marshal.RefusalCodeInterruptedOp && receipt.Blocker.ID != "" {
			return receipt.Code + ":" + receipt.Blocker.Kind + ":" + receipt.Blocker.ID
		}
		return receipt.Code + ":" + receipt.Blocker.Kind + ":" + receipt.Blocker.RunID + ":" + receipt.Blocker.TaskID
	}
	return receipt.Code
}

func (w *Workspace) finishProposalRefusal(ctx context.Context, automatic bool, runID, target, op, bindingRevision, occurrenceID string, err error) error {
	if err == nil {
		return nil
	}
	finalErr := err
	if automatic {
		finalErr = w.recordAutomaticProposalRefusal(ctx, runID, target, op, bindingRevision, err)
	}
	if occurrenceID != "" {
		w.saveProposalRefusalReceipt(ctx, occurrenceID, finalErr)
	}
	return finalErr
}

func (w *Workspace) recordAutomaticProposalRefusal(ctx context.Context, runID, target, op, bindingRevision string, err error) error {
	receipt, ok := marshal.RefusalOf(err)
	if !ok {
		receipt = marshal.RefusalReceipt{
			Code:         marshal.RefusalCodePreconditionFailed,
			Effects:      marshal.EffectsUnknown,
			NextCommands: []string{"/marshal status"},
			Message:      RedactContent(err.Error(), nil),
		}
	}
	if bindingRevision == "" {
		_, bindingRevision = w.currentRunBinding(ctx)
	}
	key := refusalSuppressionKey{
		RunID:           runID,
		Target:          target,
		Op:              op,
		Blocker:         blockerSuppressionIdentity(receipt),
		BindingRevision: bindingRevision,
	}
	markerID := suppressionMarkerID(key)
	m := w.marshalSession()
	m.refusalMu.Lock()
	rec, found, loadErr := w.loadRefusalSuppressionLocked(ctx, m, markerID)
	if !found {
		rec = refusalSuppressionRecord{Key: key}
	}
	if rec.Escalated && rec.EscalationReceipt != nil {
		replayed := rec.EscalationReceipt.Clone()
		m.refusalMu.Unlock()
		if loadErr != nil {
			w.RecordActivity("Marshal refusal journal read failed: " + loadErr.Error())
		}
		return &marshal.RefusalError{
			Receipt:  replayed,
			Cause:    err,
			Replayed: true,
		}
	}
	now := time.Now().UTC()
	if resetAt, reset := refusalResetTimeLocked(m, key.RunID, key.Target); reset && !now.After(resetAt) {
		now = resetAt.Add(time.Nanosecond)
	}
	rec.Count++
	rec.UpdatedAt = now
	var (
		outErr       error
		saveErr      error
		escalatedNow bool
		escReceipt   marshal.RefusalReceipt
	)
	if rec.Count < automaticRefusalEscalationThreshold {
		attemptReceipt := receipt.Clone()
		attemptReceipt.Attempt = rec.Count
		attemptReceipt.EscalationID = ""
		saveErr = w.saveRefusalSuppressionLocked(ctx, m, markerID, rec, attemptReceipt, "refused")
		outErr = marshal.NewRefusalError(attemptReceipt, err)
	} else {
		escID, idErr := model.NewID("esc-")
		if idErr != nil || escID == "" {
			escID = fmt.Sprintf("esc-%x", sha256.Sum256([]byte(markerID)))[:16]
		}
		escReceipt = receipt.Clone()
		escReceipt.Code = marshal.RefusalCodeRepeatedFailure
		escReceipt.Attempt = automaticRefusalEscalationThreshold
		escReceipt.EscalationID = escID
		escReceipt.Message = fmt.Sprintf("automatic retries stopped after %d equivalent refusals; change the condition before retrying (%s)", automaticRefusalEscalationThreshold, marshal.SanitizeRefusalText(receipt.Message))
		rec.Escalated = true
		rec.EscalationID = escID
		rec.ChatNotified = true
		savedReceipt := escReceipt.Clone()
		rec.EscalationReceipt = &savedReceipt
		saveErr = w.saveRefusalSuppressionLocked(ctx, m, markerID, rec, escReceipt, "escalated")
		outErr = marshal.NewRefusalError(escReceipt, err)
		escalatedNow = true
	}
	m.refusalMu.Unlock()
	if loadErr != nil {
		w.RecordActivity("Marshal refusal journal read failed: " + loadErr.Error())
	}
	if saveErr != nil {
		w.RecordActivity("Marshal refusal journal write failed: " + saveErr.Error())
	}
	if escalatedNow {
		w.tellMarshal(automaticRefusalEscalationChatNotice(key, escReceipt))
		w.RecordActivity("Escalated repeated Marshal refusal (" + escReceipt.EscalationID + "): " + RedactContent(escReceipt.Summary(), nil))
	}
	return outErr
}

func recordRefusalResetLocked(m *marshalSession, runID, target string, at time.Time) {
	if m.refusalResetAt == nil {
		m.refusalResetAt = map[refusalResetScope]time.Time{}
	}
	scope := refusalResetScope{RunID: runID, Target: target}
	if prev, exists := m.refusalResetAt[scope]; exists {
		if prev.After(at) {
			at = prev
		}
	} else {
		m.refusalResetOrder = append(m.refusalResetOrder, scope)
	}
	m.refusalResetAt[scope] = at
	for len(m.refusalResetOrder) > maxRefusalSuppressionEntries {
		oldest := m.refusalResetOrder[0]
		m.refusalResetOrder = m.refusalResetOrder[1:]
		delete(m.refusalResetAt, oldest)
	}
}

func refusalResetTimeLocked(m *marshalSession, runID, target string) (time.Time, bool) {
	if len(m.refusalResetAt) == 0 || runID == "" {
		return time.Time{}, false
	}
	var latest time.Time
	var found bool
	if t, ok := m.refusalResetAt[refusalResetScope{RunID: runID, Target: ""}]; ok {
		latest = t
		found = true
	}
	if target != "" {
		if t, ok := m.refusalResetAt[refusalResetScope{RunID: runID, Target: target}]; ok {
			if !found || t.After(latest) {
				latest = t
				found = true
			}
		}
		return latest, found
	}
	for scope, t := range m.refusalResetAt {
		if scope.RunID != runID {
			continue
		}
		if !found || t.After(latest) {
			latest = t
			found = true
		}
	}
	return latest, found
}

func (w *Workspace) loadRefusalSuppressionLocked(ctx context.Context, m *marshalSession, markerID string) (refusalSuppressionRecord, bool, error) {
	if m.refusals != nil {
		if rec, ok := m.refusals[markerID]; ok {
			return rec, true, nil
		}
	}
	if w.store == nil || w.projectID == "" {
		return refusalSuppressionRecord{}, false, nil
	}
	raw, ok, err := w.store.MarshalProposalMarker(ctx, w.projectID, markerID)
	if err != nil {
		return refusalSuppressionRecord{}, false, err
	}
	if !ok || raw == "" {
		return refusalSuppressionRecord{}, false, nil
	}
	var rec refusalSuppressionRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return refusalSuppressionRecord{}, false, err
	}
	if resetAt, reset := refusalResetTimeLocked(m, rec.Key.RunID, rec.Key.Target); reset && !rec.UpdatedAt.After(resetAt) {
		return refusalSuppressionRecord{}, false, nil
	}
	w.cacheRefusalSuppressionLocked(m, markerID, rec)
	return rec, true, nil
}

func (w *Workspace) cacheRefusalSuppressionLocked(m *marshalSession, markerID string, rec refusalSuppressionRecord) {
	if m.refusals == nil {
		m.refusals = map[string]refusalSuppressionRecord{}
	}
	if _, exists := m.refusals[markerID]; !exists {
		m.refusalOrder = append(m.refusalOrder, markerID)
	}
	m.refusals[markerID] = rec
	for len(m.refusalOrder) > maxRefusalSuppressionEntries {
		oldest := m.refusalOrder[0]
		m.refusalOrder = m.refusalOrder[1:]
		delete(m.refusals, oldest)
	}
}

func (w *Workspace) saveRefusalSuppressionLocked(ctx context.Context, m *marshalSession, markerID string, rec refusalSuppressionRecord, receipt marshal.RefusalReceipt, outcome string) error {
	w.cacheRefusalSuppressionLocked(m, markerID, rec)
	if w.store == nil || w.projectID == "" {
		return nil
	}
	encoded, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	auditData := map[string]any{
		"run_id":           marshal.SanitizeRefusalText(rec.Key.RunID),
		"target":           marshal.SanitizeRefusalText(rec.Key.Target),
		"op":               marshal.SanitizeRefusalText(rec.Key.Op),
		"blocker":          marshal.SanitizeRefusalText(rec.Key.Blocker),
		"binding_revision": marshal.SanitizeRefusalText(rec.Key.BindingRevision),
		"code":             marshal.SanitizeRefusalText(receipt.Code),
		"effects":          string(receipt.Effects),
		"attempt":          receipt.Attempt,
		"escalated":        rec.Escalated,
	}
	if rec.EscalationID != "" {
		auditData["escalation_id"] = rec.EscalationID
	}
	indexErr := w.updateRefusalSuppressionIndexLocked(ctx, markerID, rec.Key.RunID, rec.Key.Target)
	journalErr := w.store.RecordMarshalRefusalJournal(ctx, w.projectID, markerID, string(encoded), outcome, auditData)
	return errors.Join(indexErr, journalErr)
}

func (w *Workspace) updateRefusalSuppressionIndexLocked(ctx context.Context, markerID, runID, target string) error {
	if w.store == nil || w.projectID == "" {
		return nil
	}
	var entries []refusalSuppressionIndexEntry
	raw, ok, err := w.store.MarshalProposalMarker(ctx, w.projectID, markerRefusalSuppressionIndex)
	if err != nil {
		return err
	}
	if ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			return err
		}
	}
	found := false
	changed := false
	for i := range entries {
		if entries[i].MarkerID == markerID {
			if entries[i].RunID != runID || entries[i].Target != target {
				entries[i].RunID = runID
				entries[i].Target = target
				changed = true
			}
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, refusalSuppressionIndexEntry{MarkerID: markerID, RunID: runID, Target: target})
		changed = true
	}
	var errs []error
	if len(entries) > maxRefusalSuppressionEntries {
		excess := len(entries) - maxRefusalSuppressionEntries
		var keptFailed []refusalSuppressionIndexEntry
		for _, evict := range entries[:excess] {
			if err := w.store.DeleteMarshalProposalMarker(ctx, w.projectID, evict.MarkerID); err != nil {
				errs = append(errs, err)
				keptFailed = append(keptFailed, evict)
				continue
			}
			changed = true
		}
		entries = append(keptFailed, entries[excess:]...)
	}
	if changed {
		encoded, err := json.Marshal(entries)
		if err != nil {
			errs = append(errs, err)
			return errors.Join(errs...)
		}
		if err := w.store.RecordMarshalProposalMarker(ctx, w.projectID, markerRefusalSuppressionIndex, string(encoded), "recorded"); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// resetAutomaticRefusalSuppression clears automatic suppression counters for
// (runID, target) when the owner takes an explicit action, starting a fresh
// evaluation while leaving permanent holds and service policies intact.
func (w *Workspace) resetAutomaticRefusalSuppression(runID, target string) error {
	if runID == "" {
		return nil
	}
	m := w.marshalSession()
	m.refusalMu.Lock()
	matches := func(rID, tgt string) bool {
		if rID != runID {
			return false
		}
		return target == "" || tgt == "" || tgt == target
	}
	resetAt := time.Now().UTC()
	var (
		deleteOrder []string
		deleteSet   = map[string]struct{}{}
	)
	addDelete := func(id string) {
		if id == "" {
			return
		}
		if _, ok := deleteSet[id]; !ok {
			deleteSet[id] = struct{}{}
			deleteOrder = append(deleteOrder, id)
		}
	}
	if len(m.refusals) > 0 {
		kept := m.refusalOrder[:0]
		for _, id := range m.refusalOrder {
			if rec, ok := m.refusals[id]; ok && matches(rec.Key.RunID, rec.Key.Target) {
				if rec.UpdatedAt.After(resetAt) {
					resetAt = rec.UpdatedAt
				}
				addDelete(id)
				delete(m.refusals, id)
				continue
			}
			kept = append(kept, id)
		}
		m.refusalOrder = kept
	}
	recordRefusalResetLocked(m, runID, target, resetAt)

	var resetErr error
	if w.store != nil && w.projectID != "" {
		resetErr = w.deleteRefusalSuppressionMarkersLocked(context.Background(), matches, deleteOrder, deleteSet)
	}
	m.refusalMu.Unlock()
	if resetErr != nil {
		w.RecordActivity("Marshal refusal reset persistence failed: " + resetErr.Error())
	}
	return resetErr
}

func (w *Workspace) deleteRefusalSuppressionMarkersLocked(ctx context.Context, matches func(runID, target string) bool, deleteOrder []string, deleteSet map[string]struct{}) error {
	var errs []error
	var entries []refusalSuppressionIndexEntry
	indexLoaded := false
	raw, ok, err := w.store.MarshalProposalMarker(ctx, w.projectID, markerRefusalSuppressionIndex)
	if err != nil {
		errs = append(errs, err)
	} else if ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			errs = append(errs, err)
		} else {
			indexLoaded = true
			for _, entry := range entries {
				if matches(entry.RunID, entry.Target) {
					if _, exists := deleteSet[entry.MarkerID]; !exists {
						deleteSet[entry.MarkerID] = struct{}{}
						deleteOrder = append(deleteOrder, entry.MarkerID)
					}
				}
			}
		}
	}
	failedDeletes := map[string]struct{}{}
	for _, id := range deleteOrder {
		if err := w.store.DeleteMarshalProposalMarker(ctx, w.projectID, id); err != nil {
			errs = append(errs, err)
			failedDeletes[id] = struct{}{}
		}
	}
	if indexLoaded {
		kept := entries[:0]
		changed := false
		for _, entry := range entries {
			if matches(entry.RunID, entry.Target) {
				if _, failed := failedDeletes[entry.MarkerID]; !failed {
					changed = true
					continue
				}
			}
			kept = append(kept, entry)
		}
		if changed {
			updated, err := json.Marshal(kept)
			if err != nil {
				errs = append(errs, err)
			} else if err := w.store.RecordMarshalProposalMarker(ctx, w.projectID, markerRefusalSuppressionIndex, string(updated), "recorded"); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (w *Workspace) saveProposalRefusalReceipt(ctx context.Context, occurrenceID string, err error) {
	if occurrenceID == "" || err == nil || w.store == nil || w.projectID == "" {
		return
	}
	receipt, ok := marshal.RefusalOf(err)
	if !ok {
		return
	}
	sanitized := receipt.Sanitized()
	sanitized.Message = RedactContent(sanitized.Message, nil)
	raw, encErr := json.Marshal(sanitized)
	if encErr != nil {
		return
	}
	markerID := markerRefusalReceiptPrefix + occurrenceID
	m := w.marshalSession()
	m.refusalMu.Lock()
	saveErr := w.saveProposalRefusalReceiptLocked(ctx, markerID, string(raw))
	m.refusalMu.Unlock()
	if saveErr != nil {
		w.RecordActivity("Marshal refusal receipt persistence failed: " + saveErr.Error())
	}
}

func (w *Workspace) saveProposalRefusalReceiptLocked(ctx context.Context, markerID, raw string) error {
	var entries []string
	idxRaw, ok, err := w.store.MarshalProposalMarker(ctx, w.projectID, markerRefusalReceiptIndex)
	if err != nil {
		return err
	}
	if ok && idxRaw != "" {
		if err := json.Unmarshal([]byte(idxRaw), &entries); err != nil {
			entries = nil
		}
	}
	if err := w.store.RecordMarshalProposalMarker(ctx, w.projectID, markerID, raw, "refused"); err != nil {
		return err
	}
	filtered := entries[:0]
	for _, id := range entries {
		if id != markerID {
			filtered = append(filtered, id)
		}
	}
	entries = append(filtered, markerID)
	var errs []error
	if len(entries) > maxRefusalReceiptEntries {
		excess := len(entries) - maxRefusalReceiptEntries
		var keptFailed []string
		for _, evict := range entries[:excess] {
			if err := w.store.DeleteMarshalProposalMarker(ctx, w.projectID, evict); err != nil {
				errs = append(errs, err)
				keptFailed = append(keptFailed, evict)
			}
		}
		entries = append(keptFailed, entries[excess:]...)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		errs = append(errs, err)
		return errors.Join(errs...)
	}
	if err := w.store.RecordMarshalProposalMarker(ctx, w.projectID, markerRefusalReceiptIndex, string(encoded), "recorded"); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (w *Workspace) loadProposalRefusalReceipt(ctx context.Context, occurrenceID string) (marshal.RefusalReceipt, bool) {
	if occurrenceID == "" || w.store == nil || w.projectID == "" {
		return marshal.RefusalReceipt{}, false
	}
	raw, ok, err := w.store.MarshalProposalMarker(ctx, w.projectID, markerRefusalReceiptPrefix+occurrenceID)
	if err != nil || !ok || raw == "" {
		return marshal.RefusalReceipt{}, false
	}
	var receipt marshal.RefusalReceipt
	if json.Unmarshal([]byte(raw), &receipt) != nil {
		return marshal.RefusalReceipt{}, false
	}
	return receipt.Sanitized(), true
}

func automaticRefusalEscalationChatNotice(key refusalSuppressionKey, receipt marshal.RefusalReceipt) string {
	subject := key.Op
	if key.Target != "" {
		subject += " " + key.Target
	}
	if subject == "" {
		subject = "proposal"
	}
	blocker := "the active blocker"
	if receipt.Blocker != nil {
		blocker = receipt.Blocker.Label()
	}
	next := "/marshal status"
	if len(receipt.NextCommands) > 0 {
		next = strings.Join(receipt.NextCommands, ", ")
	}
	return fmt.Sprintf(marshalNotePrefix+"Escalation %s: automatic %s failed %d times under the same condition (blocked by %s; %s). Do not repeat the identical proposal; wait for the condition to change or ask the owner. Next: %s.",
		receipt.EscalationID,
		marshal.SanitizeRefusalText(subject),
		receipt.Attempt,
		RedactContent(blocker, nil),
		receipt.EffectsText(),
		RedactContent(next, nil),
	)
}

func formatInboxRefusalDetails(err error) string {
	if err == nil {
		return ""
	}
	receipt, ok := marshal.RefusalOf(err)
	if !ok {
		return "detail: " + RedactContent(marshal.SanitizeRefusalText(err.Error()), nil) + "\n"
	}
	s := receipt.Sanitized()
	var b strings.Builder
	fmt.Fprintf(&b, "code: %s\n", s.Code)
	if s.Blocker != nil {
		fmt.Fprintf(&b, "blocker: %s\n", RedactContent(s.Blocker.Label(), nil))
	}
	fmt.Fprintf(&b, "effects: %s\n", s.Effects)
	if s.Attempt > 0 {
		fmt.Fprintf(&b, "attempt: %d\n", s.Attempt)
	}
	if s.EscalationID != "" {
		fmt.Fprintf(&b, "escalationId: %s\n", s.EscalationID)
	}
	if len(s.NextCommands) > 0 {
		fmt.Fprintf(&b, "nextCommands: %s\n", RedactContent(strings.Join(s.NextCommands, ", "), nil))
	}
	if s.Message != "" {
		fmt.Fprintf(&b, "detail: %s\n", RedactContent(s.Message, nil))
	}
	return b.String()
}

func (w *Workspace) writeMarshalInboxRefusal(subject string, err error) {
	if w.runtime == nil || err == nil {
		return
	}
	text := fmt.Sprintf("## MARSHAL runtime refusal\n\naction: %s\n%s", marshal.SanitizeRefusalText(subject), formatInboxRefusalDetails(err))
	view, e := openMarshalInbox(w.runtime.ProjectRoot())
	if e == nil {
		e = view.write(text)
	}
	if e != nil {
		w.RecordActivity("Marshal refusal inbox delivery failed: " + e.Error())
	}
}
