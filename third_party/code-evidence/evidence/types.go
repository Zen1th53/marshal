// Package evidence is the public Go API of Code Evidence Context.
//
// A host (or the bundled CLI/MCP server) opens an Engine bound to one
// repository root, one policy and a set of build configurations, then:
//
//	Provide  – build a deterministic, budgeted dossier plus an Evidence Receipt
//	Expand   – zoom into evidence referenced by a receipt (bounded, paged)
//	Validate – re-check receipts against a candidate tree: spans and predicates
//	Forget   – revoke a blob so receipts that used it validate as unknown
//
// Every output under a policy is a function of the policy-visible snapshot
// only (noninterference). See docs/SPEC.md.
package evidence

import (
	"context"

	"github.com/Zen1th53/code-evidence/internal/goanalyzer"
	"github.com/Zen1th53/code-evidence/internal/policy"
)

// Version identifies this module build in receipts.
const Version = "code-evidence/0.1.0-dev"

// ReceiptFormat is the receipt schema identifier.
const ReceiptFormat = "cec/receipt/v1"

// RecipeVersion identifies the dossier recipe (selection and budgeting).
const RecipeVersion = "spans+predicates+map/2"

// Re-exported building blocks.
type (
	// Policy is a compiled visibility policy (allow/deny globs, size cap,
	// redaction hook) with a stable PolicyID.
	Policy = policy.Policy
	// PolicySpec is the serializable policy description.
	PolicySpec = policy.Spec
	// Redactor is the redaction hook interface.
	Redactor = policy.Redactor
	// Config is a named Go build configuration.
	Config = goanalyzer.Config
	// Capability is the analyzer capability declaration.
	Capability = goanalyzer.Capability
	// Coverage reports analyzed vs failed files.
	Coverage = goanalyzer.Coverage
	// Item is one element of a predicate result.
	Item = goanalyzer.Item
)

// NewPolicy compiles a policy (nil redactor = default secret redactor).
func NewPolicy(spec PolicySpec, r Redactor) (*Policy, error) { return policy.New(spec, r) }

// DefaultPolicy returns the default policy.
func DefaultPolicy() *Policy { return policy.Default() }

// ParseConfig parses "name=goos/goarch[,opt...]".
func ParseConfig(s string) (Config, error) { return goanalyzer.ParseConfig(s) }

// DefaultConfigs returns the default build configuration list.
func DefaultConfigs() []Config { return goanalyzer.DefaultConfigs() }

// Capabilities returns the Go tier-1 analyzer capability declaration.
func Capabilities() Capability { return goanalyzer.Capabilities() }

// Provider builds dossiers (push mode: called by the host at dispatch;
// pull mode: exposed as the code_context tool).
type Provider interface {
	Provide(ctx context.Context, req ProvideRequest) (*Dossier, error)
}

// Expander serves bounded expansions of receipt evidence.
type Expander interface {
	Expand(ctx context.Context, req ExpandRequest) (*Expansion, error)
}

// Validator re-checks receipts against a candidate tree.
type Validator interface {
	Validate(ctx context.Context, req ValidateRequest) (*ValidationReport, error)
}

// Forgetter revokes blobs.
type Forgetter interface {
	Forget(ctx context.Context, blob string) (*ForgetReport, error)
}

// Host is the full host-function surface.
type Host interface {
	Provider
	Expander
	Validator
	Forgetter
}

// Budget limits (bytes of model-facing text).
const (
	DefaultContextBytes  = 8 << 10
	MaxContextBytes      = 24 << 10
	DefaultExpandBytes   = 6 << 10
	MaxExpandBytes       = 16 << 10
	DefaultValidateBytes = 2 << 10
	MaxValidateBytes     = 8 << 10
	MaxRecords           = 100
)

// ProvideRequest asks for a dossier.
type ProvideRequest struct {
	Task string `json:"task"`
	// Budget is the maximum dossier text size in bytes (default 8 KiB,
	// capped at 24 KiB).
	Budget int `json:"budget,omitempty"`
	// MaxRecords caps dossier entries (default and maximum 100).
	MaxRecords int `json:"max_records,omitempty"`
	// Configs overrides the engine's configurations for this request.
	Configs []Config `json:"configs,omitempty"`
	// CoChange is an optional co-change prior (experimental; see CoChange).
	CoChange *CoChange `json:"cochange,omitempty"`
	// Recipe selects the layout: "" or "dossier" (spans+predicates+map/2,
	// the default) or "locator" (locator/1: ranked locations only, chosen
	// by the cost policy of Profile; docs/COST-MODEL.md).
	Recipe string `json:"recipe,omitempty"`
	// Profile names the cost profile for the locator recipe ("" = opus).
	Profile string `json:"profile,omitempty"`
}

// CoChange is a co-change prior: the changed file sets of past commits.
// The host derives it from history strictly older than the snapshot (the
// oracle uses ancestors of the task's parent commit, excluding the parent
// itself). The engine drops paths not visible under the policy, records
// the digest of what remains in the receipt (task.cochange_sha256), and
// uses it to boost files that changed together with the top candidates.
type CoChange struct {
	Commits [][]string `json:"commits"`
}

// Dossier is the bounded rendering of a receipt.
type Dossier struct {
	Text      string    `json:"text"`
	Receipt   *Receipt  `json:"receipt"`
	Entries   []Entry   `json:"entries"`
	FreeText  []Pointer `json:"free_text_only,omitempty"`
	Truncated bool      `json:"truncated"`
	Omitted   int       `json:"omitted"`
	Bytes     int       `json:"bytes"`
	Coverage  Coverage  `json:"coverage"`
}

