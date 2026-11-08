//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/permission"
)

const privateDecisionPacketBytes = 16 << 10
const privateDecisionPacketItems = 8

// New bound transport questions have a finite review window distinct from the
// worker's execution deadline. No existing pending deadline is changed here.
func (w *Workspace) privateProcess05ReviewDeadline(ctx context.Context, req permission.Request, now time.Time) (time.Time, bool) {
	ordinary := now.Add(permissionPopupTimeout([]permission.Request{req}))
	if !privateProcess05QuestionIntent(req) || w.runtime == nil {
		return ordinary, false
	}
	m := w.marshalSession()
	m.mu.Lock()
	runID, service := m.runID, m.service
	m.mu.Unlock()
	if service == nil || req.RunID != runID {
		return ordinary, false
	}
	record, err := service.Store.GetMarshalRun(ctx, service.ProjectID, runID)
	if err != nil || !record.Value.Process05Bound || !record.Value.Process05Parallel {
		return ordinary, false
	}
	closed := func() (time.Time, bool) { return now, true }
	executionService := w.runtime.Execution()
	if executionService.Engine() == nil || !w.privateProcess05Question(req) {
		return closed()
	}
	a, err := executionService.Engine().ApprovalManager().GetApproval(strings.TrimPrefix(req.Object, "/marshal approve-task "))
	if err != nil || a.ExpiresAt == nil {
		return closed()
	}
	child, err := executionService.GetRun(ctx, a.RunID)
	if err != nil || !privateProcess05ApprovalBound(ctx, executionService, record.Value, child, w.runtime.ProjectIdentity(), a.ApprovalID) {
		return closed()
	}
	parent, err := executionService.GetRun(ctx, child.ParentRunID)
	budget := record.Value.Budget
	if err != nil || budget != record.Value.Settings.Budget || budget.Validate() != nil || child.BudgetConsumed.WallClockMs < 0 || parent.BudgetConsumed.WallClockMs < 0 {
		return closed()
	}
	if parent.StartedAt.After(now) {
		return closed()
	}
	deadline := ordinary
	capAt := func(at time.Time) {
		if at.Before(deadline) {
			deadline = at
		}
	}
	// Only a known, bounded approved snapshot can expand the ordinary window.
	// Legacy unknown ceilings retain 120s, still capped by canonical expiry.
	if budget.WallTime.Task > 0 && budget.WallTime.Plan > 0 && !parent.StartedAt.IsZero() && !parent.StartedAt.After(now) {
		// Round known usage up and cap before converting approved integers to
		// Duration: neither overflow nor subsecond rounding can enlarge scope.
		chargedSeconds := func(ms int64) int64 {
			n := ms / 1000
			if ms%1000 != 0 {
				n++
			}
			return n
		}
		taskRemaining := budget.WallTime.Task - chargedSeconds(child.BudgetConsumed.WallClockMs)
		elapsed := now.Sub(parent.StartedAt)
		elapsedSeconds := int64(elapsed / time.Second)
		if elapsed%time.Second != 0 {
			elapsedSeconds++
		}
		planCharged := chargedSeconds(parent.BudgetConsumed.WallClockMs)
		if planCharged < elapsedSeconds {
			planCharged = elapsedSeconds
		}
		planRemaining := budget.WallTime.Plan - planCharged
		if taskRemaining <= 0 || planRemaining <= 0 {
			return closed()
		}
		reviewSeconds := int64(300)
		if taskRemaining < reviewSeconds {
			reviewSeconds = taskRemaining
		}
		if planRemaining < reviewSeconds {
			reviewSeconds = planRemaining
		}
		deadline = now.Add(time.Duration(reviewSeconds) * time.Second)
	}
	capAt(*a.ExpiresAt)
	goal, err := w.runtime.Store().GetGoalContract(ctx, parent.GoalID, parent.GoalRevision)
	if err != nil || !goal.Confirmation.Settled() {
		return closed()
	}
	policies := append([]string(nil), goal.DoNotDo...)
	for _, constraint := range goal.Constraints {
		if constraint.IsHard {
			policies = append(policies, constraint.Text)
		}
	}
	for _, policy := range policies {
		if limit, specified, known := privateReviewPolicyLimit(policy); specified {
			if !known {
				return closed()
			}
			capAt(now.Add(limit))
		}
	}
	return deadline, true
}

