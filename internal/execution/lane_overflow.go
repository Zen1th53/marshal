package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// laneSummaryLimit bounds the encoded snapshot of a task lane. A lane's
// append-only history (checkpoints, evidence, claims, provider calls,
// failures, handoffs) grows with every attempt; past the limit its older part
// moves into immutable, content-addressed segments beside the snapshot, so
// the file rewritten on every update stays small. The in-memory run is never
// compacted, and reopening the store restores the exact history.
const laneSummaryLimit = 128 << 10

// laneOverflowMinimum keeps a lane whose mutable state alone is near the
// limit from writing a tiny segment on every update.
const laneOverflowMinimum = laneSummaryLimit / 2

// laneOverflowDir holds the segments. It is a directory, so the store's
// loader, which reads only regular *.json files, never mistakes a segment for
// a run.
const laneOverflowDir = "lane-overflow"

// laneHistory is the append-only part of a run, and the content of one
// overflow segment.
type laneHistory struct {
	Checkpoints     []CheckpointRecord  `json:"checkpoints,omitempty"`
	EvidenceRecords []ExecutionEvidence `json:"evidence_records,omitempty"`
	ClaimRecords    []ExecutionClaim    `json:"claim_records,omitempty"`
	Evidence        []EvidenceRef       `json:"evidence,omitempty"`
	ProviderCalls   []ProviderCallMeta  `json:"provider_calls,omitempty"`
	Failures        []RunFailure        `json:"failures,omitempty"`
	Handoffs        []TypedHandoff      `json:"handoffs,omitempty"`
}

// laneCounts is the number of entries of each laneHistory list, in field
// order.
type laneCounts [7]int

// laneOverflowRef names one segment by the SHA-256 of its exact bytes and
// the number of entries it holds of each list. Segments apply in order, each
// continuing where the previous one ended.
type laneOverflowRef struct {
	Digest string     `json:"digest"`
	Counts laneCounts `json:"counts"`
}

// storedRun is a run as written to disk: the run itself, whose history lists
// hold only the entries after its segments, and the segment references.
type storedRun struct {
	ExecutionRun
	LaneOverflow []laneOverflowRef `json:"lane_overflow,omitempty"`
}

func historyOf(run ExecutionRun) laneHistory {
	return laneHistory{run.Checkpoints, run.EvidenceRecords, run.ClaimRecords, run.Evidence, run.ProviderCalls, run.Failures, run.Handoffs}
}

func setHistory(run *ExecutionRun, h laneHistory) {
	run.Checkpoints, run.EvidenceRecords, run.ClaimRecords, run.Evidence = h.Checkpoints, h.EvidenceRecords, h.ClaimRecords, h.Evidence
	run.ProviderCalls, run.Failures, run.Handoffs = h.ProviderCalls, h.Failures, h.Handoffs
}

func (h laneHistory) counts() laneCounts {
	return laneCounts{len(h.Checkpoints), len(h.EvidenceRecords), len(h.ClaimRecords), len(h.Evidence), len(h.ProviderCalls), len(h.Failures), len(h.Handoffs)}
}

func span[T any](s []T, from, to int) []T {
	if from >= to {
		return nil
	}
	return s[from:to:to]
}

// between is the part of h from one set of offsets to another.
func (h laneHistory) between(from, to laneCounts) laneHistory {
	return laneHistory{
		span(h.Checkpoints, from[0], to[0]),
		span(h.EvidenceRecords, from[1], to[1]),
		span(h.ClaimRecords, from[2], to[2]),
		span(h.Evidence, from[3], to[3]),
		span(h.ProviderCalls, from[4], to[4]),
		span(h.Failures, from[5], to[5]),
		span(h.Handoffs, from[6], to[6]),
	}
}