// Entry is one pushed declaration.
type Entry struct {
	Rank      int      `json:"rank"`
	Symbol    string   `json:"symbol"`
	Kind      string   `json:"kind"`
	Path      string   `json:"path"`
	StartLine int      `json:"start_line"`
	EndLine   int      `json:"end_line"`
	Score     string   `json:"score"`
	Why       []string `json:"why"`
	Form      string   `json:"form"` // "full", "clipped", "skeleton" or "signature" (map line)
	Configs   []string `json:"configs,omitempty"`
}

// Pointer is a free-text-only match: listed, never pushed as a span.
type Pointer struct {
	Path   string   `json:"path"`
	Line   int      `json:"line"`
	Symbol string   `json:"symbol,omitempty"`
	Terms  []string `json:"terms"`
}

// ExpandRequest asks for evidence behind a receipt.
type ExpandRequest struct {
	Receipt string `json:"receipt,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
	// Select is a symbol id, "path:L1-L2", "#N" (receipt span N) or empty
	// (overview of the receipt's spans).
	Select string `json:"select,omitempty"`
	// View is "source" (default), "callers", "callees", "references" or "tests".
	View       string `json:"view,omitempty"`
	Budget     int    `json:"budget,omitempty"`
	MaxRecords int    `json:"max_records,omitempty"`
}

// Expansion is a bounded slice of evidence.
type Expansion struct {
	Text       string   `json:"text"`
	Receipt    string   `json:"receipt"`
	SnapshotID string   `json:"snapshot_id"`
	Records    []Record `json:"records,omitempty"`
	// Changed lists receipt paths whose captured bytes differ now.
	Changed   []string `json:"changed_since_receipt,omitempty"`
	Cursor    string   `json:"cursor,omitempty"`
	Truncated bool     `json:"truncated"`
	Bytes     int      `json:"bytes"`
	// Delivery is the ledger entry recording this expansion.
	Delivery string `json:"delivery"`
}

// Record is one structured expansion record.
type Record struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
	Blob      string `json:"blob,omitempty"`
}

// ValidateRequest re-checks receipts. Head is a directory holding the
// exact candidate merge tree ("" = the engine root).
type ValidateRequest struct {
	Receipts []string `json:"receipts"`
	Head     string   `json:"head,omitempty"`
	// Bodies may carry receipts not present in the store (JSON). They are
	// always validated by full re-analysis: their recorded coverage is
	// not trusted.
	Bodies [][]byte `json:"-"`
	// Full forces a full re-analysis of the head instead of the
	// output-sensitive path (audits and differential tests).
	Full bool `json:"full,omitempty"`
}

// Span verdicts.
const (
	SpanUnchanged = "unchanged"
	SpanChanged   = "changed"
	SpanRemoved   = "removed"
	SpanUnknown   = "unknown"
)

// Predicate verdicts.
const (
	PredHolds       = "holds"
	PredInvalidated = "invalidated"
	PredUnknown     = "unknown"
)

// ValidationReport carries per-span and per-predicate verdicts.
type ValidationReport struct {
	Head     HeadRef          `json:"head"`
	Receipts []ReceiptVerdict `json:"receipts"`
	Text     string           `json:"text"`
}

// HeadRef identifies the validated tree under the validator's policy.
type HeadRef struct {
	SnapshotID string   `json:"snapshot_id"`
	Universe   string   `json:"universe"`
	Files      int      `json:"files"`
	Coverage   Coverage `json:"coverage"`
}

// ReceiptVerdict is the result for one receipt.
type ReceiptVerdict struct {
	Receipt    string         `json:"receipt"`
	Status     string         `json:"status"` // "checked", "not_found", "policy_mismatch", "evidence_revoked"
	Spans      []SpanVerdict  `json:"spans"`
	Predicates []PredVerdict  `json:"predicates"`
	Summary    VerdictSummary `json:"summary"`
}

// VerdictSummary counts verdicts.
type VerdictSummary struct {
	Spans      map[string]int `json:"spans"`
	Predicates map[string]int `json:"predicates"`
}

// SpanVerdict is the verdict for one delivered span.
type SpanVerdict struct {
	Index   int    `json:"index"`
	Path    string `json:"path"`
	Symbol  string `json:"symbol,omitempty"`
	Verdict string `json:"verdict"`
	Detail  string `json:"detail,omitempty"`
	NowLine int    `json:"now_line,omitempty"`
}

// PredVerdict is the verdict for one delivered predicate.
type PredVerdict struct {
	Index    int      `json:"index"`
	Relation string   `json:"relation"`
	Subject  string   `json:"subject"`
	Verdict  string   `json:"verdict"`
	Reason   string   `json:"reason,omitempty"`
	Anchors  []Anchor `json:"anchors,omitempty"`
}

// Anchor is a counterexample location.
type Anchor struct {
	Change string `json:"change"` // "added", "removed", "new_method"
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Symbol string `json:"symbol,omitempty"`
}

// ForgetReport describes a revocation and its purge.
type ForgetReport struct {
	Blob                string       `json:"blob"`
	Revoked             bool         `json:"revoked"`
	ReceiptsInvalidated []string     `json:"receipts_invalidated"`
	PinsRevoked         int          `json:"pins_revoked"`
	Purged              bool         `json:"purged"`
	Purge               *PurgeReport `json:"purge,omitempty"`
	Note                string       `json:"note"`
}

// PurgeReport counts what a purge physically removed.
type PurgeReport struct {
	Receipts     int  `json:"receipts"`
	Deliveries   int  `json:"deliveries"`
	Generations  int  `json:"generations"`
	Artifacts    int  `json:"artifacts"`
	Vacuumed     bool `json:"vacuumed"`
	WALTruncated bool `json:"wal_truncated"`
}
