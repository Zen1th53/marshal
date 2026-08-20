package webcontrol

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ReviewQueueItemDTO struct {
	TaskID         string    `json:"task_id"`
	Title          string    `json:"title"`
	Stage          string    `json:"stage"` // "plan_review", "gate_review", "merge_approval"
	Risk           string    `json:"risk"`
	Owner          string    `json:"owner"`
	BaseCommit     string    `json:"base_commit"`
	HeadCommit     string    `json:"head_commit"`
	IsStaleHead    bool      `json:"is_stale_head"`
	ApprovalsCount int       `json:"approvals_count"`
	RequiredQuorum int       `json:"required_quorum"`
	BlockerCount   int       `json:"blocker_count"`
	SubmittedAt    time.Time `json:"submitted_at"`
}

type ReviewQueueResponseDTO struct {
	Items      []ReviewQueueItemDTO `json:"items"`
	TotalCount int                  `json:"total_count"`
	Limit      int                  `json:"limit"`
	Offset     int                  `json:"offset"`
}

var mockReviewQueue = []ReviewQueueItemDTO{
	{
		TaskID:         "TASK-002-CONTROL-PLANE",
		Title:          "Mission Control Web Plane Implementation",
		Stage:          "gate_review",
		Risk:           "CRITICAL",
		Owner:          "agent-codex-implementer",
		BaseCommit:     "e174534",
		HeadCommit:     "29c3643",
		IsStaleHead:    false,
		ApprovalsCount: 1,
		RequiredQuorum: 2,
		BlockerCount:   0,
		SubmittedAt:    time.Now().UTC().Add(-30 * time.Minute),
	},
	{
		TaskID:         "TASK-003-SECURITY-AUDIT",
		Title:          "Merkle Evidence Attestation & Quorum Validation",
		Stage:          "merge_approval",
		Risk:           "HIGH",
		Owner:          "agent-gemini-multimodal",
		BaseCommit:     "3db9f8b",
		HeadCommit:     "7d17fb8",
		IsStaleHead:    false,
		ApprovalsCount: 2,
		RequiredQuorum: 2,
		BlockerCount:   0,
		SubmittedAt:    time.Now().UTC().Add(-1 * time.Hour),
	},
	{
		TaskID:         "TASK-004-BENCHMARKS",
		Title:          "Latency & Conformance Suite Baseline",
		Stage:          "plan_review",
		Risk:           "MEDIUM",
		Owner:          "agent-opencode-local",
		BaseCommit:     "1b29175",
		HeadCommit:     "1b29175",
		IsStaleHead:    true, // Out of date base
		ApprovalsCount: 0,
		RequiredQuorum: 1,
		BlockerCount:   1,
		SubmittedAt:    time.Now().UTC().Add(-4 * time.Hour),
	},
}

func (s *Server) handleGetReviewQueue(w http.ResponseWriter, r *http.Request) {
	stageFilter := strings.TrimSpace(r.URL.Query().Get("stage"))
	riskFilter := strings.TrimSpace(r.URL.Query().Get("risk"))
	ownerFilter := strings.TrimSpace(r.URL.Query().Get("owner"))

	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	offset := 0
	if oStr := r.URL.Query().Get("offset"); oStr != "" {
		if o, err := strconv.Atoi(oStr); err == nil && o >= 0 {
			offset = o
		}
	}

	var filtered []ReviewQueueItemDTO
	for _, item := range mockReviewQueue {
		if stageFilter != "" && stageFilter != "all" && item.Stage != stageFilter {
			continue
		}
		if riskFilter != "" && riskFilter != "all" && item.Risk != riskFilter {
			continue
		}
		if ownerFilter != "" && item.Owner != ownerFilter {
			continue
		}
		filtered = append(filtered, item)
	}

	total := len(filtered)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	paged := filtered[start:end]
	writeJSON(w, http.StatusOK, ReviewQueueResponseDTO{
		Items:      paged,
		TotalCount: total,
		Limit:      limit,
		Offset:     offset,
	})
}
