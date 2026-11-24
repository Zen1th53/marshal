//go:build !governed

package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

// maxLeaseLimitNotices bounds the limit notices one live lease records; a
// repeat of one already recorded is acknowledged without counting.
const maxLeaseLimitNotices = 32

// innerLimit is the refusal of a submission whose inner proposal exceeds
// its method's cap. base keeps the refusal's existing text and code.
func innerLimit(kind string, actual int, base error) error {
	limit := model.Limit{Kind: kind, Subject: "proposal", ActualBytes: int64(actual), CapBytes: proposalmcp.MaxProposalBytes, Action: proposalmcp.ProposalLimitAction}
	if kind == proposalmcp.LimitKindContract {
		limit.CapBytes, limit.Action = proposalmcp.MaxContractProposalBytes, proposalmcp.ContractLimitAction
	}
	return model.NewLimitError(base, limit)
}

// noteInnerLimit records the notice of a submission the live handler
// refused at its inner cap. It runs after the broker's mutex is released
// and before the refusal is answered.
func (w *Workspace) noteInnerLimit(ctx context.Context, l *proposalMCPLease, method string, raw json.RawMessage, runID string, err error) {
	limit, ok := model.LimitOf(err)
	if !ok || (limit.Kind != proposalmcp.LimitKindProposal && limit.Kind != proposalmcp.LimitKindContract) {
		return
	}
	var args struct {
		SubmissionID string `json:"submission_id"`
	}
	_ = json.Unmarshal(raw, &args)
	if !validProposalID(args.SubmissionID) {
		return
	}
	if _, err := w.recordLimitNotice(ctx, l, method, args.SubmissionID, runID, limit); err != nil {
		w.logTUIError("limit notice", err)
	}
}

// handleLimitNotice serves a limit notice: the live Marshal's MCP child
// refused a submission whose encoded request would not fit the frame, and
// tells the Board without sending it. The notice is checked like any
// request (capability and namespace by the transport, the live lease here),
// carries only the method, the submission ID or a correlation token and the
// frame size, and is recorded durably before it is acknowledged. It admits
// nothing and changes nothing in the project.
func (w *Workspace) handleLimitNotice(ctx context.Context, l *proposalMCPLease, raw json.RawMessage) (json.RawMessage, error) {
	runID := w.currentRunID()
	w.proposalMCP.mu.Lock()
	if err := w.validateProposalLease(l); err != nil {
		w.proposalMCP.mu.Unlock()
		return nil, err
	}
	w.proposalMCP.mu.Unlock()
	if len(raw) > proposalmcp.MaxNoticeFrameBytes {
		return nil, proposalParseRefusal(errors.New("limit notice too large"))
	}
	var n proposalmcp.LimitNotice
	if err := proposalmcp.DecodeStrict(raw, &n); err != nil {
		return nil, proposalParseRefusal(errors.New("invalid limit notice"))
	}
	if err := n.Validate(); err != nil {
		return nil, proposalParseRefusal(err)
	}
	ident := n.SubmissionID
	if ident == "" {
		ident = "correlation:" + n.Correlation
	}
	created, err := w.recordLimitNotice(ctx, l, n.Method, ident, runID, proposalmcp.FrameLimit(n.FrameBytes))
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Recorded  bool `json:"recorded"`
		Duplicate bool `json:"duplicate,omitempty"`
	}{true, !created})
}

// limitNoticeID is the stable event ID of one refused submission: the same
// project, launch, run, submission, method and cap give the same notice,
// whichever transport reported it.
func (w *Workspace) limitNoticeID(l *proposalMCPLease, method, ident, runID, kind string) string {
	sum := sha256.Sum256([]byte(w.projectID + "\x00" + l.id + "\x00" + runID + "\x00" + ident + "\x00" + method + "\x00" + kind))
	return "limit:" + hex.EncodeToString(sum[:16])
}

// recordLimitNotice records one refused submission's notice, at most once
// per event ID and within the lease's notice bound, then shows it on the
// Boards' Activity and delivers it to the Marshal chat. It reports whether
// this call recorded it.
func (w *Workspace) recordLimitNotice(ctx context.Context, l *proposalMCPLease, method, ident, runID string, limit model.Limit) (bool, error) {
	if w.runtime == nil {
		return false, model.ErrUnavailable
	}
	id := w.limitNoticeID(l, method, ident, runID, limit.Kind)
	w.proposalMCP.mu.Lock()
	seen := l.limitNotices[id]
	full := !seen && len(l.limitNotices) >= maxLeaseLimitNotices
	w.proposalMCP.mu.Unlock()
	if seen {
		return false, nil
	}
	if full {
		return false, proposalParseRefusal(errors.New("too many limit notices from this chat"))
	}
	summary := fmt.Sprintf("A Marshal %s was refused before it was admitted: the %s is %d bytes, more than the %d-byte limit.", limitSubmissionWhat(method), limit.Subject, limit.ActualBytes, limit.CapBytes)
	created, err := w.runtime.RecordLimitNotice(ctx, app.RunNotice{EventID: id, RunID: runID, Code: marshal.CodeLimitExceeded, Summary: summary, Next: limit.Action, Limit: &limit})
	if err != nil {
		return false, err
	}
	w.proposalMCP.mu.Lock()
	if l.limitNotices == nil {
		l.limitNotices = map[string]bool{}
	}
	l.limitNotices[id] = true
	w.proposalMCP.mu.Unlock()
	if created {
		w.RecordActivity(summary + " " + limit.Action)
		w.replayMarshalNotices(ctx)
	}
	return created, nil
}

func limitSubmissionWhat(method string) string {
	switch method {
	case "project_contract_submit":
		return "project contract submission"
	case "memory_submit":
		return "memory submission"
	case "design_review":
		return "design review request"
	case "architecture_request_answer":
		return "architecture answer"
	}
	return "proposal submission"
}
