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
	"strconv"
	"strings"
	"unicode"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

const marshalProposalPrefix = "MARSHAL_PROPOSAL "

type marshalProposal struct {
	reviewed                                                              *app.PlanApprovalSnapshot
	occurrence                                                            string
	who                                                                   string
	sourceSession, revision, digest, instructionRef, criteria             string
	action, key, value, provider, path, id, reason, language string
	taskID, targetWorker, model                              string
	question, choices, card                                               string
	ids                                                                   string
	freshSession                                                          bool
}

// Parse a single visible assistant line, never shell source. Reject unknown,
// duplicate, non-string and action-inappropriate fields and trailing data.
func parseMarshalProposal(line string) (marshalProposal, error) {
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
	p.ids = fields["ids"]
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
			return p, invalid
		}
	case "hold":
		keys = []string{"action", "ids", "reason"}
		if _, err := parseHoldIDs(p.ids); err != nil {
			return p, invalid
		}
		if strings.TrimSpace(p.reason) == "" || len(p.reason) > 500 {
			return p, invalid
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
		if strings.TrimSpace(p.reason) == "" || len(p.reason) > 500 {
			return p, invalid
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
				w.RecordActivity("Ignored Marshal proposal: malformed or non-allow-listed fields.")
				continue
			}
			w.enqueueValidatedMarshalProposal(p)
		}
	}
}

