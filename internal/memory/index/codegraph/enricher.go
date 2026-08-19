package codegraph

import (
	"context"
	"fmt"
	"sync"

	"github.com/Zen1th53/marshal/internal/model"
)

type CodeLink struct {
	MemoryID   string `json:"memory_id"`
	ProjectID  string `json:"project_id"`
	FilePath   string `json:"file_path"`
	Symbol     string `json:"symbol,omitempty"`
	HeadCommit string `json:"head_commit,omitempty"`
}

type ImpactResult struct {
	MemoryID string `json:"memory_id"`
	FilePath string `json:"file_path"`
	Symbol   string `json:"symbol,omitempty"`
	IsStale  bool   `json:"is_stale"`
}

type Enricher struct {
	mu      sync.RWMutex
	links   []CodeLink
	renames map[string]string // oldPath -> newPath
}

func NewEnricher() *Enricher {
	return &Enricher{
		renames: make(map[string]string),
	}
}

// RecordFileRename registers a file rename alias for path reconciliation.
func (e *Enricher) RecordFileRename(oldPath, newPath string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.renames[newPath] = oldPath
	e.renames[oldPath] = newPath
}

// EnrichRecord extracts touched file and symbol associations from record metadata.
func (e *Enricher) EnrichRecord(ctx context.Context, rec model.MemoryRecordV2) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var files []string
	var symbols []string

	if rec.ExtMeta != nil {
		if rawFiles, ok := rec.ExtMeta["touched_files"].([]string); ok {
			files = rawFiles
		} else if rawAny, ok := rec.ExtMeta["touched_files"].([]any); ok {
			for _, f := range rawAny {
				if s, ok := f.(string); ok {
					files = append(files, s)
				}
			}
		}

		if rawSyms, ok := rec.ExtMeta["touched_symbols"].([]string); ok {
			symbols = rawSyms
		} else if rawAny, ok := rec.ExtMeta["touched_symbols"].([]any); ok {
			for _, s := range rawAny {
				if str, ok := s.(string); ok {
					symbols = append(symbols, str)
				}
			}
		}
	}

	for _, file := range files {
		if len(symbols) > 0 {
			for _, sym := range symbols {
				e.links = append(e.links, CodeLink{
					MemoryID:   rec.ID,
					ProjectID:  rec.ProjectID,
					FilePath:   file,
					Symbol:     sym,
					HeadCommit: rec.HeadCommit,
				})
			}
		} else {
			e.links = append(e.links, CodeLink{
				MemoryID:   rec.ID,
				ProjectID:  rec.ProjectID,
				FilePath:   file,
				HeadCommit: rec.HeadCommit,
			})
		}
	}

	return nil
}

// FindImpact searches for all memory records and decisions associated with a file path or symbol.
func (e *Enricher) FindImpact(ctx context.Context, projectID, filePath, symbol, currentHeadCommit string) ([]ImpactResult, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	targetPath := filePath
	if orig, ok := e.renames[filePath]; ok {
		targetPath = orig
	}

	var results []ImpactResult
	seen := make(map[string]bool)

	for _, link := range e.links {
		if link.ProjectID != projectID {
			continue
		}

		pathMatch := link.FilePath == filePath || link.FilePath == targetPath
		symbolMatch := symbol == "" || link.Symbol == "" || link.Symbol == symbol

		if pathMatch && symbolMatch {
			key := fmt.Sprintf("%s:%s:%s", link.MemoryID, link.FilePath, link.Symbol)
			if !seen[key] {
				seen[key] = true
				isStale := link.HeadCommit != "" && currentHeadCommit != "" && link.HeadCommit != currentHeadCommit
				results = append(results, ImpactResult{
					MemoryID: link.MemoryID,
					FilePath: link.FilePath,
					Symbol:   link.Symbol,
					IsStale:  isStale,
				})
			}
		}
	}

	return results, nil
}
