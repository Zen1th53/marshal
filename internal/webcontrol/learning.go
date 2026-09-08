package webcontrol

import (
	"net/http"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
)

// handleGetMemoryCommit returns one canonical Process 07 memory commit.
//
// The read verifies the stored digest, so a commit altered in the database is
// reported as tampered rather than served as canonical.
func (s *Server) handleGetMemoryCommit(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "learning_unavailable", "canonical learning store unavailable", GetCorrelationID(r.Context()))
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_memory_commit", "memory commit ID is required", GetCorrelationID(r.Context()))
		return
	}
	record, err := s.store.GetMemoryCommit(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "memory_commit_not_found", "canonical memory commit not found", GetCorrelationID(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, record)
}

// handleSearchLearningMemory returns bounded Process 07 memory.
//
// Each result keeps its claim state, freshness, evidence-cluster count and
// contradiction signal, so a caller cannot receive a claim stripped of the
// uncertainty attached to it. Stale memory appears only when asked for, and
// never as usable.
func (s *Server) handleSearchLearningMemory(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "learning_unavailable", "canonical learning store unavailable", GetCorrelationID(r.Context()))
		return
	}
	query := learning.Query{
		ProjectID:      strings.TrimSpace(r.URL.Query().Get("project_id")),
		IncludeGeneral: r.URL.Query().Get("include_general") == "true",
		IncludeStale:   r.URL.Query().Get("include_stale") == "true",
	}
	if terms := strings.TrimSpace(r.URL.Query().Get("terms")); terms != "" {
		query.Terms = strings.Split(terms, ",")
	}
	if err := learning.ValidateQuery(query); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_memory_query", "memory query must be bounded to a project or general scope", GetCorrelationID(r.Context()))
		return
	}
	items, err := s.store.ListMemoryItems(r.Context(), query.ProjectID, query.IncludeGeneral)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "memory_read_failed", "canonical memory could not be read", GetCorrelationID(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, learning.Retrieve(items, query, time.Now().UTC()))
}

// handleGetMemoryProvenance returns one item's full version history, so a
// revision never erases what was believed before it.
func (s *Server) handleGetMemoryProvenance(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "learning_unavailable", "canonical learning store unavailable", GetCorrelationID(r.Context()))
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_memory_item", "memory item ID is required", GetCorrelationID(r.Context()))
		return
	}
	history, err := s.store.ItemRevisions(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "memory_read_failed", "memory history could not be read", GetCorrelationID(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, history)
}

// handleGetRoutingTrust returns measured routing outcomes.
//
// Aggregation covers failures, blocked runs and unselected routes, and carries
// the selection-bias flag, so a provider handed only easy work cannot read as
// universally strong.
func (s *Server) handleGetRoutingTrust(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "learning_unavailable", "canonical learning store unavailable", GetCorrelationID(r.Context()))
		return
	}
	observations, err := s.store.RoutingObservations(r.Context(), strings.TrimSpace(r.URL.Query().Get("task_class")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "routing_read_failed", "routing observations could not be read", GetCorrelationID(r.Context()))
		return
	}
	// The map is keyed by a struct, so it is returned as a list to stay valid
	// JSON without flattening the key into an ambiguous string.
	trust := learning.AggregateTrust(observations)
	out := make([]learning.Trust, 0, len(trust))
	for _, record := range trust {
		out = append(out, record)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleListPlaybookCandidates returns candidate procedures. A candidate is
// never active: activation is a governed decision made elsewhere.
func (s *Server) handleListPlaybookCandidates(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "learning_unavailable", "canonical learning store unavailable", GetCorrelationID(r.Context()))
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if project == "" {
		writeError(w, http.StatusBadRequest, "invalid_project", "project_id is required", GetCorrelationID(r.Context()))
		return
	}
	candidates, err := s.store.PlaybookCandidates(r.Context(), project)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "playbook_read_failed", "playbook candidates could not be read", GetCorrelationID(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, candidates)
}

// handleListFailureFingerprints returns bounded failure fingerprints.
func (s *Server) handleListFailureFingerprints(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "learning_unavailable", "canonical learning store unavailable", GetCorrelationID(r.Context()))
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if project == "" {
		writeError(w, http.StatusBadRequest, "invalid_project", "project_id is required", GetCorrelationID(r.Context()))
		return
	}
	prints, err := s.store.FailureFingerprints(r.Context(), project)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fingerprint_read_failed", "failure fingerprints could not be read", GetCorrelationID(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, prints)
}

// handleListReplayIndex returns the reproducibility index.
func (s *Server) handleListReplayIndex(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "learning_unavailable", "canonical learning store unavailable", GetCorrelationID(r.Context()))
		return
	}
	records, err := s.store.ReplayRecords(r.Context(), strings.TrimSpace(r.URL.Query().Get("run_id")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "replay_read_failed", "replay index could not be read", GetCorrelationID(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, records)
}

// handleListBenchmarkRecords returns reproducible evaluation records.
func (s *Server) handleListBenchmarkRecords(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "learning_unavailable", "canonical learning store unavailable", GetCorrelationID(r.Context()))
		return
	}
	records, err := s.store.BenchmarkRecords(r.Context(), strings.TrimSpace(r.URL.Query().Get("benchmark")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "benchmark_read_failed", "benchmark records could not be read", GetCorrelationID(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, records)
}
