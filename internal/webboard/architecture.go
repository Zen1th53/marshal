package webboard

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/Zen1th53/marshal/internal/architectureworkspace"
)

type ArchitectureSource func(context.Context) (architectureworkspace.Snapshot, error)
type architectureSource struct{ read ArchitectureSource }

func (s *Server) ConfigureArchitecture(source ArchitectureSource) {
	s.architecture.Store(&architectureSource{source})
}
func (s *Server) serveArchitecture(w http.ResponseWriter, q *http.Request) {
	if origin := q.Header.Get("Origin"); origin != "" && !s.originAllowed(origin) {
		deny(w, http.StatusForbidden, "Open the Board from its own origin.")
		return
	}
	source := s.architecture.Load()
	if source == nil || source.read == nil {
		deny(w, 503, "No accepted architecture is available.")
		return
	}
	snapshot, err := source.read(q.Context())
	if err != nil {
		deny(w, 503, "The accepted architecture could not be read.")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(snapshot)
}

type architecturePointer = atomic.Pointer[architectureSource]
