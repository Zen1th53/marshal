package webcontrol

import (
	"net/http"
	"strings"
)

func (s *Server) optimizationCycleID(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "optimization_unavailable", "canonical optimization store unavailable", GetCorrelationID(r.Context()))
		return "", false
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_optimization_cycle", "optimization cycle ID is required", GetCorrelationID(r.Context()))
		return "", false
	}
	return id, true
}

func (s *Server) handleGetOptimizationCycle(w http.ResponseWriter, r *http.Request) {
	id, ok := s.optimizationCycleID(w, r)
	if !ok {
		return
	}
	cycle, err := s.store.GetOptimizationCycle(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "optimization_cycle_not_found", "canonical optimization cycle not found", GetCorrelationID(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, cycle)
}

func (s *Server) handleOptimizationCandidates(w http.ResponseWriter, r *http.Request) {
	id, ok := s.optimizationCycleID(w, r)
	if !ok {
		return
	}
	items, err := s.store.OptimizationCandidates(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "optimization_read_failed", "optimization candidates could not be read", GetCorrelationID(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleOptimizationCounterfactuals(w http.ResponseWriter, r *http.Request) {
	id, ok := s.optimizationCycleID(w, r)
	if !ok {
		return
	}
	items, err := s.store.Counterfactuals(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "optimization_read_failed", "counterfactual evidence could not be read", GetCorrelationID(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleOptimizationManifests(w http.ResponseWriter, r *http.Request) {
	id, ok := s.optimizationCycleID(w, r)
	if !ok {
		return
	}
	items, err := s.store.BenchmarkManifests(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "optimization_read_failed", "benchmark manifests could not be read", GetCorrelationID(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleOptimizationCanaries(w http.ResponseWriter, r *http.Request) {
	id, ok := s.optimizationCycleID(w, r)
	if !ok {
		return
	}
	items, err := s.store.Canaries(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "optimization_read_failed", "canary records could not be read", GetCorrelationID(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, items)
}