// A draft may be written before its observer imports it. Keep its proposal
// pending until the runtime has a plan identity and version to display and bind.
func (w *Workspace) queueMarshalPlanApproval() {
	w.permissions.mu.Lock()
	pending := w.permissions.planApprovalPending
	proposalRunID := w.permissions.planApprovalRunID
	occurrence := w.permissions.planApprovalOccurrence
	w.permissions.mu.Unlock()
	if !pending {
		return
	}
	m := w.marshalSession()
	m.mu.Lock()
	service, runID, amendment := m.service, m.runID, m.pending
	m.mu.Unlock()
	if service == nil || runID == "" || (proposalRunID != "" && proposalRunID != runID) {
		return
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
		return
	}
	w.permissions.mu.Lock()
	if w.permissions.planApprovalOccurrence == occurrence && w.permissions.planApprovalRunID == proposalRunID {
		w.permissions.planApprovalPending = false
	}
	w.permissions.mu.Unlock()
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
		return errors.New("no matching active plan")
	}
	record, err := service.Store.GetMarshalRun(context.Background(), service.ProjectID, runID)
	if err != nil {
		return err
	}
	run := record.Value
	if p.action == "approve" || p.action == "amend-approve" {
		if err := refuseUnimportedMarshalDraft(service.Repository); err != nil {
			return err
		}
	}
	if p.action == "approve" && run.State != marshal.Drafting {
		return errors.New("plan is not awaiting approval")
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
			return errors.New("task is not in the active plan")
		}
	}
	if p.action == "approve-task" {
		if !w.pendingProcess05Approval(context.Background(), run, p.id) {
			return errors.New("approval does not belong to the active Marshal plan")
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
		return errors.New("Process 05 continuation gates are not ready")
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
	if strings.HasPrefix(req.TaskID, "reserved:") {
		m := w.marshalSession()
		m.mu.Lock()
		service, runID := m.service, m.runID
		m.mu.Unlock()
		if service == nil || runID != req.RunID {
			return errors.New("reserved merge approval expired")
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
			return errors.New("reserved merge approval expired")
		}
		m.grant(runID, req.TaskID)
		_, err = w.cmd.handleMarshal(ctx, []string{"resume"})
		if err != nil {
			m.mu.Lock()
			delete(m.approvals, runID+"/"+req.TaskID)
			m.mu.Unlock()
		}
		return err
	}
	if req.RunID != "" {
		w.permissions.mu.Lock()
		p, ok := w.permissions.runProposals[req.Key()]
		w.permissions.mu.Unlock()
		if !ok || p.command() != req.Object {
			return errors.New("invalid Marshal run proposal")
		}
		m := w.marshalSession()
		m.mu.Lock()
		service, runID, pending := m.service, m.runID, m.pending
		m.mu.Unlock()
		if service == nil || runID != req.RunID || (pending != nil && p.action != "amend-approve" && p.action != "amend-deny") || (pending == nil && (p.action == "amend-approve" || p.action == "amend-deny")) {
			return errors.New("Marshal proposal expired")
		}
		binding := req.TaskID
		if p.action == "approve" || p.action == "amend-approve" {
			var snapshot app.PlanApprovalSnapshot
			if req.ApprovalBinding == "" || json.Unmarshal([]byte(req.ApprovalBinding), &snapshot) != nil {
				return errors.New("plan snapshot missing; review and approve again")
			}
			p.reviewed = &snapshot
		}
		record, err := service.Store.GetMarshalRun(ctx, service.ProjectID, runID)
		if err != nil || (p.action != "deliver" && marshalRunProposalBinding(record.Value.PlanVersion, record.Revision, pending) != binding) || (p.action == "deliver" && record.Value.ApprovalScopeDigest != req.ApprovalBinding) {
			return errors.New("Marshal proposal expired")
		}
		if p.action == "approve" || p.action == "amend-approve" {
			_, err = w.marshalApproveReviewed(ctx, p.reviewed)
			return err
		}
		if p.action == "deliver" {
			_, cancel, err := m.reserve()
			if err != nil {
				return err
			}
			purpose := "standing-close:" + record.Value.ApprovalScopeDigest
			m.grant(runID, purpose)
			authErr := service.AuthorizeDelivery(ctx, runID, record.Value.ApprovalScopeDigest)
			m.mu.Lock()
			delete(m.approvals, runID+"/"+purpose)
			m.mu.Unlock()
			m.finish(cancel)
			if authErr != nil {
				return authErr
			}
			_, err = w.marshalResume(ctx)
			return err
		}
		if err := w.validatePrivateProcess05Resume(ctx, req, p); err != nil {
			return err
		}
		// Dispatch arguments directly so spaces in free-text reasons are exact.
		_, err = w.cmd.handleMarshal(ctx, marshalProposalArgs(p))
		return err
	}
	if req.Who == hostWatchActor {
		// A host-watch lane-cap request is applied only once, and only
		// while the guard that posted it still stands behind it.
		if err := w.takeHostCapRequest(req); err != nil {
			return err
		}
		_, err := w.cmd.Handle(ctx, req.Object)
		return err
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
		return errors.New("invalid Marshal command proposal")
	}
	data, _ := json.Marshal(map[string]string{"action": "setting", "key": parts[2], "value": parts[3]})
	if _, err := parseMarshalProposal(marshalProposalPrefix + string(data)); err != nil {
		return err
	}
	_, err := w.cmd.Handle(ctx, req.Object)
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
		text = "## MARSHAL operator popup decision\n\nApplication failed; do not continue as approved.\n"
	}
	if w.runtime == nil {
		return
	}
	view, e := openInboxView(w.runtime.ProjectRoot(), "marshal", false)
	if e == nil {
		e = view.write(text)
	}
	if e != nil {
		w.RecordActivity("Marshal popup decision delivery failed: " + e.Error())
	}
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
			continue
		}
		sessionID := fmt.Sprintf("proposal-file:%s:%d:%x", name, info.ModTime().UnixNano(), sha256.Sum256(data))
		fingerprint := fmt.Sprintf("%s\x00%d\x00%s\x00%s", sessionID, 0, "", line)
		id := fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprint)))
		who := "Local request (unverified)"
		if proposal.action == "architecture" && root == w.providerRoot() {
			if bound := w.boundMarshalArchitectureSource(); bound != "" {
				id = fmt.Sprintf("runtime:%x", sha256.Sum256([]byte(bound+"\x00"+fingerprint)))
				who = "Marshal"
			}
		}
		if w.store == nil {
			continue
		}
		pending, admitErr := w.store.AdmitMarshalProposal(context.Background(), w.projectID, id, line)
		if admitErr != nil {
			continue
		}
		// Durable admission precedes source consumption. A crash on either
		// side of the rename replays the same occurrence, not a new request.
		if err := fs.Rename(name, name+".consumed"); err != nil {
			continue
		}
		if !pending {
			continue
		}
		w.observeMarshalProposalSource(importer.SessionTranscript{SessionID: sessionID, Messages: []importer.Message{{Role: "assistant", Content: line}}}, id, who)
	}
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
	for _, record := range pending {
		if w.staleProposalMCP(record.ID) {
			continue
		}
		if w.recoverBoundMCPMemory(record) {
			continue
		}
		if record.RequestJSON == "" {
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
			openErr = errors.New("architecture auto-open requires the bound live Marshal source; use /marshal architecture")
		} else {
			_, openErr = w.marshalArchitecture(w.workers.context(), "", p.occurrence)
		}
		if openErr != nil {
			w.resolveArchitectureProposal(p.occurrence, openErr)
			w.RecordActivity("Ignored Marshal architecture proposal: " + openErr.Error())
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
			if w.store != nil && p.occurrence != "" {
				_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, "rejected")
			}
			w.RecordActivity("Ignored Marshal proposal: " + err.Error())
		}
	}
}
