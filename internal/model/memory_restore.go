package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// MemoryRestoreSnapshot describes a bounded set of project records. Payloads
// are authenticated ciphertext stored separately; no record body is displayed
// or copied into command receipts. Other records are preserved by restoration.
type MemoryRestoreSnapshot struct {
	ID           string                 `json:"id"`
	ProjectID    string                 `json:"project_id"`
	Name         string                 `json:"name"`
	PolicyDigest string                 `json:"policy_digest"`
	StateDigest  string                 `json:"state_digest"`
	Records      []MemorySnapshotRecord `json:"records"`
	CreatedAt    time.Time              `json:"created_at"`
	Digest       string                 `json:"digest"`
}
type MemorySnapshotRecord struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
}

func (s *MemoryRestoreSnapshot) Seal() {
	s.Digest = ""
	body, _ := json.Marshal(s)
	sum := sha256.Sum256(body)
	s.Digest = hex.EncodeToString(sum[:])
}
func (s MemoryRestoreSnapshot) Valid() bool {
	d := s.Digest
	s.Seal()
	return d != "" && d == s.Digest && s.ID != "" && s.ProjectID != "" && s.Name != "" && s.PolicyDigest != "" && s.StateDigest != "" && !s.CreatedAt.IsZero() && len(s.Records) > 0 && len(s.Records) <= 128
}

type MemoryRestoreState struct {
	ProjectID  string           `json:"project_id"`
	Generation int64            `json:"generation"`
	Digest     string           `json:"digest"`
	Records    []MemoryRecordV2 `json:"-"`
}
type MemoryRestoreResult struct {
	SnapshotID       string                 `json:"snapshot_id"`
	SnapshotDigest   string                 `json:"snapshot_digest"`
	Generation       int64                  `json:"generation"`
	StateDigest      string                 `json:"state_digest"`
	Restored         []MemorySnapshotRecord `json:"restored"`
	ProjectionStatus string                 `json:"projection_status"`
}
