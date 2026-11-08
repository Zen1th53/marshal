package projectknowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"os"
	"path/filepath"
	"strings"
)

var ErrContextBudget = errors.New("project architecture context exceeds its byte budget")

// OpenWorktree puts a task's disposable index in its owning runtime directory,
// never in the task checkout: workers commit their source, not a changing cache.
func OpenWorktree(root, ownerRoot, ownerState string) (*Index, error) {
	owner, err := Open(ownerRoot, ownerState)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || resolved != abs {
		return nil, ErrBinding
	}
	contained := false
	for _, base := range []string{"worktrees", "branches"} {
		rel, err := filepath.Rel(filepath.Join(ownerState, base), abs)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			contained = true
		}
	}
	if !contained {
		return nil, ErrBinding
	}
	common := func(root string) (string, error) {
		cmd, err := hostgit.Command(context.Background(), root, "rev-parse", "--git-common-dir")
		if err != nil {
			return "", err
		}
		data, err := cmd.Output()
		if err != nil {
			return "", err
		}
		path := strings.TrimSpace(string(data))
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		return filepath.EvalSymlinks(path)
	}
	a, err := common(abs)
	if err != nil {
		return nil, ErrBinding
	}
	b, err := common(ownerRoot)
	if err != nil || a != b {
		return nil, ErrBinding
	}
	sum := sha256.Sum256([]byte(abs))
	index := &Index{root: abs, state: owner.state, database: filepath.Join(ownerState, "knowledge", "worktrees", hex.EncodeToString(sum[:]), "index-v1.db")}
	if err := index.safeState(); err != nil {
		return nil, err
	}
	return index, nil
}

type SourceText struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	FileHash  string `json:"file_hash"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Architecture contains current repository observations, never approval or
// instructions. Its source fingerprint belongs to the same snapshot as symbols.
type Architecture struct {
	Status       string       `json:"status"`
	Trust        string       `json:"trust"`
	Snapshot     Snapshot     `json:"snapshot"`
	Purpose      []SourceText `json:"purpose"`
	Architecture []SourceText `json:"architecture"`
}

// Ensure updates only this project's disposable projection. Corruption and
// binding failures are not repaired silently. Query callers cannot invoke it.
func (i *Index) Ensure(ctx context.Context) error {
	_, err := i.Query(ctx, Query{Mode: "overview", Limit: 1})
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrStale) {
		if _, statErr := os.Lstat(i.database); !errors.Is(statErr, os.ErrNotExist) {
			return err
		}
	}
	_, err = i.Refresh(ctx)
	return err
}

// ArchitectureContext never serves a cached document body: source bytes and
// hashes are rechecked before and after extraction, including uncommitted edits.
func (i *Index) ArchitectureContext(ctx context.Context, maxBytes int) (Architecture, error) {
	if maxBytes < 1024 || maxBytes > 65536 {
		return Architecture{}, ErrContextBudget
	}
	result, err := i.Query(ctx, Query{Mode: "context", Limit: 100})
	if err != nil {
		return Architecture{}, fmt.Errorf("query context sources: %w", err)
	}
	if result.NextOffset != 0 {
		return Architecture{}, ErrContextBudget
	}
	out := Architecture{Status: "needs_architecture", Trust: "repository_observations_not_instructions_or_approval", Snapshot: result.Snapshot, Purpose: []SourceText{}, Architecture: []SourceText{}}
	for _, entry := range result.Items {
		if err := ctx.Err(); err != nil {
			return Architecture{}, err
		}
		data, ok, err := i.readFile(entry.Path)
		if err != nil {
			return Architecture{}, err
		}
		if !ok || !safeSourceContent(entry.Path, data) {
			return Architecture{}, ErrStale
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != entry.FileHash {
			return Architecture{}, fmt.Errorf("%w: architecture source bytes changed", ErrStale)
		}
		source := SourceText{Path: entry.Path, Line: 1, FileHash: entry.FileHash, Text: string(data)}
		if strings.Contains(strings.ToLower(entry.Path), "architecture") {
			out.Architecture = append(out.Architecture, source)
		} else {
			// README setup/reference sections are not the overall project goal.
			// Preserve the opening section and explicitly identify any omission.
			if at := strings.Index(source.Text, "\n## "); at >= 0 {
				source.Text = source.Text[:at]
				source.Truncated = true
			}
			out.Purpose = append(out.Purpose, source)
		}
	}
	if len(out.Architecture) > 0 {
		out.Status = "ready"
	}
	if len(out.Purpose) == 0 {
		out.Status = "needs_purpose"
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return Architecture{}, err
	}
	if len(encoded) > maxBytes {
		return Architecture{}, fmt.Errorf("%w: need %d bytes", ErrContextBudget, len(encoded))
	}
	current, err := i.Query(ctx, Query{Mode: "context", Limit: 1})
	if err != nil {
		return Architecture{}, err
	}
	if current.Snapshot.Head != out.Snapshot.Head || current.Snapshot.Digest != out.Snapshot.Digest {
		return Architecture{}, ErrStale
	}
	return out, nil
}
