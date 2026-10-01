package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Snapshot states reported by VerifySnapshot. Only SnapshotIntact means the
// stored files were re-read and match the digest bound at capture.
const (
	SnapshotIntact     = "INTACT"
	SnapshotTampered   = "TAMPERED"
	SnapshotMissing    = "MISSING"
	SnapshotUndigest   = "NO_SNAPSHOT_DIGEST"
	SnapshotUnreadable = "UNREADABLE"
)

// SnapshotCheck is a checkpoint record with a fresh check of its files.
type SnapshotCheck struct {
	Record CheckpointRecord
	State  string
	Detail string
}

// VerifySnapshot validates the checkpoint's metadata binding, then re-digests
// its snapshot tree. It reads only; nothing is restored or repaired.
func (ce *CheckpointEngine) VerifySnapshot(checkpointID string) (SnapshotCheck, error) {
	rec, err := ce.GetCheckpoint(checkpointID)
	if err != nil {
		return SnapshotCheck{}, err
	}
	check := SnapshotCheck{Record: rec}
	if rec.SnapshotDigest == "" {
		check.State = SnapshotUndigest
		return check, nil
	}
	if _, err := os.Lstat(rec.WorktreePath); errors.Is(err, os.ErrNotExist) {
		check.State = SnapshotMissing
		return check, nil
	}
	digest, err := digestSnapshot(rec.WorktreePath)
	switch {
	case err != nil:
		check.State, check.Detail = SnapshotUnreadable, err.Error()
	case digest != rec.SnapshotDigest:
		check.State = SnapshotTampered
	default:
		check.State = SnapshotIntact
	}
	return check, nil
}

// SnapshotDiff lists the files that differ between two snapshots.
type SnapshotDiff struct {
	From, To                string
	Added, Removed, Changed []string
	Truncated               bool
}

// DiffSnapshots compares two intact snapshots file by file. Both are verified
// first; a snapshot that is not intact is refused rather than compared. No
// external diff program runs, and at most limit paths are reported.
func (ce *CheckpointEngine) DiffSnapshots(fromID, toID string, limit int) (SnapshotDiff, error) {
	from, err := ce.VerifySnapshot(fromID)
	if err != nil {
		return SnapshotDiff{}, err
	}
	to, err := ce.VerifySnapshot(toID)
	if err != nil {
		return SnapshotDiff{}, err
	}
	for _, c := range []SnapshotCheck{from, to} {
		if c.State != SnapshotIntact {
			return SnapshotDiff{}, fmt.Errorf("%w: checkpoint %s is %s and cannot be compared", ErrCheckpointFailed, c.Record.CheckpointID, c.State)
		}
	}
	a, err := snapshotFiles(from.Record.WorktreePath)
	if err != nil {
		return SnapshotDiff{}, err
	}
	b, err := snapshotFiles(to.Record.WorktreePath)
	if err != nil {
		return SnapshotDiff{}, err
	}
	diff := SnapshotDiff{From: fromID, To: toID}
	add := func(list *[]string, path string) {
		if len(diff.Added)+len(diff.Removed)+len(diff.Changed) >= limit {
			diff.Truncated = true
			return
		}
		*list = append(*list, path)
	}
	for _, path := range sortedKeys(b) {
		if old, ok := a[path]; !ok {
			add(&diff.Added, path)
		} else if old != b[path] {
			add(&diff.Changed, path)
		}
	}
	for _, path := range sortedKeys(a) {
		if _, ok := b[path]; !ok {
			add(&diff.Removed, path)
		}
	}
	return diff, nil
}

// snapshotFiles maps each regular file's relative path to its mode, size and
// content digest. Snapshots only ever contain directories and regular files.
func snapshotFiles(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported snapshot entry %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		files[filepath.ToSlash(rel)] = fmt.Sprintf("%o:%d:%s", info.Mode().Perm(), info.Size(), hex.EncodeToString(h.Sum(nil)))
		return nil
	})
	return files, err
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
