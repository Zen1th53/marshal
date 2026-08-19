package shared

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrUnauthorizedMutation = errors.New("unauthorized contribution mutation: agents cannot edit peer contributions")
	ErrRecordNotFound       = errors.New("shared record not found")
)

type Archive struct {
	mu      sync.RWMutex
	records map[string]model.MemoryRecordV2
}

func NewArchive() *Archive {
	return &Archive{
		records: make(map[string]model.MemoryRecordV2),
	}
}

func generateShareID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("MEM-SHARE-%s", hex.EncodeToString(b[:]))
}

// Contribute adds a new memory record to the shared archive authored by actorAgentID.
func (a *Archive) Contribute(ctx context.Context, actorAgentID string, rec model.MemoryRecordV2) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if rec.ID == "" {
		rec.ID = generateShareID()
	}
	if rec.Source.AgentID == "" {
		rec.Source.AgentID = actorAgentID
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = rec.CreatedAt
	}
	rec.ContentDigest = rec.CanonicalDigest()

	a.records[rec.ID] = rec
	return nil
}

// UpdateContribution allows an agent to update their own contribution, rejecting unauthorized peer edits.
func (a *Archive) UpdateContribution(ctx context.Context, actorAgentID string, updated model.MemoryRecordV2) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	existing, ok := a.records[updated.ID]
	if !ok {
		return ErrRecordNotFound
	}

	// Peer edit protection
	if existing.Source.AgentID != actorAgentID {
		return fmt.Errorf("%w: record %s owned by %s, attempted update by %s",
			ErrUnauthorizedMutation, updated.ID, existing.Source.AgentID, actorAgentID)
	}

	// Durable records cannot be updated directly without lifecycle review
	if existing.Lifecycle == model.MemoryDurable {
		return fmt.Errorf("%w: durable records require lifecycle promotion governance", ErrUnauthorizedMutation)
	}

	updated.UpdatedAt = time.Now().UTC()
	updated.ContentDigest = updated.CanonicalDigest()
	a.records[updated.ID] = updated
	return nil
}

// Search queries the shared archive by project, team scope, and keyword.
func (a *Archive) Search(ctx context.Context, projectID, scopeID, keyword string) ([]model.MemoryRecordV2, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var matched []model.MemoryRecordV2
	kw := strings.ToLower(strings.TrimSpace(keyword))

	for _, rec := range a.records {
		if rec.ProjectID != projectID {
			continue
		}
		if scopeID != "" && rec.ScopeID != scopeID {
			continue
		}
		if kw != "" {
			titleMatch := strings.Contains(strings.ToLower(rec.Title), kw)
			bodyMatch := strings.Contains(strings.ToLower(rec.Body), kw)
			if !titleMatch && !bodyMatch {
				continue
			}
		}
		matched = append(matched, rec)
	}

	return matched, nil
}
