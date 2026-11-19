//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// ValidateProjectContractSubmission checks the living project before journaling
// a draft. A receipt retry of the already accepted draft remains idempotent.
func (r *Runtime) ValidateProjectContractSubmission(ctx context.Context, proposed projectcontext.Contract) error {
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err == nil {
		if digest, err := proposed.CanonicalDigest(); err == nil && digest == accepted.Digest {
			return nil
		}
		return projectcontext.ValidateContractRevision(accepted.Contract, proposed)
	}
	if !errors.Is(err, projectcontext.ErrReviewPending) {
		return err
	}
	return validateProjectBaseline(ctx, r.ProjectRoot(), proposed)
}

// A baseline must cover the code already here, including uncommitted source.
// Read the package layout directly so a missing or stale knowledge cache cannot
// turn the first goal's fragment into the accepted architecture of the project.
func validateProjectBaseline(ctx context.Context, root string, proposed projectcontext.Contract) error {
	for _, part := range proposed.Components {
		if part.Removed {
			return fmt.Errorf("%w: a baseline describes the current project; component %s cannot already be removed", projectcontext.ErrInvalid, part.ID)
		}
	}
	missing := map[string]bool{}
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if file == root {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case statepath.DirName, "vendor", "node_modules", "third_party", "dist", "build", "testdata", "__pycache__":
				return filepath.SkipDir
			}
			if strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !conformanceCode[filepath.Ext(file)] {
			return nil
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, part := range proposed.Components {
			if part.Removed {
				continue
			}
			for _, path := range part.Paths {
				if rel == path || strings.HasPrefix(rel, path+"/") {
					return nil
				}
			}
		}
		pieces := strings.Split(rel, "/")
		part := pieces[0]
		if len(pieces) > 2 && (part == "internal" || part == "cmd" || part == "pkg" || part == "src") {
			part += "/" + pieces[1]
		}
		missing[part] = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("read project package layout: %w", err)
	}
	if len(missing) > 0 {
		parts := make([]string, 0, len(missing))
		for part := range missing {
			parts = append(parts, part)
		}
		sort.Strings(parts)
		return fmt.Errorf("%w: the first architecture must cover the whole project; add component paths covering %s", projectcontext.ErrInvalid, strings.Join(parts, ", "))
	}
	return nil
}
