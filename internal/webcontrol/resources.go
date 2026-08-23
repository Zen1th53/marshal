package webcontrol

import (
	"net/http"

	"github.com/Zen1th53/marshal/internal/resources"
)

// handleGetResources exposes only a bounded, point-in-time local snapshot.
// It never returns environment variables, device serials, or a control action.
func (s *Server) handleGetResources(w http.ResponseWriter, r *http.Request) {
	path := "."
	if s.store != nil {
		if project, err := s.store.Project(r.Context()); err == nil && project.Repository != "" {
			path = project.Repository
		}
	}
	writeJSON(w, http.StatusOK, resources.NewCollector().Collect(r.Context(), path))
}
