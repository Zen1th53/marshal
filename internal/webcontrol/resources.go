package webcontrol

import (
	"net/http"

	"github.com/Zen1th53/marshal/internal/resources"
)

// handleGetResources exposes only a bounded, point-in-time local snapshot.
// It never returns environment variables, device serials, or a control action.
func (s *Server) handleGetResources(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "resources_unavailable", "runtime-backed resources are unavailable", GetCorrelationID(r.Context()))
		return
	}
	project, err := s.store.Project(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "resources_unavailable", "resource inventory is unavailable", GetCorrelationID(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, resources.NewCollector().Collect(r.Context(), project.Repository))
}
