package working

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrCASConflict     = errors.New("working memory CAS revision conflict")
	ErrSlotNotFound    = errors.New("working memory slot not found")
	ErrCeilingExceeded = errors.New("working memory byte ceiling exceeded")
)

type SlotType string

const (
	SlotHypothesis            SlotType = "hypothesis"
	SlotPlanState             SlotType = "plan_state"
	SlotActiveSymbols         SlotType = "active_symbols"
	SlotBlockers              SlotType = "blockers"
	SlotTemporaryObservations SlotType = "temporary_observations"
	SlotToolResults           SlotType = "tool_results"
	SlotFinding               SlotType = "finding"
	SlotDecision              SlotType = "decision"
	SlotConstraint            SlotType = "constraint"
	SlotFailedApproach        SlotType = "failed_approach"
	SlotArtifactReference     SlotType = "artifact_reference"
	SlotOpenQuestion          SlotType = "open_question"
	SlotHandoffNote           SlotType = "handoff_note"
)

type WorkingSlot struct {
	Type        SlotType  `json:"type"`
	Value       string    `json:"value"`
	Revision    int       `json:"revision"`
	Pinned      bool      `json:"pinned"`
	LastAgentID string    `json:"last_agent_id"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Config struct {
	MaxEntriesPerScope int
	MaxBytesPerScope   int
}

type Manager struct {
	config Config
	mu     sync.RWMutex
	// taskID -> map[SlotType]WorkingSlot
	sharedSlots map[string]map[SlotType]WorkingSlot
	// taskID:agentID -> map[string]string
	privateSlots map[string]map[string]string
}

func NewManager(cfg Config) *Manager {
	if cfg.MaxEntriesPerScope <= 0 {
		cfg.MaxEntriesPerScope = 50
	}
	if cfg.MaxBytesPerScope <= 0 {
		cfg.MaxBytesPerScope = 64 * 1024 // 64KB
	}
	return &Manager{
		config:       cfg,
		sharedSlots:  make(map[string]map[SlotType]WorkingSlot),
		privateSlots: make(map[string]map[string]string),
	}
}

// SetSlot writes or overwrites a shared task working memory slot.
func (m *Manager) SetSlot(ctx context.Context, taskID, agentID string, slotType SlotType, value string, pinned bool) (WorkingSlot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	slots, ok := m.sharedSlots[taskID]
	if !ok {
		slots = make(map[SlotType]WorkingSlot)
		m.sharedSlots[taskID] = slots
	}

	// Check entry ceiling and evict oldest unpinned if needed
	if len(slots) >= m.config.MaxEntriesPerScope {
		var oldestKey SlotType
		var oldestTime time.Time
		for k, s := range slots {
			if !s.Pinned && (oldestTime.IsZero() || s.UpdatedAt.Before(oldestTime)) {
				oldestKey = k
				oldestTime = s.UpdatedAt
			}
		}
		if oldestKey != "" {
			delete(slots, oldestKey)
		}
	}

	rev := 1
	if existing, exists := slots[slotType]; exists {
		rev = existing.Revision + 1
	}

	slot := WorkingSlot{
		Type:        slotType,
		Value:       value,
		Revision:    rev,
		Pinned:      pinned,
		LastAgentID: agentID,
		UpdatedAt:   time.Now().UTC(),
	}
	slots[slotType] = slot
	return slot, nil
}

// UpdateSlotCAS atomically updates a slot if the revision matches expectedRevision.
func (m *Manager) UpdateSlotCAS(ctx context.Context, taskID, agentID string, slotType SlotType, expectedRevision int, newValue string) (WorkingSlot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	slots, ok := m.sharedSlots[taskID]
	if !ok {
		return WorkingSlot{}, ErrSlotNotFound
	}

	slot, ok := slots[slotType]
	if !ok {
		return WorkingSlot{}, ErrSlotNotFound
	}

	if slot.Revision != expectedRevision {
		return WorkingSlot{}, fmt.Errorf("%w: expected rev %d, current rev %d", ErrCASConflict, expectedRevision, slot.Revision)
	}

	slot.Value = newValue
	slot.Revision++
	slot.LastAgentID = agentID
	slot.UpdatedAt = time.Now().UTC()
	slots[slotType] = slot

	return slot, nil
}

// ListSlots returns all active working memory slots for a task.
func (m *Manager) ListSlots(ctx context.Context, taskID string) []WorkingSlot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []WorkingSlot
	for _, s := range m.sharedSlots[taskID] {
		result = append(result, s)
	}
	return result
}

// SetPrivateSlot writes agent-private scratchpad state isolated from other agents.
func (m *Manager) SetPrivateSlot(ctx context.Context, taskID, agentID, key, value string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	scopeKey := fmt.Sprintf("%s:%s", taskID, agentID)
	pMap, ok := m.privateSlots[scopeKey]
	if !ok {
		pMap = make(map[string]string)
		m.privateSlots[scopeKey] = pMap
	}
	pMap[key] = value
	return value, nil
}

// GetPrivateSlot reads agent-private scratchpad state.
func (m *Manager) GetPrivateSlot(ctx context.Context, taskID, agentID, key string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	scopeKey := fmt.Sprintf("%s:%s", taskID, agentID)
	pMap, ok := m.privateSlots[scopeKey]
	if !ok {
		return "", false
	}
	val, ok := pMap[key]
	return val, ok
}
