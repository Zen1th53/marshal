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
// Implementations own their stored state and return independent snapshots;
// callers may mutate a retrieved run before submitting a CAS update.
type RunStore interface {
	CreateRun(ctx context.Context, run ExecutionRun) error
	GetRun(ctx context.Context, runID string) (ExecutionRun, error)
	UpdateRun(ctx context.Context, run ExecutionRun) error
	ListRuns(ctx context.Context) ([]ExecutionRun, error)
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
	owned, err := cloneExecutionRun(run)
	if err != nil {
		return err
	}
	s.runs[run.RunID] = owned
	return nil
}

func (s *MemoryRunStore) GetRun(ctx context.Context, runID string) (ExecutionRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	run, exists := s.runs[runID]
	if !exists {
		return ExecutionRun{}, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	return cloneExecutionRun(run)
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
	owned, err := cloneExecutionRun(run)
	if err != nil {
		return err
	}
	s.runs[run.RunID] = owned
	return nil
}

func (s *MemoryRunStore) ListRuns(ctx context.Context) ([]ExecutionRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]ExecutionRun, 0, len(s.runs))
	for _, r := range s.runs {
		snapshot, err := cloneExecutionRun(r)
		if err != nil {
			return nil, err
		}
		result = append(result, snapshot)
	}
	return result, nil
}

// Clone the complete durable value, including nested task slices and pointers,
// using the same representation as FileRunStore. The caller holds the store
// lock when cloning stored state, so no mutable map escapes that lock.
func cloneExecutionRun(run ExecutionRun) (ExecutionRun, error) {
	data, err := json.Marshal(run)
	if err != nil {
		return ExecutionRun{}, err
	}
	var snapshot ExecutionRun
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return ExecutionRun{}, err
	}
	return snapshot, nil
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
	if run.RunID == "" {
		return fmt.Errorf("%w: run ID cannot be empty", ErrRunInvalid)
	}
	f.mem.mu.Lock()
	defer f.mem.mu.Unlock()
	if _, exists := f.mem.runs[run.RunID]; exists {
		return fmt.Errorf("%w: run %s already exists", ErrRunConflict, run.RunID)
	}
	if run.Version == 0 {
		run.Version = 1
	}
	owned, err := cloneExecutionRun(run)
	if err != nil {
		return err
	}
	if err := f.persistRun(owned); err != nil {
		return err
	}
	f.mem.runs[run.RunID] = owned
	return nil
}

func (f *FileRunStore) GetRun(ctx context.Context, runID string) (ExecutionRun, error) {
	return f.mem.GetRun(ctx, runID)
}

func (f *FileRunStore) UpdateRun(ctx context.Context, run ExecutionRun) error {
	// Hold the canonical store lock through the disk commit: failed persistence
	// must not publish a new revision, and competing CAS writers must serialize.
	f.mem.mu.Lock()
	defer f.mem.mu.Unlock()
	current, exists := f.mem.runs[run.RunID]
	if !exists {
		return fmt.Errorf("%w: %s", ErrRunNotFound, run.RunID)
	}
	if run.Version != current.Version {
		return fmt.Errorf("%w: version mismatch (got v%d, want v%d)",
			ErrRunConflict, run.Version, current.Version)
	}
	run.Version++
	run.UpdatedAt = time.Now().UTC()
	owned, err := cloneExecutionRun(run)
	if err != nil {
		return err
	}
	if err := f.persistRun(owned); err != nil {
		return err
	}
	f.mem.runs[run.RunID] = owned
	return nil
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
	return writeRunSnapshot(destPath, data)
}