var privateReviewSubject = regexp.MustCompile(`(?i)\b(?:reviews?|reviewing|reviewed|decisions?|decid(?:e|es|ed|ing)|approvals?|approv(?:e|es|ed|ing)|permissions?|permit(?:s|ted|ting)?|authori[sz](?:e|es|ed|ing)|deny|denies|denied|denying|reject(?:s|ed|ing)?|transport)\b`)
var privateReviewTiming = regexp.MustCompile(`(?i)\b(?:deadlines?|timeouts?|within|at most|no more than|max(?:imum)?|limits?|limited|bounded)\b`)

// Capture the complete amount, including unsupported decimal/sign/exponent or
// word forms. Partial integer matching must never enlarge a written limit.
var privateReviewUnits = regexp.MustCompile(`(?i)(?:^|[\s(])([+.\-]?[0-9][0-9a-z.,+\-]*|[a-z]+[ \t]+)[ \t]*(seconds?|secs?|minutes?|mins?|milliseconds?|hours?|hrs?|ms|s|m|h)\b`)
var privateReviewInteger = regexp.MustCompile(`^[0-9]+$`)
var privateReviewClauses = regexp.MustCompile(`[;!?\n]|\.[ \t]+`)

// This is a conservative limit reader, not general natural-language policy
// interpretation. Unrecognized explicit review timing refuses the new path.
func privateReviewPolicyLimit(text string) (time.Duration, bool, bool) {
	limit, specified := 300*time.Second, false
	words := map[string]int64{"one": 1, "two": 2, "three": 3, "five": 5, "ten": 10}
	for _, clause := range privateReviewClauses.Split(text, -1) {
		if !privateReviewSubject.MatchString(clause) {
			continue
		}
		matches := privateReviewUnits.FindAllStringSubmatch(clause, -1)
		if !privateReviewTiming.MatchString(clause) && len(matches) == 0 {
			continue
		}
		specified = true
		if len(matches) == 0 {
			return 0, true, false
		}
		for _, match := range matches {
			amount := strings.ToLower(strings.TrimSpace(match[1]))
			n, ok := words[amount]
			if !ok {
				if !privateReviewInteger.MatchString(amount) {
					return 0, true, false
				}
				var err error
				n, err = strconv.ParseInt(amount, 10, 64)
				if err != nil || n <= 0 || n > 86400 {
					return 0, true, false
				}
			}
			unit := strings.ToLower(match[2])
			var factor time.Duration
			switch unit {
			case "second", "seconds", "sec", "secs", "s":
				factor = time.Second
			case "minute", "minutes", "min", "mins", "m":
				factor = time.Minute
			default:
				return 0, true, false
			}
			if d := time.Duration(n) * factor; d < limit {
				limit = d
			}
		}
	}
	// A timing clause separated from its review subject is ambiguous. Do not
	// infer that an unrelated execution duration defines the review deadline.
	if !specified && privateReviewSubject.MatchString(text) && (privateReviewTiming.MatchString(text) || privateReviewUnits.MatchString(text)) {
		return 0, true, false
	}
	return limit, specified, true
}

type privateDecisionRegistrationKey struct{}

func deferPrivateDecisionDelivery(ctx context.Context) bool {
	v, _ := ctx.Value(privateDecisionRegistrationKey{}).(bool)
	return v
}

// Only presentation is coalesced. Each original audited request, deadline and
// live MCP decision callback retains its independent authority boundary.
func (w *Workspace) registerPrivateProcess05Questions(ctx context.Context, batch []permission.Request) {
	w.tmuxMu.Lock()
	chat := w.tmuxActiveWins["marshal-chat"]
	codex := chat != nil && chat.provider == "codex"
	w.tmuxMu.Unlock()
	if !codex {
		return
	}
	ctx = context.WithValue(ctx, privateDecisionRegistrationKey{}, true)
	for _, req := range batch {
		// Pre-registration only creates new questions. The normal polling
		// phase settles existing answers/timeouts exactly once; settling here
		// would route the same batch item again after removing its pending ID.
		w.permissions.mu.Lock()
		registered := false
		for _, pending := range w.permissions.build.pending {
			if pending.request.Key() == req.Key() {
				registered = true
				break
			}
		}
		w.permissions.mu.Unlock()
		if registered {
			continue
		}
		if w.privateProcess05Question(req) {
			w.routeBuildPermission(ctx, req)
		}
	}
}

