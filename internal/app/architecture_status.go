package app

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/Zen1th53/marshal/internal/architecturereview"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// ArchitectureStatus is what the workspace shows about the browser
// architecture review. It reads the review's own state; it grants nothing.
type ArchitectureStatus struct {
	Exists    bool
	Revision  int64
	Accepted  bool
	Answering bool // the Marshal is answering a request
	Open      int  // unresolved requests waiting for the owner
}

// ArchitectureReviewStatus reads the browser review's own state for the TUI
// status line: whether a review exists, its revision, whether it is
// accepted, whether the Marshal is answering a request, and how many
// requests wait for the owner. It reads only the owner runtime's own review
// and grants nothing.
func (r *Runtime) ArchitectureReviewStatus(ctx context.Context) (ArchitectureStatus, error) {
	if err := ctx.Err(); err != nil {
		return ArchitectureStatus{}, err
	}
	if r == nil || r.layout.RuntimeDir != filepath.Join(r.layout.Root, statepath.DirName) {
		return ArchitectureStatus{}, model.ErrPolicyDenied
	}
	directory := filepath.Join(r.layout.RuntimeDir, "architecture", "review")
	data, err := readArchitecturePrivateFile(directory, "state.json", architectureContextLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return ArchitectureStatus{}, nil
	}
	if err != nil {
		return ArchitectureStatus{}, err
	}
	packet, err := architecturereview.ValidateStoredState(data, r.ProjectIdentity())
	if err != nil {
		return ArchitectureStatus{}, err
	}
	var bound struct {
		Status   string `json:"status"`
		Revision int64  `json:"revision"`
	}
	if err := json.Unmarshal(packet, &bound); err != nil {
		return ArchitectureStatus{}, err
	}
	var raw struct {
		Comments []struct {
			Status   string `json:"status"`
			Resolved bool   `json:"resolved"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return ArchitectureStatus{}, err
	}
	status := ArchitectureStatus{Exists: true, Revision: bound.Revision, Accepted: bound.Status == "accepted"}
	for _, c := range raw.Comments {
		if c.Status == "queued" || c.Status == "running" {
			status.Answering = true
			continue
		}
		if !c.Resolved {
			status.Open++
		}
	}
	return status, nil
}
