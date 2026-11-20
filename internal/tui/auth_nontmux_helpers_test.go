package tui

import "github.com/Zen1th53/marshal/internal/model"

// authNontmuxParticipant returns the workspace participant for agentID.
func authNontmuxParticipant(ws *Workspace, agentID string) (model.Participant, bool) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	for _, p := range ws.state.Participants {
		if p.AgentID == agentID {
			return p, true
		}
	}
	return model.Participant{}, false
}
