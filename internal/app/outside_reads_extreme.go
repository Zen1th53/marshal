//go:build !governed

package app

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
)

// maxOutsideReadFolders bounds the mounts one governed launch receives.
const maxOutsideReadFolders = 16

// outsideReadMounts gives a governed worker the outside folders it needs,
// read-only. The private edition reads outside the project automatically on
// the host (HasReadGrant); inside the sandbox there is no read broker, so the
// folders the task names and every folder the operator allowed are mounted at
// their own paths instead. HOME itself is never mounted: credential stores,
// provider state, MARSHAL state, runtime sockets and denied folders inside a
// mounted folder are hidden. Each mount is recorded as evidence.
func (r *Runtime) outsideReadMounts(ctx context.Context, taskID, subject, worktree string) []model.OutsideRead {
	if r == nil {
		return nil
	}
	home, _ := os.UserHomeDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	project := r.layout.Root
	if resolved, err := filepath.EvalSymlinks(project); err == nil {
		project = resolved
	}
	var candidates []string
	var denied []string
	if r.store != nil {
		if grants, err := r.store.ListOperatorReadGrants(ctx, r.ProjectIdentity()); err == nil {
			for _, grant := range grants {
				if grant.Allowed {
					candidates = append(candidates, grant.Folder)
				} else {
					denied = append(denied, grant.Folder)
				}
			}
		}
	}
	candidates = append(candidates, outsidePathCandidates(outsideReadHints(ctx), home)...)
	folders := map[string]bool{}
	for _, candidate := range candidates {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil {
			continue
		}
		folder := resolved
		if !info.IsDir() {
			folder = filepath.Dir(resolved)
		}
		if folders[folder] || outsideReadExcluded(folder, home, project) || sameOrWithin(folder, worktree) || sameOrWithin(worktree, folder) {
			continue
		}
		// The most specific operator decision still wins; an undecided
		// folder is read automatically and its first read is recorded.
		if !r.HasReadGrant(resolved) {
			continue
		}
		folders[folder] = true
	}
	ordered := make([]string, 0, len(folders))
	for folder := range folders {
		ordered = append(ordered, folder)
	}
	sort.Strings(ordered)
	var reads []model.OutsideRead
	for _, folder := range ordered {
		// A folder inside another mounted folder is already visible.
		nested := false
		for _, outer := range ordered {
			if within(outer, folder) {
				nested = true
				break
			}
		}
		if nested {
			continue
		}
		if len(reads) == maxOutsideReadFolders {
			break
		}
		dirs, files := outsideReadMasks(folder, home, denied)
		reads = append(reads, model.OutsideRead{Folder: folder, MaskDirs: dirs, MaskFiles: files})
	}
	if len(reads) > 0 && r.store != nil {
		if id, err := model.NewID("EVENT-"); err == nil {
			mounted := make([]any, 0, len(reads))
			for _, read := range reads {
				mounted = append(mounted, map[string]any{"folder": read.Folder, "masked_dirs": read.MaskDirs, "masked_files": read.MaskFiles})
			}
			_, _ = r.store.Append(ctx, events.Event{ID: id, Type: events.EventTypePolicyAllowed, Subject: subject, TaskID: taskID, At: time.Now().UTC(),
				Data: map[string]any{"decision": "outside folders mounted read-only in the worker sandbox", "reason": extremeReadReason, "outside_reads": mounted}})
		}
	}
	return reads
}

// outsideReadDirArgs lets a Claude Marshal role turn (review, verification,
// planning) read the outside folders its own prompt names. The turn runs on
// the host, so a folder is added only when nothing inside it needs masking;
// the same exclusions and recorded read decision apply as for workers.
func (m *MarshalCLI) outsideReadDirArgs(ctx context.Context, prompt string) []string {
	if m == nil || m.runtime == nil {
		return nil
	}
	var args []string
	for _, read := range m.runtime.outsideReadMounts(withOutsideReadHints(ctx, prompt), "", "marshal-"+m.Provider, m.Dir) {
		if len(read.MaskDirs) == 0 && len(read.MaskFiles) == 0 {
			args = append(args, "--add-dir="+read.Folder)
		}
	}
	return args
}
