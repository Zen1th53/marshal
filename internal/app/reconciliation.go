package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/slaves/internal/model"
)

type ReconcileRequest struct {
	FileState string `json:"file_state"`
}

type ReconciliationConflict struct {
	Field        string `json:"field"`
	FileState    any    `json:"file_state"`
	RuntimeState any    `json:"runtime_state"`
}

type ReconciliationReport struct {
	Status    string                   `json:"status"`
	Mode      string                   `json:"mode"`
	Conflicts []ReconciliationConflict `json:"conflicts"`
}

func (r *Runtime) Reconcile(ctx context.Context, request ReconcileRequest) (ReconciliationReport, error) {
	path, err := filepath.Abs(request.FileState)
	if err != nil {
		return ReconciliationReport{}, err
	}
	relative, err := filepath.Rel(r.layout.Root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ReconciliationReport{}, fmt.Errorf("%w: file state is outside repository", model.ErrInvalid)
	}
	file, err := os.Open(path)
	if err != nil {
		return ReconciliationReport{}, fmt.Errorf("open file state: %w", err)
	}
	defer file.Close()
	var fileState map[string]any
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	if err := decoder.Decode(&fileState); err != nil {
		return ReconciliationReport{}, fmt.Errorf("%w: decode file state: %v", model.ErrInvalid, err)
	}
	tasks, err := r.store.ListTasks(ctx)
	if err != nil {
		return ReconciliationReport{}, err
	}
	runtimeState := map[string]any{"project": map[string]any{"branch": r.layout.Branch, "commit": r.layout.HEAD}}
	if len(tasks) > 0 {
		runtimeState["task"] = map[string]any{"id": tasks[0].ID, "status": string(tasks[0].Status)}
	}
	report := ReconciliationReport{Status: "CLEAN", Mode: "runtime-first", Conflicts: []ReconciliationConflict{}}
	for _, field := range []string{"task.id", "task.phase", "task.status", "project.branch", "project.commit"} {
		left, leftOK := nested(fileState, field)
		right, rightOK := nested(runtimeState, field)
		if leftOK && rightOK && fmt.Sprint(left) != fmt.Sprint(right) {
			report.Conflicts = append(report.Conflicts, ReconciliationConflict{Field: field, FileState: left, RuntimeState: right})
		}
	}
	if len(report.Conflicts) > 0 {
		report.Status = "CONFLICT"
	}
	return report, nil
}

func nested(value map[string]any, dotted string) (any, bool) {
	var current any = value
	for _, part := range strings.Split(dotted, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}
