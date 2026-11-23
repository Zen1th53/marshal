package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/redaction"
	"github.com/Zen1th53/marshal/internal/store"
)

// ApprovalNoticeView is the read-only projection of a bound approval ticket
// for MCP pull, Inbox and Board surfaces.
type ApprovalNoticeView struct {
	ApprovalID     string                   `json:"approval_id"`
	RunID          string                   `json:"run"`
	MarshalRunID   string                   `json:"marshal_run_id,omitempty"`
	TaskID         string                   `json:"task"`
	Attempt        int                      `json:"attempt"`
	ScopeDigest    string                   `json:"scope_digest"`
	Authority      string                   `json:"authority"`
	CreatedAt      time.Time                `json:"created_at"`
	ExpiresAt      *time.Time               `json:"expires_at,omitempty"`
	OperationType  string                   `json:"operation_type,omitempty"`
	TargetResource string                   `json:"target_resource,omitempty"`
	Status         execution.ApprovalStatus `json:"status"`
	Expired        bool                     `json:"expired"`
	ReplacementID  string                   `json:"replacement_id,omitempty"`
	Revoked        bool                     `json:"revoked,omitempty"`
	RevokedReason  string                   `json:"revoked_reason,omitempty"`
	ApprovedBy     string                   `json:"approved_by,omitempty"`
	DecisionReason string                   `json:"decision_reason,omitempty"`
	Destination    string                   `json:"destination"`
	Command        string                   `json:"command"`
	WhoCanAnswer   string                   `json:"who_can_answer"`
	OwnerNotice    string                   `json:"owner_notice"`
	MarshalNotice  string                   `json:"marshal_notice"`
}

