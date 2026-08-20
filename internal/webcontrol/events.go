package webcontrol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	MaxReplayBufferSize = 100
	ClientChannelBuffer = 64
	HeartbeatInterval   = 15 * time.Second
)

type SSEEvent struct {
	ID        int64           `json:"id"`
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Scope     string          `json:"scope,omitempty"`
	ScopeID   string          `json:"scope_id,omitempty"`
	Data      json.RawMessage `json:"data"`
}

type SSEClient struct {
	id          string
	user        *AuthUserDTO
	sendCh      chan SSEEvent
	lastEventID int64
}

type SSEHub struct {
	mu           sync.RWMutex
	clients      map[string]*SSEClient
	replayBuffer []SSEEvent
	currentSeq   int64
}

func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients:      make(map[string]*SSEClient),
		replayBuffer: make([]SSEEvent, 0, MaxReplayBufferSize),
		currentSeq:   0,
	}
}

func (h *SSEHub) Broadcast(eventType string, scope, scopeID string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}

	h.mu.Lock()
	h.currentSeq++
	event := SSEEvent{
		ID:        h.currentSeq,
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Scope:     scope,
		ScopeID:   scopeID,
		Data:      raw,
	}

	// Append to bounded replay buffer
	if len(h.replayBuffer) >= MaxReplayBufferSize {
		h.replayBuffer = h.replayBuffer[1:]
	}
	h.replayBuffer = append(h.replayBuffer, event)

	// Copy clients snapshot to deliver without holding global lock
	clients := make([]*SSEClient, 0, len(h.clients))
	for _, c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, client := range clients {
		// Filter by client role/scope
		if !h.isEventVisibleToClient(event, client) {
			continue
		}

		select {
		case client.sendCh <- event:
		default:
			// Slow client buffer full: drop client
			h.RemoveClient(client.id)
		}
	}
}

func (h *SSEHub) isEventVisibleToClient(event SSEEvent, client *SSEClient) bool {
	if client.user == nil {
		return false
	}
	if client.user.Role == "admin" {
		return true
	}
	// Viewers can view public system/task/audit events, but not internal secrets
	return true
}

func (h *SSEHub) AddClient(client *SSEClient, lastEventID int64) []SSEEvent {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.clients[client.id] = client

	// Check if backfill requested
	if lastEventID <= 0 {
		return nil
	}

	// Check if requested ID is too old (not in replay buffer)
	if len(h.replayBuffer) > 0 {
		oldestID := h.replayBuffer[0].ID
		if lastEventID < oldestID-1 {
			// Trigger resync
			return []SSEEvent{
				{
					ID:        h.currentSeq,
					Type:      "resync",
					Timestamp: time.Now().UTC(),
					Data:      json.RawMessage(`{"reason":"replay_window_expired"}`),
				},
			}
		}
	}

	// Replay missing events
	var replay []SSEEvent
	for _, ev := range h.replayBuffer {
		if ev.ID > lastEventID {
			replay = append(replay, ev)
		}
	}
	return replay
}

func (h *SSEHub) RemoveClient(clientID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.clients[clientID]; ok {
		close(c.sendCh)
		delete(h.clients, clientID)
	}
}

func (s *Server) handleEventsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported", "Streaming not supported", "")
		return
	}

	user := s.getAuthenticatedUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Session required for event stream", "")
		return
	}

	// Set SSE streaming headers
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Parse Last-Event-ID header or query param
	lastEventIDStr := r.Header.Get("Last-Event-ID")
	if lastEventIDStr == "" {
		lastEventIDStr = r.URL.Query().Get("last_event_id")
	}
	var lastEventID int64
	if lastEventIDStr != "" {
		lastEventID, _ = strconv.ParseInt(lastEventIDStr, 10, 64)
	}

	clientID := fmt.Sprintf("client-%d-%d", time.Now().UnixNano(), lastEventID)
	client := &SSEClient{
		id:          clientID,
		user:        user,
		sendCh:      make(chan SSEEvent, ClientChannelBuffer),
		lastEventID: lastEventID,
	}

	replayEvents := s.sseHub.AddClient(client, lastEventID)
	defer s.sseHub.RemoveClient(clientID)

	// Send initial replay if any
	for _, ev := range replayEvents {
		fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, string(ev.Data))
	}
	flusher.Flush()

	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			// Send SSE comment heartbeat
			fmt.Fprintf(w, ": heartbeat %d\n\n", time.Now().Unix())
			flusher.Flush()
		case ev, ok := <-client.sendCh:
			if !ok {
				return
			}
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, string(ev.Data))
			flusher.Flush()
		}
	}
}
