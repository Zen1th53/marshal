package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Receipt is the Evidence Receipt ρ = (S_P, U_P, P, C, V, D_span, D_pred, t)
// of SPEC §1. It holds structure and hashes only, never source text.
type Receipt struct {
	Format     string        `json:"format"`
	ID         string        `json:"id"`
	ObservedAt string        `json:"observed_at"` // t (past tense)
	Snapshot   SnapshotRef   `json:"snapshot"`    // S_P and U_P
	PolicyID   string        `json:"policy_id"`   // P
	Config     ConfigRecord  `json:"config"`      // C
	Versions   VersionVector `json:"versions"`    // V
	Task       TaskRecord    `json:"task"`
	ReadSet    []FileRef     `json:"read_set"`   // files whose bytes were delivered
	Spans      []Span        `json:"spans"`      // D_span
	Predicates []Predicate   `json:"predicates"` // D_pred
}

// SnapshotRef commits to the policy-visible snapshot and search universe.
type SnapshotRef struct {
	ID       string `json:"id"`       // Merkle root over (path, sha256), policy-scoped
	Universe string `json:"universe"` // Merkle root over sorted paths, policy-scoped
	Files    int    `json:"files"`
	Mode     string `json:"enumeration"`
}

// ConfigRecord is the requested and effective build configuration.
type ConfigRecord struct {
	Requested []Config `json:"requested"`
	Effective []Config `json:"effective"`
	Mismatch  []string `json:"mismatch"`
}

// VersionVector pins every component that shaped the output.
type VersionVector struct {
	Module   string `json:"module"`
	Analyzer string `json:"analyzer"`
	Recipe   string `json:"recipe"`
	Ranker   string `json:"ranker"`
	Renderer string `json:"renderer"`
	Redactor string `json:"redactor"`
	Lexical  string `json:"lexical"`
}

// TaskRecord identifies the request without storing its text.
type TaskRecord struct {
	SHA256     string `json:"sha256"`
	Budget     int    `json:"budget"`
	MaxRecords int    `json:"max_records"`
	// CoChange is the digest of the policy-filtered co-change prior, when
	// one was used.
	CoChange string `json:"cochange_sha256,omitempty"`
}

// FileRef is a (path, blob) pair.
type FileRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Span is a delivered byte interval of a captured blob.
type Span struct {
	Path      string `json:"path"`
	Blob      string `json:"blob"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Symbol    string `json:"symbol,omitempty"`
	// RelStart is Start minus the symbol's base offset (doc comment start,
	// or decl start), used to re-locate the span after unrelated edits.
	RelStart int    `json:"rel_start"`
	Content  string `json:"content_sha256"`
	Form     string `json:"form"`
}

// Predicate is a delivered structural claim.
type Predicate struct {
	Relation string   `json:"relation"`
	Subject  string   `json:"subject"`
	Method   string   `json:"method"`
	Bound    string   `json:"bound"`
	Coverage Coverage `json:"coverage"`
	// Items holds up to maxStoredItems item identities; ItemCount is the
	// full size and ResultDigest covers the full set.
	Items          []StoredItem `json:"items"`
	ItemCount      int          `json:"item_count"`
	ItemsTruncated bool         `json:"items_truncated,omitempty"`
	ResultDigest   string       `json:"result_digest"`
	Deps           string       `json:"deps,omitempty"`
	// Delivered is how many items were shown in the dossier text.
	Delivered int `json:"delivered"`
	// Withheld counts items removed because their file was forgotten;
	// such a predicate is not the full relation and validates as unknown.
	Withheld int `json:"withheld,omitempty"`
}

// StoredItem is the compact identity of a predicate item kept in a
// receipt (structure only).
type StoredItem struct {
	Key  string `json:"k"`
	Path string `json:"p,omitempty"`
	Line int    `json:"l"`
}

const maxStoredItems = 48

// seal computes the receipt id over the canonical JSON with an empty id.
func (r *Receipt) seal() error {
	r.ID = ""
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	r.ID = "rcpt_" + hex.EncodeToString(sum[:16])
	return nil
}

// Verify recomputes the id and reports whether the receipt is intact.
func (r *Receipt) Verify() error {
	c := *r
	if err := c.seal(); err != nil {
		return err
	}
	if c.ID != r.ID {
		return fmt.Errorf("evidence: receipt id mismatch (have %s, computed %s)", r.ID, c.ID)
	}
	return nil
}

// Blobs returns the distinct blob hashes referenced by the receipt.
func (r *Receipt) Blobs() []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range r.ReadSet {
		if !seen[f.SHA256] {
			seen[f.SHA256] = true
			out = append(out, f.SHA256)
		}
	}
	return out
}

// ParseReceipt decodes and verifies a receipt.
func ParseReceipt(b []byte) (*Receipt, error) {
	var r Receipt
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("evidence: bad receipt: %v", err)
	}
	if r.Format != ReceiptFormat {
		return nil, fmt.Errorf("evidence: unsupported receipt format %q", r.Format)
	}
	if err := r.Verify(); err != nil {
		return nil, err
	}
	return &r, nil
}
