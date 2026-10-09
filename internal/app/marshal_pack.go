package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// MarshalPackRelativePath is where an interactive Marshal writes the plan
// pack next to its task list.
const MarshalPackRelativePath = ".marshal/marshal/plan"

const (
	marshalPackRequirements = "REQUIREMENTS.md"
	marshalPackIndex        = "00_INDEX.md"
	marshalPackTasks        = "tasks"
	// A note is context for a worker, not a document store; the cap keeps a
	// brief within what a worker session reads.
	marshalPackFileLimit = 64 << 10
)

// MarshalRunPackPath is where a run's plan pack is kept once the runtime has
// taken it: the person reads and may edit it there until approval.
func MarshalRunPackPath(root, runID string) string {
	return filepath.Join(root, ".marshal", "marshal", "runs", runID, "plan")
}

// TakePlanPack moves the pack the Marshal wrote to the run's own directory,
// so a later Marshal session starts without it and a rejected pack is kept
// for inspection rather than read twice.
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
	if err := os.Rename(from, to); err != nil {
		return "", err
	}
	return to, nil
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

// refreshPlanPack reads the run's pack for a review snapshot and checks at
// approval that its bytes still match the reviewed digest.
// Only the notes of the original tasks are required: an amendment may have
// added tasks without one.
func (s *MarshalService) refreshPlanPack(runID string, run marshal.Run) (*marshal.PlanPack, error) {
	if run.Pack == nil {
		return nil, nil
	}
	ids := make([]string, 0, len(run.Pack.Tasks))
	for id := range run.Pack.Tasks {
		ids = append(ids, id)
	}
	pack, err := ReadPlanPack(MarshalRunPackPath(s.Repository, runID), ids)
	if err != nil {
		return nil, err
	}
	return &pack, nil
}
