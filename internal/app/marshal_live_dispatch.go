package app

import "sync"

// marshalLiveDispatches records the Marshal workers this process launched and
// has not yet collected. A MarshalService is rebuilt per command, so the
// registry lives on the Runtime (or, for a service without one, in a package
// fallback). Resume uses it to tell a worker that is still running in
// parallel apart from one a restart really interrupted.
var marshalLiveFallback sync.Map

func (s *MarshalService) liveDispatches() *sync.Map {
	if s != nil && s.runtime != nil {
		return &s.runtime.marshalLive
	}
	return &marshalLiveFallback
}

func marshalLiveKey(runID, taskID string) string { return runID + "\x00" + taskID }

func (s *MarshalService) markDispatchLive(runID string, launched []MarshalDispatch) {
	for _, d := range launched {
		s.liveDispatches().Store(marshalLiveKey(runID, d.TaskID), true)
	}
}

func (s *MarshalService) clearDispatchLive(runID, taskID string) {
	s.liveDispatches().Delete(marshalLiveKey(runID, taskID))
}

func (s *MarshalService) dispatchLive(runID, taskID string) bool {
	_, ok := s.liveDispatches().Load(marshalLiveKey(runID, taskID))
	return ok
}