func privateProcess05QuestionIntent(req permission.Request) bool {
	if req.Kind != "marshal-command" || req.RunID == "" || !strings.HasPrefix(req.Object, "/marshal approve-task ") {
		return false
	}
	var approval execution.RuntimeApproval
	return json.Unmarshal([]byte(req.ApprovalBinding), &approval) == nil && approval.OperationType == "PROVIDER_TRANSPORT"
}
func (w *Workspace) privateProcess05Question(req permission.Request) bool {
	if w.runtime == nil || req.Kind != "marshal-command" || req.ProposalID != "" || req.SourceLaunch != "" || req.SourceOccurrence != "" || req.RunID == "" {
		return false
	}
	id, ok := strings.CutPrefix(req.Object, "/marshal approve-task ")
	if !ok || id == "" || req.ApprovalBinding == "" {
		return false
	}
	m := w.marshalSession()
	m.mu.Lock()
	runID, service, pending := m.runID, m.service, m.pending
	m.mu.Unlock()
	if req.RunID != runID || service == nil || pending != nil {
		return false
	}
	record, err := service.Store.GetMarshalRun(context.Background(), service.ProjectID, runID)
	if err != nil || req.TaskID != marshalRunProposalBinding(record.Value.PlanVersion, record.Revision, nil) || !w.pendingProcess05Approval(context.Background(), record.Value, id) {
		return false
	}
	var reviewed execution.RuntimeApproval
	if json.Unmarshal([]byte(req.ApprovalBinding), &reviewed) != nil || reviewed.ApprovalID != id || reviewed.OperationType != "PROVIDER_TRANSPORT" || reviewed.Status != execution.ApprovalRequested {
		return false
	}
	actual, err := w.runtime.Execution().Engine().ApprovalManager().GetApproval(id)
	if err != nil {
		return false
	}
	body, err := json.Marshal(actual)
	return err == nil && string(body) == req.ApprovalBinding && actual.ExpiresAt != nil && time.Now().Before(*actual.ExpiresAt)
}

type privateDecisionItem struct {
	id       string
	p        *buildPendingDecision
	request  permission.Request
	deadline time.Time
}

func privateDecisionItemFor(id string, p *buildPendingDecision) privateDecisionItem {
	return privateDecisionItem{id, p, p.request, p.deadline}
}

func (w *Workspace) privateDecisionPacket(id string, p *buildPendingDecision) ([]privateDecisionItem, string, bool) {
	items := []privateDecisionItem{privateDecisionItemFor(id, p)}
	if privateProcess05QuestionIntent(p.request) {
		w.permissions.mu.Lock()
		candidates := make([]privateDecisionItem, 0, len(w.permissions.build.pending))
		for otherID, other := range w.permissions.build.pending {
			if otherID != id && other.provider == p.provider && other.paneID == p.paneID && other.launchID == p.launchID && other.request.RunID == p.request.RunID && other.request.TaskID == p.request.TaskID && !other.delivered && other.answer == nil && time.Now().Before(other.deadline) {
				candidates = append(candidates, privateDecisionItemFor(otherID, other))
			}
		}
		w.permissions.mu.Unlock()
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].id < candidates[j].id })
		for _, candidate := range candidates {
			if len(items) < privateDecisionPacketItems && privateProcess05QuestionIntent(candidate.request) {
				items = append(items, candidate)
			}
		}
	}
	if len(items) == 1 {
		return items, p.text, !privateProcess05QuestionIntent(p.request) || len(p.text) <= privateDecisionPacketBytes
	}
	texts := make([]string, 0, len(items))
	for _, item := range items {
		texts = append(texts, item.p.text)
	}
	payload := "Independent pending transport questions for this exact current run. Judge each separately through its existing decision id; this packet grants no approval. " + strings.Join(texts, " ")
	return items, payload, len(payload) <= privateDecisionPacketBytes
}
