package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// retainedWorktreeIDs joins legacy ownership with canonical Marshal snapshots
// and durable Process05 run/lane/evidence state. Retention outlives completion:
// releasing evidence is a separate explicit operation, never inferred from TTL.
func (r *Runtime) retainedWorktreeIDs(ctx context.Context, tasks []model.Task) ([]string, error) {
	keep := map[string]bool{}
	addPath := func(path string) {
		if path == "" {
			return
		}
		rel, err := filepath.Rel(r.layout.Worktrees, filepath.Clean(path))
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			keep[strings.Split(rel, string(filepath.Separator))[0]] = true
		}
	}
	for _, task := range tasks {
		switch task.Status {
		case model.TaskMerged, model.TaskCancelled, model.TaskSuperseded:
		default:
			keep[task.ID] = true
		}
		if task.OwnerAgentID != nil && *task.OwnerAgentID != "" {
			keep[task.ID] = true
		}
		if task.Worktree != nil {
			addPath(*task.Worktree)
		}
	}
	runs, err := r.store.MarshalRuns(ctx, r.ProjectID())
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(r.layout.Worktrees)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	// The run prefix also retains superseded amendment and failed-attempt trees
	// that no longer appear in the latest task slice.
	for runID, record := range runs {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "TASK-"+runID+"-") {
				keep[entry.Name()] = true
			}
		}
		if record.Value.Operation != nil {
			addPath(record.Value.Operation.Dir)
		}
	}
	// Decode the persisted files directly. FileRunStore deliberately ignores
	// malformed files; a GC must instead stop when it cannot establish references.
	execRoot := filepath.Join(r.layout.RuntimeDir, "execution")
	err = filepath.WalkDir(execRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) && path == execRoot {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var value any
		if err = json.Unmarshal(body, &value); err != nil {
			return fmt.Errorf("read canonical worktree references %s: %w", path, err)
		}
		var visit func(any)
		visit = func(value any) {
			switch v := value.(type) {
			case map[string]any:
				for key, item := range v {
					normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
					if normalized == "worktree" || normalized == "worktreepath" {
						if path, ok := item.(string); ok {
							addPath(path)
						}
					}
					visit(item)
				}
			case []any:
				for _, item := range v {
					visit(item)
				}
			}
		}
		visit(value)
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(keep))
	for id := range keep {
		result = append(result, id)
	}
	return result, nil
}