// RedactApprovalText strips sensitive tokens/credentials and collapses control
// characters so approval commands and notices are safe for logs, mail and MCP.
func RedactApprovalText(s string) string {
	s = redaction.RedactAll(s)
	s = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// ApprovalDestinationInfo returns the authoritative actor who can answer, the
// actual owner destination, the redacted exact command, and explicit wording
// for both owner and Marshal notices naming who can answer.
func ApprovalDestinationInfo(a execution.RuntimeApproval) (authority, destination, command, ownerNotice, marshalNotice string) {
	id := a.ApprovalID
	task := a.TaskID
	if task == "" {
		task = "task"
	}
	if !isExtremeBuild || a.Authority == "owner" || a.HardViolation {
		authority = "owner"
		destination = "Owner (/approve)"
		command = RedactApprovalText("/approve execution:" + id)
		ownerNotice = RedactApprovalText(fmt.Sprintf(
			"Approval execution:%s for task %s awaits the owner's decision: /approve execution:%s or /reject execution:%s [reason].",
			id, task, id, id,
		))
		marshalNotice = RedactApprovalText(fmt.Sprintf(
			"Approval execution:%s for task %s requires the owner's decision (/approve execution:%s or /reject execution:%s). Only the owner can answer.",
			id, task, id, id,
		))
		return
	}
	authority = "marshal"
	destination = "Marshal chat (/marshal approve-task)"
	command = RedactApprovalText("/marshal approve-task " + id)
	ownerNotice = RedactApprovalText(fmt.Sprintf(
		"Approval %s for task %s is waiting for the Marshal to answer (owner command: /marshal approve-task %s).",
		id, task, id,
	))
	marshalNotice = RedactApprovalText(fmt.Sprintf(
		"Approval %s for task %s is waiting for the Marshal's decision (submit action=decide or /marshal approve-task %s).",
		id, task, id,
	))
	return
}

// RecordApprovalOpened stores an opened approval outbox entry and durable audit
// event in one SQLite transaction with phase_order=0 so opened always queues
// ahead of terminal/closed entries.
func (r *Runtime) RecordApprovalOpened(ctx context.Context, marshalRunID string, a execution.RuntimeApproval) error {
	if r == nil || r.store == nil || a.ApprovalID == "" {
		return nil
	}
	ident := a.Identity()
	authority, destination, command, ownerNotice, marshalNotice := ApprovalDestinationInfo(a)
	if ident.Authority == "" {
		ident.Authority = authority
	}
	runID := marshalRunID
	if runID == "" {
		runID = a.RunID
	}
	now := a.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	eventID := "approval:" + a.ApprovalID + ":opened"
	payloadMap := map[string]any{
		"approval_id":    ident.ApprovalID,
		"run":            ident.RunID,
		"marshal_run_id": runID,
		"task":           ident.TaskID,
		"attempt":        ident.Attempt,
		"scope_digest":   ident.ScopeDigest,
		"authority":      authority,
		"created_at":     ident.CreatedAt.UTC().Format(time.RFC3339Nano),
		"status":         string(a.Status),
		"transition":     "opened",
		"destination":    destination,
		"command":        command,
		"who_can_answer": authority,
		"owner_notice":   ownerNotice,
		"marshal_notice": marshalNotice,
		"target":         RedactApprovalText(a.TargetResource),
		"scope":          RedactApprovalText(a.Scope),
	}
	if ident.ExpiresAt != nil {
		payloadMap["expires_at"] = ident.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	payloadBytes, _ := json.Marshal(payloadMap)
	entry := store.NoticeOutboxEntry{
		EventID:     eventID,
		ProjectID:   r.ProjectIdentity(),
		RunID:       runID,
		TaskID:      a.TaskID,
		ApprovalID:  a.ApprovalID,
		Category:    "approval",
		Transition:  "opened",
		PhaseOrder:  0,
		Status:      string(execution.ApprovalRequested),
		Authority:   authority,
		Destination: destination,
		Command:     command,
		Summary:     marshalNotice,
		PayloadJSON: string(payloadBytes),
		CreatedAt:   now,
	}
	auditEvent := model.Event{
		ID:        eventID,
		Type:      "approval.requested",
		ProjectID: r.ProjectID(),
		Timestamp: now,
		Data:      payloadMap,
	}
	project, err := r.store.Project(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	auditEvent.ProjectID = project.ID
	_, err = r.store.RecordNoticeWithEvent(context.WithoutCancel(ctx), entry, auditEvent)
	return err
}

// RecordApprovalTerminal stores a terminal (or post-allow revocation) approval
// outbox entry and durable audit event in one SQLite transaction with
// phase_order=1 so it always queues after opened notices.
func (r *Runtime) RecordApprovalTerminal(ctx context.Context, marshalRunID string, a execution.RuntimeApproval, transition, reason string) error {
	if r == nil || r.store == nil || a.ApprovalID == "" {
		return nil
	}
	ident := a.Identity()
	authority, destination, command, _, _ := ApprovalDestinationInfo(a)
	if transition == "" {
		transition = strings.ToLower(string(a.Status))
	}
	runID := marshalRunID
	if runID == "" {
		runID = a.RunID
	}
	now := time.Now().UTC()
	if a.RevokedAt != nil {
		now = *a.RevokedAt
	} else if a.ResolvedAt != nil {
		now = *a.ResolvedAt
	}
	eventID := "approval:" + a.ApprovalID + ":terminal"
	if a.Revoked && a.ApprovedBy != "" {
		eventID = "approval:" + a.ApprovalID + ":revoked"
	}
	redactedReason := RedactApprovalText(reason)
	if redactedReason == "" {
		if a.RevokedReason != "" {
			redactedReason = RedactApprovalText(a.RevokedReason)
		} else if a.DecisionReason != "" {
			redactedReason = RedactApprovalText(a.DecisionReason)
		}
	}
	if a.Status == execution.ApprovalExpired && a.TaskID != "" {
		command = RedactApprovalText("/marshal retry " + a.TaskID)
	}
	summary := fmt.Sprintf("Approval %s for task %s transitioned to %s", a.ApprovalID, a.TaskID, a.Status)
	if a.ReplacementID != "" {
		summary += fmt.Sprintf(" (replacement: %s)", a.ReplacementID)
	}
	if a.Revoked {
		summary += " [revoked]"
	}
	if redactedReason != "" {
		summary += ": " + redactedReason
	}
	summary = RedactApprovalText(summary)
	payloadMap := map[string]any{
		"approval_id":    ident.ApprovalID,
		"run":            ident.RunID,
		"marshal_run_id": runID,
		"task":           ident.TaskID,
		"attempt":        ident.Attempt,
		"scope_digest":   ident.ScopeDigest,
		"authority":      authority,
		"status":         string(a.Status),
		"transition":     transition,
		"destination":    destination,
		"command":        command,
		"who_can_answer": authority,
		"replacement_id": a.ReplacementID,
		"revoked":        a.Revoked,
		"approved_by":    a.ApprovedBy,
		"reason":         redactedReason,
		"summary":        summary,
	}
	if ident.ExpiresAt != nil {
		payloadMap["expires_at"] = ident.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	payloadBytes, _ := json.Marshal(payloadMap)
	eventType := approvalAuditEventType(a.Status, a.Revoked)
	entry := store.NoticeOutboxEntry{
		EventID:       eventID,
		ProjectID:     r.ProjectIdentity(),
		RunID:         runID,
		TaskID:        a.TaskID,
		ApprovalID:    a.ApprovalID,
		Category:      "approval",
		Transition:    transition,
		PhaseOrder:    1,
		Status:        string(a.Status),
		Authority:     authority,
		Destination:   destination,
		Command:       command,
		ReplacementID: a.ReplacementID,
		Summary:       summary,
		PayloadJSON:   string(payloadBytes),
		CreatedAt:     now,
	}
	auditEvent := model.Event{
		ID:        eventID,
		Type:      eventType,
		ProjectID: r.ProjectID(),
		Timestamp: now,
		Data:      payloadMap,
	}
	project, err := r.store.Project(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	auditEvent.ProjectID = project.ID
	_, err = r.store.RecordNoticeWithEvent(context.WithoutCancel(ctx), entry, auditEvent)
	return err
}

func approvalAuditEventType(status execution.ApprovalStatus, revoked bool) string {
	switch status {
	case execution.ApprovalApproved:
		return "approval.granted"
	case execution.ApprovalDenied:
		return "approval.denied"
	case execution.ApprovalExpired:
		return "approval.expired"
	case execution.ApprovalSuperseded:
		return "approval.superseded"
	case execution.ApprovalClosed:
		return "approval.closed"
	case execution.ApprovalInterrupted:
		return "approval.interrupted"
	default:
		if revoked {
			return "approval.revoked"
		}
		return "approval.closed"
	}
}

// ExpireApproval transitions an overdue request and repairs its terminal notice
// when expiry was already recorded by another path.
func (r *Runtime) ExpireApproval(ctx context.Context, marshalRunID, approvalID string, now time.Time) (*execution.RuntimeApproval, bool, error) {
	if r == nil || r.Execution() == nil || r.Execution().Engine() == nil {
		return nil, false, model.ErrUnavailable
	}
	after, changed, err := r.Execution().Engine().ApprovalManager().Expire(approvalID, now)
	if err != nil {
		return after, false, err
	}
	if after != nil && after.Status != execution.ApprovalRequested {
		if recErr := r.RecordApprovalTerminal(ctx, marshalRunID, *after, "", ""); recErr != nil {
			return after, changed, recErr
		}
	}
	return after, changed, nil
}

// SupersedeApproval atomically supersedes a REQUESTED or unconsumed APPROVED
// approval with a replacement ticket and records the terminal/revocation outbox notice.
func (r *Runtime) SupersedeApproval(ctx context.Context, marshalRunID, approvalID, replacementID, reason string, now time.Time) (*execution.RuntimeApproval, bool, error) {
	if r == nil || r.Execution() == nil || r.Execution().Engine() == nil {
		return nil, false, model.ErrUnavailable
	}
	after, changed, err := r.Execution().Engine().ApprovalManager().Supersede(approvalID, replacementID, reason, now)
	if err != nil {
		return after, false, err
	}
	if changed && after != nil {
		transition := "superseded"
		if after.Revoked && after.ApprovedBy != "" {
			transition = "revoked"
		}
		if recErr := r.RecordApprovalTerminal(ctx, marshalRunID, *after, transition, reason); recErr != nil {
			return after, true, recErr
		}
	}
	return after, changed, nil
}

// CloseRunApprovals closes and revokes any open REQUESTED or unconsumed
// APPROVED approvals bound to the closed Marshal run and records their
// terminal/revocation outbox notices.
func (r *Runtime) CloseRunApprovals(ctx context.Context, marshalRunID string, run marshal.Run, reason string, now time.Time) ([]execution.RuntimeApproval, error) {
	if r == nil || r.Execution() == nil || r.Execution().Engine() == nil {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	boundApprovals := r.boundApprovalIDs(ctx, marshalRunID, run)
	all, err := r.Execution().Engine().ApprovalManager().ListApprovals()
	if err != nil {
		return nil, err
	}
	var closed []execution.RuntimeApproval
	for _, a := range all {
		if !approvalMatchesMarshalRun(a, run, boundApprovals) {
			continue
		}
		if a.Status != execution.ApprovalRequested && a.Status != execution.ApprovalApproved {
			continue
		}
		after, changed, closeErr := r.Execution().Engine().ApprovalManager().CloseApproval(a.ApprovalID, reason, now)
		if closeErr != nil {
			return closed, closeErr
		}
		if changed && after != nil {
			transition := "closed"
			if after.Revoked && after.ApprovedBy != "" {
				transition = "revoked"
			}
			if recErr := r.RecordApprovalTerminal(ctx, marshalRunID, *after, transition, reason); recErr != nil {
				return closed, recErr
			}
			closed = append(closed, *after)
		}
	}
	return closed, nil
}

// ReconcileInterruptedApproval evaluates an interrupted REQUESTED approval:
// when deadline and binding are intact it stays REQUESTED and replays its
// opened notice; otherwise it closes as INTERRUPTED/revoked and emits a
// terminal notice.
func (r *Runtime) ReconcileInterruptedApproval(ctx context.Context, marshalRunID, approvalID string, bindingIntact bool, reason string, now time.Time) (*execution.RuntimeApproval, bool, error) {
	if r == nil || r.Execution() == nil || r.Execution().Engine() == nil {
		return nil, false, model.ErrUnavailable
	}
	after, changed, err := r.Execution().Engine().ApprovalManager().ReconcileInterrupted(approvalID, bindingIntact, reason, now)
	if err != nil {
		return after, false, err
	}
	if changed && after != nil {
		if recErr := r.RecordApprovalTerminal(ctx, marshalRunID, *after, "interrupted", reason); recErr != nil {
			return after, true, recErr
		}
		return after, true, nil
	}
	if after != nil && after.Status == execution.ApprovalRequested {
		if err := r.RecordApprovalOpened(ctx, marshalRunID, *after); err != nil {
			return after, false, err
		}
	}
	return after, false, nil
}

// DeliverPendingNotices replays unacknowledged outbox entries in strict order
// (opened ahead of closed), renders current live status when delivery happens
// after agreement/expiry/run close, admits by stable event ID in the durable
// accepting inbox (deduping crash-before-ack replays), and acks only after
// durable admission succeeds.
func (r *Runtime) DeliverPendingNotices(ctx context.Context, consumer, runID string, deliver func(entry store.NoticeOutboxEntry, rendered string) error) (int, error) {
	if r == nil || r.store == nil {
		return 0, model.ErrUnavailable
	}
	r.noticeDeliveryMu.Lock()
	defer r.noticeDeliveryMu.Unlock()
	if svc := r.Execution(); svc != nil && svc.Engine() != nil {
		approvals, err := svc.Engine().ApprovalManager().ListApprovals()
		if err != nil {
			return 0, err
		}
		for _, a := range approvals {
			if a.Status == execution.ApprovalRequested {
				continue
			}
			binding, err := r.store.ApprovalNoticeRun(ctx, r.ProjectIdentity(), a.ApprovalID)
			if err != nil {
				return 0, err
			}
			if binding == "" {
				binding = r.resolveMarshalRunIDForExecution(ctx, a.RunID, a.PlanID, a.PlanVersion)
			}
			if runID != "" && binding != runID {
				continue
			}
			if err := r.RecordApprovalTerminal(ctx, binding, a, "", ""); err != nil {
				return 0, err
			}
		}
	}
	pending, err := r.store.PendingNotices(ctx, r.ProjectIdentity(), runID, 200)
	if err != nil {
		return 0, err
	}
	acked := 0
	for _, entry := range pending {
		rendered := r.renderNoticeWithLiveStatus(ctx, entry)
		admitted, err := r.store.AdmitNotice(ctx, consumer, entry.EventID, "", time.Now().UTC())
		if err != nil {
			return acked, err
		}
		if admitted && deliver != nil {
			if err := deliver(entry, rendered); err != nil {
				if releaseErr := r.store.ReleaseNotice(ctx, consumer, entry.EventID); releaseErr != nil {
					return acked, fmt.Errorf("deliver notice: %v; release claim: %w", err, releaseErr)
				}
				return acked, err
			}
		}
		if err := r.store.AckNotice(ctx, entry.EventID, time.Now().UTC()); err != nil {
			return acked, err
		}
		acked++
	}
	return acked, r.store.PruneNotices(ctx, time.Now().UTC().Add(-30*24*time.Hour))
}

func (r *Runtime) renderNoticeWithLiveStatus(ctx context.Context, entry store.NoticeOutboxEntry) string {
	base := RedactApprovalText(entry.Summary)
	if base == "" {
		base = RedactApprovalText(fmt.Sprintf("Approval %s (%s): %s", entry.ApprovalID, entry.Transition, entry.Command))
	}
	if entry.ApprovalID == "" || r.Execution() == nil || r.Execution().Engine() == nil {
		return base
	}
	live, err := r.Execution().Engine().ApprovalManager().GetApproval(entry.ApprovalID)
	if err != nil || live == nil {
		return base
	}
	currentStatus := live.Status
	if currentStatus == execution.ApprovalRequested && live.ExpiresAt != nil && !time.Now().UTC().Before(*live.ExpiresAt) {
		currentStatus = execution.ApprovalExpired
	}
	// Also check if the owning Marshal run closed since the notice was queued.
	if entry.RunID != "" && r.store != nil {
		if rec, err := r.store.GetMarshalRun(ctx, r.ProjectIdentity(), entry.RunID); err == nil && rec.Value.State == marshal.Closed && currentStatus == execution.ApprovalRequested {
			currentStatus = execution.ApprovalClosed
		}
	}
	if string(currentStatus) != entry.Status {
		note := fmt.Sprintf(" [current status: %s", currentStatus)
		if live.ReplacementID != "" {
			note += fmt.Sprintf(", replacement: %s", live.ReplacementID)
		}
		note += "]"
		base += note
	}
	return base
}

// ApprovalProjection returns the read-only, redacted approval list and durable
// outbox notices strictly scoped to the given Marshal run.
func (r *Runtime) ApprovalProjection(ctx context.Context, marshalRunID string, run marshal.Run, includeClosed bool) ([]ApprovalNoticeView, []store.NoticeOutboxEntry, error) {
	if r == nil || r.store == nil {
		return nil, nil, model.ErrUnavailable
	}
	now := time.Now().UTC()
	boundApprovals := r.boundApprovalIDs(ctx, marshalRunID, run)
	views := make([]ApprovalNoticeView, 0)
	if execSvc := r.Execution(); execSvc != nil && execSvc.Engine() != nil {
		all, err := execSvc.Engine().ApprovalManager().ListApprovals()
		if err != nil {
			return nil, nil, err
		}
		for _, a := range all {
			if !approvalMatchesMarshalRun(a, run, boundApprovals) {
				continue
			}
			effectiveStatus := a.Status
			expired := a.Status == execution.ApprovalExpired
			if a.Status == execution.ApprovalRequested && a.ExpiresAt != nil && !now.Before(*a.ExpiresAt) {
				effectiveStatus = execution.ApprovalExpired
				expired = true
			}
			if !includeClosed && effectiveStatus != execution.ApprovalRequested && !expired {
				continue
			}
			ident := a.Identity()
			authority, destination, command, ownerNotice, marshalNotice := ApprovalDestinationInfo(a)
			views = append(views, ApprovalNoticeView{
				ApprovalID:     ident.ApprovalID,
				RunID:          ident.RunID,
				MarshalRunID:   marshalRunID,
				TaskID:         ident.TaskID,
				Attempt:        ident.Attempt,
				ScopeDigest:    ident.ScopeDigest,
				Authority:      authority,
				CreatedAt:      ident.CreatedAt,
				ExpiresAt:      ident.ExpiresAt,
				OperationType:  a.OperationType,
				TargetResource: RedactApprovalText(a.TargetResource),
				Status:         effectiveStatus,
				Expired:        expired,
				ReplacementID:  a.ReplacementID,
				Revoked:        a.Revoked,
				RevokedReason:  RedactApprovalText(a.RevokedReason),
				ApprovedBy:     a.ApprovedBy,
				DecisionReason: RedactApprovalText(a.DecisionReason),
				Destination:    destination,
				Command:        command,
				WhoCanAnswer:   authority,
				OwnerNotice:    ownerNotice,
				MarshalNotice:  marshalNotice,
			})
		}
	}
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].CreatedAt.Equal(views[j].CreatedAt) {
			return views[i].ApprovalID < views[j].ApprovalID
		}
		return views[i].CreatedAt.Before(views[j].CreatedAt)
	})
	notices, err := r.store.ListNotices(ctx, r.ProjectIdentity(), marshalRunID, 100)
	if err != nil {
		return views, nil, err
	}
	for i := range notices {
		notices[i].Command = RedactApprovalText(notices[i].Command)
		notices[i].Summary = RedactApprovalText(notices[i].Summary)
		notices[i].Destination = RedactApprovalText(notices[i].Destination)
		notices[i].PayloadJSON = ""
	}
	return views, notices, nil
}

func (r *Runtime) boundApprovalIDs(ctx context.Context, marshalRunID string, run marshal.Run) map[string]bool {
	bound := map[string]bool{}
	for _, t := range run.Tasks {
		if t.Process05WaitRunID != "" {
			bound[t.Process05WaitApprovalID] = true
		}
	}
	if r == nil || r.Execution() == nil || r.Execution().Engine() == nil {
		return bound
	}
	all, err := r.Execution().Engine().ApprovalManager().ListApprovals()
	if err != nil {
		return bound
	}
	for _, a := range all {
		if binding, err := r.store.ApprovalNoticeRun(ctx, r.ProjectIdentity(), a.ApprovalID); err == nil && binding == marshalRunID {
			bound[a.ApprovalID] = true
		}
	}
	return bound
}

func approvalMatchesMarshalRun(a execution.RuntimeApproval, run marshal.Run, boundApprovals map[string]bool) bool {
	if boundApprovals[a.ApprovalID] {
		return true
	}

	for _, t := range run.Tasks {
		if t.Process05WaitApprovalID != "" && t.Process05WaitApprovalID == a.ApprovalID {
			return true
		}
	}
	return false
}

// resolveMarshalRunIDForExecution finds the Marshal run bound to an execution
// run's exact wait binding, falling back to the execution run ID if ambiguous.
func (r *Runtime) resolveMarshalRunIDForExecution(ctx context.Context, execRunID, _ string, _ int64) string {
	if r == nil || r.store == nil {
		return execRunID
	}
	bare := r.Marshal()
	if bare == nil || bare.ProjectID == "" {
		return execRunID
	}
	runs, err := r.store.MarshalRuns(ctx, bare.ProjectID)
	if err != nil {
		return execRunID
	}
	binding := ""
	for id, rec := range runs {
		for _, t := range rec.Value.Tasks {
			if t.Process05WaitRunID == execRunID {
				if binding != "" && binding != id {
					return execRunID
				}
				binding = id
			}
		}
	}
	if binding != "" {
		return binding
	}
	return execRunID
}