func (h *laneHistory) extend(more laneHistory) {
	h.Checkpoints = append(h.Checkpoints, more.Checkpoints...)
	h.EvidenceRecords = append(h.EvidenceRecords, more.EvidenceRecords...)
	h.ClaimRecords = append(h.ClaimRecords, more.ClaimRecords...)
	h.Evidence = append(h.Evidence, more.Evidence...)
	h.ProviderCalls = append(h.ProviderCalls, more.ProviderCalls...)
	h.Failures = append(h.Failures, more.Failures...)
	h.Handoffs = append(h.Handoffs, more.Handoffs...)
}

func (c laneCounts) plus(d laneCounts) laneCounts {
	for i := range c {
		c[i] += d[i]
	}
	return c
}

func (c laneCounts) within(limit laneCounts) bool {
	for i := range c {
		if c[i] > limit[i] {
			return false
		}
	}
	return true
}

func segmentDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// encodeLane returns the bytes to write for a lane and the segment references
// they carry. Existing segments are kept while they are still an exact prefix
// of the history; a history rewritten under them starts over, so a segment
// never stands for entries the run no longer holds. A new segment is written
// and synced before the snapshot that references it.
func (f *FileRunStore) encodeLane(run ExecutionRun, refs []laneOverflowRef) ([]byte, []laneOverflowRef, error) {
	full := historyOf(run)
	total := full.counts()
	var at laneCounts
	var kept []laneOverflowRef
	for _, ref := range refs {
		next := at.plus(ref.Counts)
		if !next.within(total) {
			break
		}
		raw, err := json.Marshal(full.between(at, next))
		if err != nil || segmentDigest(raw) != ref.Digest {
			break
		}
		kept, at = append(kept, ref), next
	}
	encode := func(from laneCounts, refs []laneOverflowRef) ([]byte, error) {
		tail := run
		setHistory(&tail, full.between(from, total))
		return json.MarshalIndent(storedRun{ExecutionRun: tail, LaneOverflow: refs}, "", "  ")
	}
	data, err := encode(at, kept)
	if err != nil || len(data) <= laneSummaryLimit {
		return data, kept, err
	}
	segment := full.between(at, total)
	raw, err := json.Marshal(segment)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) < laneOverflowMinimum {
		// Too little history to move: the mutable state itself is large.
		// Identity, bindings, tasks and leases are never truncated.
		return data, kept, nil
	}
	ref := laneOverflowRef{Digest: segmentDigest(raw), Counts: segment.counts()}
	if err := f.writeLaneSegment(ref.Digest, raw); err != nil {
		return nil, nil, err
	}
	kept = append(kept, ref)
	data, err = encode(total, kept)
	return data, kept, err
}

// writeLaneSegment durably writes one immutable segment under its digest.
func (f *FileRunStore) writeLaneSegment(digest string, raw []byte) error {
	dir := filepath.Join(f.baseDir, laneOverflowDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create lane overflow directory: %w", err)
	}
	return writeRunSnapshot(filepath.Join(dir, digest+".json"), raw)
}

// rehydrateLane restores a stored lane's full history from its segments. A
// missing or altered segment fails the load: a lane is never reopened with
// part of its evidence silently gone.
func (f *FileRunStore) rehydrateLane(stored storedRun) (ExecutionRun, error) {
	var history laneHistory
	for _, ref := range stored.LaneOverflow {
		raw, err := os.ReadFile(filepath.Join(f.baseDir, laneOverflowDir, filepath.Base(ref.Digest)+".json"))
		if err != nil {
			return ExecutionRun{}, fmt.Errorf("lane %s history segment %s unreadable: %w", stored.RunID, ref.Digest, err)
		}
		if segmentDigest(raw) != ref.Digest {
			return ExecutionRun{}, fmt.Errorf("lane %s history segment %s does not match its digest", stored.RunID, ref.Digest)
		}
		var segment laneHistory
		if err := json.Unmarshal(raw, &segment); err != nil || segment.counts() != ref.Counts {
			return ExecutionRun{}, fmt.Errorf("lane %s history segment %s is malformed", stored.RunID, ref.Digest)
		}
		history.extend(segment)
	}
	run := stored.ExecutionRun
	history.extend(historyOf(run))
	setHistory(&run, history)
	return run, nil
}
