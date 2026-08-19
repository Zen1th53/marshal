package versioning

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type SnapshotRecordInfo struct {
	Revision      int64  `json:"revision"`
	ContentDigest string `json:"content_digest"`
}

type Snapshot struct {
	SnapshotID     string                        `json:"snapshot_id"`
	ProjectID      string                        `json:"project_id"`
	Name           string                        `json:"name"`
	ManifestDigest string                        `json:"manifest_digest"`
	Records        map[string]SnapshotRecordInfo `json:"records"`
	CreatedAt      time.Time                     `json:"created_at"`
}

type SnapshotDiff struct {
	Added          []string `json:"added"`
	Removed        []string `json:"removed"`
	Modified       []string `json:"modified"`
	UnchangedCount int      `json:"unchanged_count"`
}

type Manager struct {
	mu        sync.RWMutex
	snapshots map[string]*Snapshot
}

func NewManager() *Manager {
	return &Manager{
		snapshots: make(map[string]*Snapshot),
	}
}

func generateSnapshotID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("SNAP-%s", hex.EncodeToString(b[:]))
}

// CreateSnapshot captures an immutable point-in-time manifest of canonical memory records and computes a stable digest.
func (m *Manager) CreateSnapshot(ctx context.Context, projectID, name string, records []model.MemoryRecordV2) (Snapshot, error) {
	if projectID == "" || name == "" {
		return Snapshot{}, errors.New("project ID and name required")
	}

	recordMap := make(map[string]SnapshotRecordInfo)
	var sortedIDs []string

	for _, r := range records {
		recordMap[r.ID] = SnapshotRecordInfo{
			Revision:      r.Revision,
			ContentDigest: r.ContentDigest,
		}
		sortedIDs = append(sortedIDs, r.ID)
	}

	sort.Strings(sortedIDs)

	// Calculate deterministic manifest digest
	h := sha256.New()
	for _, id := range sortedIDs {
		info := recordMap[id]
		fmt.Fprintf(h, "%s:%d:%s\n", id, info.Revision, info.ContentDigest)
	}
	manifestDigest := hex.EncodeToString(h.Sum(nil))

	snap := Snapshot{
		SnapshotID:     generateSnapshotID(),
		ProjectID:      projectID,
		Name:           name,
		ManifestDigest: manifestDigest,
		Records:        recordMap,
		CreatedAt:      time.Now().UTC(),
	}

	m.mu.Lock()
	m.snapshots[snap.SnapshotID] = &snap
	m.mu.Unlock()

	return snap, nil
}

// DiffSnapshots compares two snapshots and outputs added, removed, modified, and unchanged record IDs.
func (m *Manager) DiffSnapshots(a, b Snapshot) SnapshotDiff {
	var diff SnapshotDiff

	for id, infoB := range b.Records {
		infoA, exists := a.Records[id]
		if !exists {
			diff.Added = append(diff.Added, id)
		} else if infoA.Revision != infoB.Revision || infoA.ContentDigest != infoB.ContentDigest {
			diff.Modified = append(diff.Modified, id)
		} else {
			diff.UnchangedCount++
		}
	}

	for id := range a.Records {
		if _, exists := b.Records[id]; !exists {
			diff.Removed = append(diff.Removed, id)
		}
	}

	sort.Strings(diff.Added)
	sort.Strings(diff.Removed)
	sort.Strings(diff.Modified)

	return diff
}
