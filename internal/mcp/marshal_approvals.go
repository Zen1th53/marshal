package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// marshalApprovalsResponse is the read-only, run-scoped approval view pulled
// by the Marshal chat. It never mutates approval receipts or run state.
type marshalApprovalsResponse struct {
	RunID     string                   `json:"run_id,omitempty"`
	NoRun     bool                     `json:"no_run,omitempty"`
	Approvals []app.ApprovalNoticeView `json:"approvals"`
	Notices   []store.NoticeOutboxEntry `json:"notices,omitempty"`
}

// SetBoundRunID pins this MCP server to a single Marshal run for run-scoped
// read tools such as marshal_approvals.
func (s *Server) SetBoundRunID(runID string) {
	s.boundRunID = runID
}

func (s *Server) marshalApprovals(ctx context.Context, args map[string]any) (marshalApprovalsResponse, error) {
	out := marshalApprovalsResponse{Approvals: []app.ApprovalNoticeView{}}
	bare := s.runtime.Marshal()
	if bare == nil || bare.ProjectID == "" {
		out.NoRun = true
		return out, nil
	}
	requestedRunID := stringArg(args, "run_id")
	includeClosed := boolArg(args, "include_closed")

	var runID string
	var run marshal.Run
	if s.boundRunID != "" {
		if requestedRunID != "" && requestedRunID != s.boundRunID {
			return out, fmt.Errorf("%w: marshal_approvals is scoped to bound run %s", model.ErrPolicyDenied, s.boundRunID)
		}
		rec, err := s.runtime.Store().GetMarshalRun(ctx, bare.ProjectID, s.boundRunID)
		if errors.Is(err, model.ErrNotFound) {
			out.NoRun = true
			return out, nil
		}
		if err != nil {
			return out, err
		}
		runID, run = s.boundRunID, rec.Value
	} else {
		latestID, rec, err := s.runtime.Store().LatestMarshalRun(ctx, bare.ProjectID)
		if errors.Is(err, model.ErrNotFound) {
			out.NoRun = true
			return out, nil
		}
		if err != nil {
			return out, err
		}
		if requestedRunID != "" && requestedRunID != latestID {
			return out, fmt.Errorf("%w: marshal_approvals is scoped to active run %s", model.ErrPolicyDenied, latestID)
		}
		runID, run = latestID, rec.Value
	}

	views, notices, err := s.runtime.ApprovalProjection(ctx, runID, run, includeClosed)
	if err != nil {
		return out, err
	}
	out.RunID = runID
	if views != nil {
		out.Approvals = views
	}
	out.Notices = notices
	return out, nil
}
