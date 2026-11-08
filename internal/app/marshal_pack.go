package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// MarshalPackRelativePath is where an interactive Marshal writes the plan
// pack next to its task list.
const MarshalPackRelativePath = statepath.DirName + "/marshal/plan"

const (
	marshalPackRequirements = "REQUIREMENTS.md"
	marshalPackIndex        = "00_INDEX.md"
	marshalPackTasks        = "tasks"
	// A note is context for a worker, not a document store; the cap keeps a
	// brief within what a worker session reads.
	marshalPackFileLimit = 64 << 10
)

// MarshalRunPackPath is where a planning draft's source pack is kept once the
// runtime has taken it. Proposal imports its contents into SQLite; this path
// is never consulted for approval or dispatch.
func MarshalRunPackPath(root, runID string) string {
	return filepath.Join(root, statepath.DirName, "marshal", "runs", runID, "plan")
}

// TakePlanPack moves the pack the Marshal wrote to its private run directory,
// so a later Marshal session starts without it and the source remains
// available for inspection after it is imported into SQLite.
func (s *MarshalService) TakePlanPack(runID string) (string, error) {
	if err := s.requireLifecycleOwner(); err != nil {
		return "", err
	}
	if !marshalIdentifier(runID) {
		return "", errors.New("invalid run ID")
	}
	from := filepath.Join(s.Repository, MarshalPackRelativePath)
	info, err := os.Lstat(from)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("the Marshal wrote no plan pack at %s", MarshalPackRelativePath)
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", MarshalPackRelativePath)
	}
	to := MarshalRunPackPath(s.Repository, runID)
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return "", err
	}
	// A later draft for the same run (a corrected or re-imported draft)
	// replaces the taken pack. The earlier pack is kept beside it for
	// inspection, never merged into or silently overwritten by the new one.
	if _, err := os.Lstat(to); err == nil {
		if err := os.Rename(to, supersededPackPath(to)); err != nil {
			return "", fmt.Errorf("retain the previously taken plan pack: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(from, to); err != nil {
		return "", err
	}
	return to, nil
}

// supersededPackPath names the first free "<pack>.superseded-N" path.
func supersededPackPath(pack string) string {
	for n := 1; ; n++ {
		candidate := fmt.Sprintf("%s.superseded-%d", pack, n)
		if _, err := os.Lstat(candidate); errors.Is(err, fs.ErrNotExist) {
			return candidate
		}
	}
}

// ReadPlanPack reads a pack and requires exactly one note per task, so a
// note cannot drift away from the task it was written for.
func ReadPlanPack(dir string, taskIDs []string) (marshal.PlanPack, error) {
	var pack marshal.PlanPack
	var err error
	if pack.Requirements, err = readPackFile(dir, marshalPackRequirements); err != nil {
		return pack, err
	}
	if pack.Index, err = readPackFile(dir, marshalPackIndex); err != nil {
		return pack, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, marshalPackTasks))
	if err != nil {
		return pack, fmt.Errorf("plan pack: %w", err)
	}
	want := map[string]bool{}
	for _, id := range taskIDs {
		want[id+".md"] = true
	}
	pack.Tasks = map[string]string{}
	for _, entry := range entries {
		if !want[entry.Name()] {
			return pack, fmt.Errorf("plan pack: %s/%s belongs to no task", marshalPackTasks, entry.Name())
		}
		note, err := readPackFile(dir, filepath.Join(marshalPackTasks, entry.Name()))
		if err != nil {
			return pack, err
		}
		pack.Tasks[strings.TrimSuffix(entry.Name(), ".md")] = note
	}
	for _, id := range taskIDs {
		if _, ok := pack.Tasks[id]; !ok {
			return pack, fmt.Errorf("plan pack: task %s has no %s/%s.md", id, marshalPackTasks, id)
		}
	}
	pack.Digest = planPackDigest(pack)
	return pack, nil
}

// readPackFile accepts only a regular, non-empty file of bounded size: a
// symlink could otherwise pull any readable file into a worker's brief.
func readPackFile(dir, name string) (string, error) {
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("plan pack: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("plan pack: %s is not a regular file", name)
	}
	if info.Size() > marshalPackFileLimit {
		return "", fmt.Errorf("plan pack: %s is larger than %d bytes", name, marshalPackFileLimit)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("plan pack: %w", err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", fmt.Errorf("plan pack: %s is empty", name)
	}
	return string(data), nil
}

func planPackDigest(pack marshal.PlanPack) string {
	ids := make([]string, 0, len(pack.Tasks))
	for id := range pack.Tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	notes := make([][2]string, 0, len(ids))
	for _, id := range ids {
		notes = append(notes, [2]string{id, pack.Tasks[id]})
	}
	data, _ := json.Marshal(struct {
		Requirements string
		Index        string
		Tasks        [][2]string
	}{pack.Requirements, pack.Index, notes})
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// refreshPlanPack validates and copies the proposal-time SQLite snapshot. The
// run passed here is loaded from the canonical store by approval and review
// paths. Source Markdown is deliberately not re-read: edits after proposal
// cannot alter the revision being reviewed or approved.
func (s *MarshalService) refreshPlanPack(_ string, run marshal.Run) (*marshal.PlanPack, error) {
	if run.Pack == nil {
		return nil, nil
	}
	pack := *run.Pack
	pack.Tasks = make(map[string]string, len(run.Pack.Tasks))
	for id, note := range run.Pack.Tasks {
		pack.Tasks[id] = note
	}
	if pack.Digest == "" || pack.Digest != planPackDigest(pack) {
		return nil, errors.New("plan pack: stored SQLite revision digest is invalid")
	}
	return &pack, nil
}
