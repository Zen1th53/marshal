//go:build !governed

package tui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

type designReviewJob struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	state  string
	result app.DesignReviewResult
	reason string
}

// Caller holds proposalMCP.mu. Long reviews run outside that lock and outlive
// the 30-second relay call; repeating the same request polls its receipt.
func (w *Workspace) designReviewMCP(l *proposalMCPLease, raw json.RawMessage) (json.RawMessage, error) {
	var req app.DesignReviewRequest
	if err := proposalmcp.DecodeStrict(raw, &req); err != nil {
		return nil, err
	}
	if err := req.Validate(l.provider); err != nil {
		return nil, err
	}
	canonical, _ := json.Marshal(req)
	key := fmt.Sprintf("%x", sha256.Sum256(canonical))
	if l.designReviews == nil {
		l.designReviews = map[string]*designReviewJob{}
	}
	job := l.designReviews[key]
	if job == nil {
		if len(l.designReviews) >= 8 {
			return nil, fmt.Errorf("design review limit reached for this launch")
		}
		ctx, cancel := context.WithCancel(w.workers.context())
		job = &designReviewJob{state: "pending", cancel: cancel}
		l.designReviews[key] = job
		if !w.workers.start(ctx, nil, func(ctx context.Context) {
			defer cancel()
			result, err := w.runtime.ReviewDesign(ctx, l.provider, req)
			job.mu.Lock()
			defer job.mu.Unlock()
			if err != nil {
				job.state = "failed"
				job.reason = err.Error()
			} else {
				job.state = "completed"
				job.result = result
			}
		}) {
			cancel()
			job.state, job.reason = "failed", "workspace closed"
		}
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	return json.Marshal(struct {
		State  string                 `json:"state"`
		Result app.DesignReviewResult `json:"result"`
		Reason string                 `json:"reason,omitempty"`
	}{job.state, job.result, job.reason})
}
