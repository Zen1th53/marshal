package blocks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrBlockNotFound  = errors.New("core memory block not found")
	ErrBlockTooLarge  = errors.New("core memory block exceeds size limit")
	ErrInvalidBlockID = errors.New("invalid block ID")
)

type Config struct {
	MaxBlockCharacters int
}

type CoreBlock struct {
	ID        string                `json:"id"`
	ProjectID string                `json:"project_id"`
	Name      string                `json:"name"`
	Content   string                `json:"content"`
	Authority model.MemoryAuthority `json:"authority"`
	Revision  int64                 `json:"revision"`
	CreatedAt time.Time             `json:"created_at"`
	UpdatedAt time.Time             `json:"updated_at"`
}

type BlockInput struct {
	ProjectID string                `json:"project_id"`
	Name      string                `json:"name"`
	Content   string                `json:"content"`
	Authority model.MemoryAuthority `json:"authority"`
}

type Manager struct {
	mu          sync.RWMutex
	config      Config
	blocks      map[string]*CoreBlock
	attachments map[string]map[string]bool // key: "scope:scopeID", value: set of blockIDs
}

func NewManager(config Config) *Manager {
	if config.MaxBlockCharacters == 0 {
		config.MaxBlockCharacters = 8000
	}
	return &Manager{
		config:      config,
		blocks:      make(map[string]*CoreBlock),
		attachments: make(map[string]map[string]bool),
	}
}

func generateBlockID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("BLK-%s", hex.EncodeToString(b[:]))
}

// CreateBlock creates and registers a new pinned core memory block.
func (m *Manager) CreateBlock(ctx context.Context, in BlockInput) (*CoreBlock, error) {
	if in.ProjectID == "" || in.Name == "" || in.Content == "" {
		return nil, errors.New("missing required block fields")
	}

	if len(in.Content) > m.config.MaxBlockCharacters {
		return nil, fmt.Errorf("%w: content length %d exceeds max %d", ErrBlockTooLarge, len(in.Content), m.config.MaxBlockCharacters)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	blk := &CoreBlock{
		ID:        generateBlockID(),
		ProjectID: in.ProjectID,
		Name:      in.Name,
		Content:   in.Content,
		Authority: in.Authority,
		Revision:  0,
		CreatedAt: now,
		UpdatedAt: now,
	}

	m.blocks[blk.ID] = blk
	return blk, nil
}

// Attach binds a block to a specific target scope (e.g. agent, task, team).
func (m *Manager) Attach(ctx context.Context, blockID, scopeKind, scopeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.blocks[blockID]; !ok {
		return ErrBlockNotFound
	}

	key := fmt.Sprintf("%s:%s", scopeKind, scopeID)
	if m.attachments[key] == nil {
		m.attachments[key] = make(map[string]bool)
	}
	m.attachments[key][blockID] = true
	return nil
}

// Detach unbinds a block from a specific target scope.
func (m *Manager) Detach(ctx context.Context, blockID, scopeKind, scopeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s:%s", scopeKind, scopeID)
	if m.attachments[key] != nil {
		delete(m.attachments[key], blockID)
	}
	return nil
}

// GetAttachedBlocks retrieves all core blocks currently attached to the given scope.
func (m *Manager) GetAttachedBlocks(ctx context.Context, projectID, scopeKind, scopeID string) ([]*CoreBlock, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := fmt.Sprintf("%s:%s", scopeKind, scopeID)
	blockSet := m.attachments[key]

	var result []*CoreBlock
	for id := range blockSet {
		if blk, ok := m.blocks[id]; ok && blk.ProjectID == projectID {
			result = append(result, blk)
		}
	}
	return result, nil
}
