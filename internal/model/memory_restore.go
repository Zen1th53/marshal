package model

// MemorySnapshotRecord identifies one memory record revision by digest.
type MemorySnapshotRecord struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
}
