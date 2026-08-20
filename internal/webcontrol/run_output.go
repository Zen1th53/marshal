package webcontrol

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

type LogLineDTO struct {
	Index     int       `json:"index"`
	Timestamp time.Time `json:"timestamp"`
	Stream    string    `json:"stream"` // "stdout", "stderr", "system"
	Message   string    `json:"message"`
}

type RunLogsResponseDTO struct {
	RunID       string       `json:"run_id"`
	Lines       []LogLineDTO `json:"lines"`
	TotalLines  int          `json:"total_lines"`
	IsTruncated bool         `json:"is_truncated"`
	NextCursor  int          `json:"next_cursor"`
}

type RunDetailComprehensiveDTO struct {
	RunID         string          `json:"run_id"`
	TaskID        string          `json:"task_id"`
	AgentID       string          `json:"agent_id"`
	Provider      string          `json:"provider"`
	Status        string          `json:"status"`
	DurationMs    int64           `json:"duration_ms"`
	StepCount     int             `json:"step_count"`
	EvidenceCount int             `json:"evidence_count"`
	BaseCommit    string          `json:"base_commit"`
	HeadCommit    string          `json:"head_commit"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
	CorrelationID string          `json:"correlation_id"`
	Summary       string          `json:"summary"`
	Logs          []LogLineDTO    `json:"logs"`
}

func sanitizeLogMessage(msg string) string {
	// Strip ANSI escape codes
	return ansiRegex.ReplaceAllString(msg, "")
}

func (s *Server) handleGetRunDetail(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Run ID is required", "")
		return
	}

	for _, item := range mockRunsList {
		if item.RunID == runID {
			logs := generateMockLogs(runID)
			writeJSON(w, http.StatusOK, RunDetailComprehensiveDTO{
				RunID:         item.RunID,
				TaskID:        item.TaskID,
				AgentID:       item.AgentID,
				Provider:      item.Provider,
				Status:        item.Status,
				DurationMs:    item.DurationMs,
				StepCount:     item.StepCount,
				EvidenceCount: item.EvidenceCount,
				BaseCommit:    item.BaseCommit,
				HeadCommit:    item.HeadCommit,
				StartedAt:     item.StartedAt,
				FinishedAt:    item.FinishedAt,
				CorrelationID: "req-run-" + item.RunID,
				Summary:       "Autonomous worker step execution for " + item.TaskID,
				Logs:          logs,
			})
			return
		}
	}

	writeError(w, http.StatusNotFound, "run_not_found", "Run not found: "+runID, "")
}

func (s *Server) handleGetRunLogs(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Run ID is required", "")
		return
	}

	cursor := 0
	if cStr := r.URL.Query().Get("cursor"); cStr != "" {
		if c, err := strconv.Atoi(cStr); err == nil && c >= 0 {
			cursor = c
		}
	}

	limit := 100
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 && l <= 500 {
			limit = l
		}
	}

	allLogs := generateMockLogs(runID)
	total := len(allLogs)

	start := cursor
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	paged := allLogs[start:end]
	writeJSON(w, http.StatusOK, RunLogsResponseDTO{
		RunID:       runID,
		Lines:       paged,
		TotalLines:  total,
		IsTruncated: total > 500,
		NextCursor:  end,
	})
}

func generateMockLogs(runID string) []LogLineDTO {
	baseTime := time.Now().UTC().Add(-30 * time.Minute)
	rawLines := []struct {
		stream string
		msg    string
	}{
		{"system", fmt.Sprintf("Initializing sandbox execution cell for run %s", runID)},
		{"stdout", "[orchestrator] Attached to workspace /home/zen1th53/Desktop/codex/marshal"},
		{"stdout", "[memory] Loaded working memory context: 4 blocks active"},
		{"stdout", "[planner] Beginning multi-step plan verification..."},
		{"stdout", "[worker] Compiling verification gate: go test ./..."},
		{"stdout", "=== RUN   TestT188RunsExplorer\n--- PASS: TestT188RunsExplorer (0.00s)\nPASS"},
		{"system", "Step 12 completed successfully in 4250ms with 0 violations."},
	}

	res := make([]LogLineDTO, len(rawLines))
	for i, l := range rawLines {
		res[i] = LogLineDTO{
			Index:     i + 1,
			Timestamp: baseTime.Add(time.Duration(i*500) * time.Millisecond),
			Stream:    l.stream,
			Message:   sanitizeLogMessage(l.msg),
		}
	}
	return res
}
