package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RunStore defines storage and retrieval for canonical ExecutionRuns.
type RunStore interface {
	CreateRun(ctx context.Context, run ExecutionRun) error
	GetRun(ctx context.Context, runID string) (ExecutionRun, error)
	UpdateRun(ctx context.Context, run ExecutionRun) error
	ListRuns(ctx context.Context) ([]ExecutionRun, error)
}

// IntentStore defines storage and state tracking for two-phase durable intents.
type IntentStore interface {
	SaveIntent(ctx context.Context, intent ExecutionIntent) error
	GetIntent(ctx context.Context, intentID string) (ExecutionIntent, error)
	GetIntentByIdempotencyKey(ctx context.Context, runID, key string) (ExecutionIntent, error)
	ListIntents(ctx context.Context, runID string) ([]ExecutionIntent, error)
}

// MemoryRunStore is an in-memory implementation of RunStore with CAS versioning.
type MemoryRunStore struct {
	mu   sync.RWMutex
	runs map[string]ExecutionRun
}

// NewMemoryRunStore creates a new in-memory run store.
func NewMemoryRunStore() *MemoryRunStore {
	return &MemoryRunStore{
		runs: make(map[string]ExecutionRun),
	}
}

func (s *MemoryRunStore) CreateRun(ctx context.Context, run ExecutionRun) error {
	if run.RunID == "" {
		return fmt.Errorf("%w: run ID cannot be empty", ErrRunInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.runs[run.RunID]; exists {
		return fmt.Errorf("%w: run %s already exists", ErrRunConflict, run.RunID)
	}
	if run.Version == 0 {
		run.Version = 1
	}
	s.runs[run.RunID] = run
	return nil
}

func (s *MemoryRunStore) GetRun(ctx context.Context, runID string) (ExecutionRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	run, exists := s.runs[runID]
	if !exists {
		return ExecutionRun{}, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	return run, nil
}

func (s *MemoryRunStore) UpdateRun(ctx context.Context, run ExecutionRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.runs[run.RunID]
	if !exists {
		return fmt.Errorf("%w: %s", ErrRunNotFound, run.RunID)
	}

	// CAS check: incoming version must match current version
	if run.Version != current.Version {
		return fmt.Errorf("%w: version mismatch (got v%d, want v%d)",
			ErrRunConflict, run.Version, current.Version)
	}

	run.Version++
	run.UpdatedAt = time.Now().UTC()
	s.runs[run.RunID] = run
	return nil
}

func (s *MemoryRunStore) ListRuns(ctx context.Context) ([]ExecutionRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]ExecutionRun, 0, len(s.runs))
	for _, r := range s.runs {
		result = append(result, r)
	}
	return result, nil
}

// FileRunStore implements durable disk persistence for ExecutionRuns.
type FileRunStore struct {
	baseDir string
	mem     *MemoryRunStore
}

// NewFileRunStore creates a durable file-backed run store.
func NewFileRunStore(baseDir string) (*FileRunStore, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create run store directory: %w", err)
	}
	store := &FileRunStore{
		baseDir: baseDir,
		mem:     NewMemoryRunStore(),
	}

	// Load existing runs from disk
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read run store directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			data, err := os.ReadFile(filepath.Join(baseDir, entry.Name()))
			if err != nil {
				continue
			}
			var run ExecutionRun
			if err := json.Unmarshal(data, &run); err == nil && run.RunID != "" {
				store.mem.runs[run.RunID] = run
			}
		}
	}

	return store, nil
}

func (f *FileRunStore) CreateRun(ctx context.Context, run ExecutionRun) error {
	if err := f.mem.CreateRun(ctx, run); err != nil {
		return err
	}
	return f.persistRun(run)
}

func (f *FileRunStore) GetRun(ctx context.Context, runID string) (ExecutionRun, error) {
	return f.mem.GetRun(ctx, runID)
}

func (f *FileRunStore) UpdateRun(ctx context.Context, run ExecutionRun) error {
	if err := f.mem.UpdateRun(ctx, run); err != nil {
		return err
	}
	updated, _ := f.mem.GetRun(ctx, run.RunID)
	return f.persistRun(updated)
}

func (f *FileRunStore) ListRuns(ctx context.Context) ([]ExecutionRun, error) {
	return f.mem.ListRuns(ctx)
}

func (f *FileRunStore) persistRun(run ExecutionRun) error {
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal run: %w", err)
	}

	destPath := filepath.Join(f.baseDir, run.RunID+".json")
	tmpPath := destPath + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write run temp file: %w", err)
	}

	// Atomic rename ensures crash consistency
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed to commit run file atomically: %w", err)
	}

	return nil
}

// MemoryIntentStore implements an in-memory two-phase intent store.
type MemoryIntentStore struct {
	mu      sync.RWMutex
	intents map[string]ExecutionIntent // intentID -> Intent
}

// NewMemoryIntentStore creates a new memory intent store.
func NewMemoryIntentStore() *MemoryIntentStore {
	return &MemoryIntentStore{
		intents: make(map[string]ExecutionIntent),
	}
}

func (s *MemoryIntentStore) SaveIntent(ctx context.Context, intent ExecutionIntent) error {
	if intent.IntentID == "" {
		return fmt.Errorf("%w: intent ID is empty", ErrRunInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	intent.UpdatedAt = time.Now().UTC()
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = intent.UpdatedAt
	}
	s.intents[intent.IntentID] = intent
	return nil
}

func (s *MemoryIntentStore) GetIntent(ctx context.Context, intentID string) (ExecutionIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	intent, exists := s.intents[intentID]
	if !exists {
		return ExecutionIntent{}, fmt.Errorf("intent %s not found", intentID)
	}
	return intent, nil
}

func (s *MemoryIntentStore) GetIntentByIdempotencyKey(ctx context.Context, runID, key string) (ExecutionIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, intent := range s.intents {
		if intent.RunID == runID && intent.IdempotencyKey == key {
			return intent, nil
		}
	}
	return ExecutionIntent{}, fmt.Errorf("intent for key %s not found in run %s", key, runID)
}

func (s *MemoryIntentStore) ListIntents(ctx context.Context, runID string) ([]ExecutionIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []ExecutionIntent
	for _, intent := range s.intents {
		if intent.RunID == runID {
			result = append(result, intent)
		}
	}
	return result, nil
}
