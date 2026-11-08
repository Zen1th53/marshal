package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"github.com/Zen1th53/marshal/internal/architecturereview"
	"github.com/Zen1th53/marshal/internal/model"
)

const architectureContextLimit = 128 << 10

// acceptedArchitectureContext reads the owner runtime's browser review as data.
// A worker checkout cannot substitute its own state or create an approval.
func (r *Runtime) acceptedArchitectureContext(ctx context.Context, roleRoot string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || r.store == nil {
		return nil, model.ErrUnavailable
	}
	owner := filepath.Clean(r.layout.Root)
	if !filepath.IsAbs(owner) || r.layout.Root != owner || r.layout.RuntimeDir != filepath.Join(owner, statepath.DirName) {
		return nil, model.ErrPolicyDenied
	}
	if roleRoot != owner {
		if r.layout.Worktrees != filepath.Join(r.layout.RuntimeDir, "worktrees") {
			return nil, model.ErrPolicyDenied
		}
		contained := false
		for _, base := range []string{"worktrees", "branches"} {
			rel, err := filepath.Rel(filepath.Join(r.layout.RuntimeDir, base), roleRoot)
			if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				contained = true
			}
		}
		if !contained {
			return nil, model.ErrPolicyDenied
		}
	}
	for _, path := range []string{owner, roleRoot} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || resolved != path {
			return nil, model.ErrPolicyDenied
		}
	}
	ownerInfo, err := os.Lstat(owner)
	if err != nil || !ownerInfo.IsDir() {
		return nil, model.ErrPolicyDenied
	}
	root, err := os.OpenRoot(owner)
	if err != nil {
		return nil, fmt.Errorf("open architecture owner: %w", err)
	}
	defer root.Close()
	openedInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(ownerInfo, openedInfo) {
		return nil, model.ErrPolicyDenied
	}
	paths := []string{statepath.DirName, filepath.Join(statepath.DirName, "architecture"), filepath.Join(statepath.DirName, "architecture", "review"), filepath.Join(statepath.DirName, "architecture", "review", "state.json")}
	infos := make([]os.FileInfo, len(paths))
	for i, path := range paths {
		info, err := root.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("inspect browser architecture: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || (i < len(paths)-1 && !info.IsDir()) || (i == len(paths)-1 && !info.Mode().IsRegular()) {
			return nil, model.ErrPolicyDenied
		}
		infos[i] = info
	}
	file, err := root.Open(paths[len(paths)-1])
	if err != nil {
		return nil, fmt.Errorf("open browser architecture: %w", err)
	}
	defer file.Close()
	fileInfo, err := file.Stat()
	if err != nil || !os.SameFile(fileInfo, infos[len(infos)-1]) || fileInfo.Size() != infos[len(infos)-1].Size() || !fileInfo.ModTime().Equal(infos[len(infos)-1].ModTime()) || fileInfo.Size() > architectureContextLimit {
		return nil, model.ErrPolicyDenied
	}
	data, err := io.ReadAll(io.LimitReader(file, architectureContextLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read browser architecture: %w", err)
	}
	if len(data) > architectureContextLimit {
		return nil, model.ErrPolicyDenied
	}
	for i, path := range paths {
		info, err := root.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || !os.SameFile(infos[i], info) {
			return nil, model.ErrPolicyDenied
		}
		if i == len(paths)-1 && (info.Size() != infos[i].Size() || !info.ModTime().Equal(infos[i].ModTime())) {
			return nil, model.ErrPolicyDenied
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	packet, err := architecturereview.ValidateStoredState(data, r.ProjectIdentity())
	if err != nil {
		return nil, err
	}
	if err := r.validateMarshalArchitectureApproval(ctx, data); err != nil {
		return nil, err
	}
	return packet, nil
}

// RequireArchitectureAcceptance preserves legacy projects without a review,
// but a current browser draft cannot be bypassed by approving a task plan.
func (r *Runtime) RequireArchitectureAcceptance(ctx context.Context) error {
	data, err := r.acceptedArchitectureContext(ctx, r.layout.Root)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var packet struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &packet); err != nil {
		return err
	}
	if packet.Status != "accepted" {
		if architecturecodec.ModelArchitectureApprovalAllowed() {
			return errors.New("accept the exact current architecture in the browser or on a recorded operator instruction before plan approval or worker dispatch")
		}
		return errors.New("accept the exact current architecture in the browser before plan approval or worker dispatch")
	}
	return nil
}
