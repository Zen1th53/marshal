// Package projectcontext defines deterministic project contracts and mandatory
// context values. These values convey data, never live acceptance or execution
// authority. Runtime authorization and canonical transactions remain elsewhere.
package projectcontext

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrInvalid       = errors.New("required context invalid")
	ErrMissing       = errors.New("REQUIRED_CONTEXT_MISSING")
	ErrChanged       = errors.New("REQUIRED_CONTEXT_CHANGED")
	ErrReviewPending = errors.New("REQUIRED_CONTEXT_REVIEW_PENDING")
	ErrBudget        = errors.New("REQUIRED_CONTEXT_BUDGET_EXCEEDED")
)

const (
	FormatVersion = 1
	// MaximumPacketBytes is a defensive upper bound, not the selected runtime
	// default. The complete provider request needs its own measured envelope cap.
	// It stays below Linux's 128 KiB single-argument limit, because some
	// providers receive the packet as one command-line argument.
	MaximumPacketBytes = 112 << 10
	// Classification is a one-time bounded proposal input. Inspection replies
	// keep their separate 4 KiB wire cap, and provider context keeps its own cap.
	MaximumContractSubmissionBytes = 64 << 10
	Policy                         = "policy"
	Constraint                     = "constraint"
	CriticalDecision               = "critical_decision"
	AdditionalFact                 = "additional_fact"
	ContextOnly                    = "context_only"
	packetHeader                   = "=== MARSHAL REQUIRED PROJECT CONTEXT ===\n"
)

type ProjectBinding struct {
	CanonicalID string `json:"canonical_id"`
	StorageID   string `json:"storage_id"`
}
type ContractRef struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
}
type Component struct {
	ID             string   `json:"id"`
	Responsibility string   `json:"responsibility"`
	Paths          []string `json:"paths"`
	Dependencies   []string `json:"dependencies,omitempty"`
}
type Applicability struct {
	ProjectWide bool     `json:"project_wide,omitempty"`
	TaskIDs     []string `json:"task_ids,omitempty"`
	Components  []string `json:"components,omitempty"`
	Paths       []string `json:"paths,omitempty"`
}
type Selection struct {
	CanonicalTaskID   string
	TaskID            string
	Components, Paths []string
	Mode              SelectionMode `json:"mode,omitempty"`
}

// SelectionMode changes data coverage, never execution/owner authority. Its
// zero value preserves the original scoped worker JSON and packet behavior.
type SelectionMode string

const SelectionPlanning SelectionMode = "planning"

// PlanningSelection derives complete project coverage from a canonical
// contract value. The caller must establish actual acceptance separately.
func PlanningSelection(c Contract) (Selection, error) {
	if _, err := c.Canonical(); err != nil {
		return Selection{}, err
	}
	return Selection{Mode: SelectionPlanning}, nil
}

func validContextSelection(s Selection) bool {
	switch s.Mode {
	case "":
		return true
	case SelectionPlanning:
		return s.TaskID == "" && s.CanonicalTaskID == "" && len(s.Components) == 0 && len(s.Paths) == 0
	default:
		return false
	}
}

type MemoryRef struct {
	Scope                string        `json:"scope"`
	ScopeID              string        `json:"scope_id"`
	ID                   string        `json:"id"`
	ProjectID            string        `json:"project_id"`
	Revision             int64         `json:"revision"`
	ContentDigest        string        `json:"content_digest"`
	StateFingerprint     string        `json:"state_fingerprint"`
	Class                string        `json:"class"`
	Applies              Applicability `json:"applies"`
	AcceptanceOccurrence string        `json:"acceptance_occurrence"`
	Reason               string        `json:"reason"`
	// Normative facts are binding through accepted policy. Empirical premises
	// require verified confidence/evidence and a current freshness decision.
	EvidenceRequired          bool  `json:"evidence_required,omitempty"`
	MaxVerificationAgeSeconds int64 `json:"max_verification_age_seconds,omitempty"`
}

func (r MemoryRef) Mandatory() bool { return r.Class != ContextOnly }

type Contract struct {
	FormatVersion           int            `json:"format_version"`
	Project                 ProjectBinding `json:"project"`
	ID                      string         `json:"id"`
	Revision                int64          `json:"revision"`
	Purpose                 string         `json:"purpose"`
	Components              []Component    `json:"components"`
	Invariants              []string       `json:"invariants"`
	ProhibitedChanges       []string       `json:"prohibited_changes,omitempty"`
	Required                []MemoryRef    `json:"required"`
	ReviewedInventoryDigest string         `json:"reviewed_inventory_digest"`
	SourceAnchors           []string       `json:"source_anchors,omitempty"`
	DecisionRefs            []string       `json:"decision_refs,omitempty"`
	SupersedesDigest        string         `json:"supersedes_digest,omitempty"`
	RevisionReason          string         `json:"revision_reason,omitempty"`
}

// Bindings embedded into common plan/run/package structs MUST be nil pointers
// with omitempty when absent. A zero value struct does not preserve legacy JSON.
type Binding struct {
	FormatVersion       int            `json:"format_version"`
	Project             ProjectBinding `json:"project"`
	Contract            ContractRef    `json:"contract"`
	InventoryDigest     string         `json:"inventory_digest"`
	ManifestDigest      string         `json:"manifest_digest"`
	RequiredStateDigest string         `json:"required_state_digest,omitempty"`
	GoalID              string         `json:"goal_id"`
	GoalRevision        int64          `json:"goal_revision"`
	PlanID              string         `json:"plan_id,omitempty"`
	PlanVersion         int64          `json:"plan_version,omitempty"`
	ApprovalScopeDigest string         `json:"approval_scope_digest,omitempty"`
	RunID               string         `json:"run_id,omitempty"`
	RunVersion          int64          `json:"run_version,omitempty"`
	TaskID              string         `json:"task_id,omitempty"`
	CanonicalTaskID     string         `json:"canonical_task_id,omitempty"`
	Attempt             int            `json:"attempt,omitempty"`
	Provider            string         `json:"provider,omitempty"`
	Model               string         `json:"model,omitempty"`
	Principal           string         `json:"principal"`
	Session             string         `json:"session"`
	Base                string         `json:"base,omitempty"`
	Tree                string         `json:"tree,omitempty"`
}
type Goal struct {
	ID                     string             `json:"id"`
	Revision               int64              `json:"revision"`
	Outcome                string             `json:"outcome"`
	Constraints            []model.Constraint `json:"constraints"`
	DoNotDo                []string           `json:"do_not_do"`
	SuccessCriteria        []string           `json:"success_criteria"`
	ExpectedArtifact       string             `json:"expected_artifact,omitempty"`
	Scope                  []string           `json:"scope,omitempty"`
	RequiredCriticalClaims []string           `json:"required_critical_claims,omitempty"`
	OriginalRequest        string             `json:"original_request,omitempty"`
	BudgetLimits           *model.BudgetLimit `json:"budget_limits,omitempty"`
	// Canonical Goal metadata only: these stored values neither attest a
	// human identity nor grant execution, account access or contract acceptance.
	Confirmation    model.ConfirmationState `json:"confirmation,omitempty"`
	AuthoritySource string                  `json:"authority_source,omitempty"`
}

func GoalFromCanonical(g model.GoalContract) Goal {
	var limits *model.BudgetLimit
	if g.BudgetLimits != nil {
		copy := *g.BudgetLimits
		if g.BudgetLimits.MaxTotalTokens != nil {
			tokens := *g.BudgetLimits.MaxTotalTokens
			copy.MaxTotalTokens = &tokens
		}
		if g.BudgetLimits.MaxCostUSD != nil {
			cost := *g.BudgetLimits.MaxCostUSD
			copy.MaxCostUSD = &cost
		}
		limits = &copy
	}
	return Goal{OriginalRequest: g.OriginalRequest, BudgetLimits: limits, ID: g.ID, Revision: g.Revision, Outcome: g.DesiredOutcome, Constraints: append([]model.Constraint(nil), g.Constraints...), DoNotDo: append([]string(nil), g.DoNotDo...), SuccessCriteria: append([]string(nil), g.SuccessCriteria...), ExpectedArtifact: g.ExpectedArtifact, Scope: append([]string(nil), g.Scope...), RequiredCriticalClaims: append([]string(nil), g.RequiredCriticalClaims...), Confirmation: g.Confirmation, AuthoritySource: g.AuthoritySource}
}

type Manifest struct {
	Contract  Contract
	Goal      Goal
	Binding   Binding
	Selection Selection
	Records   []model.MemoryRecordV2
}
type LoadedReference struct {
	ID               string `json:"id"`
	ProjectID        string `json:"project_id"`
	Revision         int64  `json:"revision"`
	ContentDigest    string `json:"content_digest"`
	StateFingerprint string `json:"state_fingerprint"`
}
type Receipt struct {
	CurrentContract             ContractRef       `json:"current_contract"`
	TranslationDigest           string            `json:"translation_digest,omitempty"`
	ID                          string            `json:"id"`
	Stage                       string            `json:"stage"`
	ParentReceiptID             string            `json:"parent_receipt_id,omitempty"`
	Binding                     Binding           `json:"binding"`
	PacketDigest                string            `json:"packet_digest"`
	Loaded                      []LoadedReference `json:"loaded"`
	RequiredCount               int               `json:"required_count"`
	IncludedCount               int               `json:"included_count"`
	RenderedBytes               int               `json:"rendered_bytes"`
	CheckedAt                   time.Time         `json:"checked_at"`
	ValidUntil                  *time.Time        `json:"valid_until,omitempty"`
	Outcome                     string            `json:"outcome"`
	ReasonCode                  string            `json:"reason_code,omitempty"`
	CompatibilityEvidenceDigest string            `json:"compatibility_evidence_digest,omitempty"`
}
type Packet struct {
	Text, Digest, ManifestDigest string
	Bytes                        int
	Loaded                       []LoadedReference
}

func digest(version string, v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(append([]byte(version+"\x00"), raw...))
	return "sha256:" + hex.EncodeToString(sum[:])
}
func Digest(version string, v any) string { return digest(version, v) }
func textOK(s string) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func canonicalSet(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
func validPath(p string) bool {
	return textOK(p) && !strings.Contains(p, "\\") && !path.IsAbs(p) && path.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, "../")
}
func normalizedApplicability(a Applicability) (Applicability, error) {
	a.TaskIDs = canonicalSet(a.TaskIDs)
	a.Components = canonicalSet(a.Components)
	a.Paths = canonicalSet(a.Paths)
	if a.ProjectWide && len(a.TaskIDs)+len(a.Components)+len(a.Paths) > 0 {
		return a, ErrInvalid
	}
	if !a.ProjectWide && len(a.TaskIDs)+len(a.Components)+len(a.Paths) == 0 {
		return a, ErrInvalid
	}
	for _, p := range a.Paths {
		if !validPath(p) {
			return a, ErrInvalid
		}
	}
	for _, v := range append(append([]string(nil), a.TaskIDs...), a.Components...) {
		if !textOK(v) {
			return a, ErrInvalid
		}
	}
	return a, nil
}
func (a Applicability) Matches(s Selection) bool {
	if !validContextSelection(s) {
		return false
	}
	if s.Mode == SelectionPlanning {
		return true
	}
	if a.ProjectWide {
		return true
	}
	for _, id := range a.TaskIDs {
		if id == s.TaskID {
			return true
		}
	}
	for _, id := range a.Components {
		for _, selected := range s.Components {
			if id == selected {
				return true
			}
		}
	}
	for _, p := range a.Paths {
		for _, selected := range s.Paths {
			if selected == p || strings.HasPrefix(selected, p+"/") || strings.HasPrefix(p, selected+"/") {
				return true
			}
		}
	}
	return false
}
func (c Contract) Canonical() (Contract, error) {
	if c.FormatVersion != FormatVersion || !textOK(c.Project.CanonicalID) || !textOK(c.Project.StorageID) || !textOK(c.ID) || c.Revision < 1 || !textOK(c.Purpose) || len(c.Components) == 0 || len(c.Invariants) == 0 {
		return c, ErrInvalid
	}
	c.Components = append([]Component(nil), c.Components...)
	componentIDs := map[string]bool{}
	for i := range c.Components {
		v := &c.Components[i]
		if !textOK(v.ID) || !textOK(v.Responsibility) || len(v.Paths) == 0 || componentIDs[v.ID] {
			return c, ErrInvalid
		}
		componentIDs[v.ID] = true
		v.Paths = canonicalSet(v.Paths)
		v.Dependencies = canonicalSet(v.Dependencies)
		for _, p := range v.Paths {
			if !validPath(p) {
				return c, ErrInvalid
			}
		}
	}
	sort.Slice(c.Components, func(i, j int) bool { return c.Components[i].ID < c.Components[j].ID })
	for _, v := range c.Components {
		for _, dep := range v.Dependencies {
			if !componentIDs[dep] || dep == v.ID {
				return c, ErrInvalid
			}
		}
	}
	c.Invariants = canonicalSet(c.Invariants)
	c.ProhibitedChanges = canonicalSet(c.ProhibitedChanges)
	c.SourceAnchors = canonicalSet(c.SourceAnchors)
	c.DecisionRefs = canonicalSet(c.DecisionRefs)
	for _, v := range append(append([]string(nil), c.Invariants...), c.ProhibitedChanges...) {
		if !textOK(v) {
			return c, ErrInvalid
		}
	}
	c.Required = append([]MemoryRef(nil), c.Required...)
	seen := map[string]bool{}
	for i := range c.Required {
		r := &c.Required[i]
		key := r.ProjectID + "\x00" + r.ID
		if seen[key] || !textOK(r.ID) || r.ProjectID != c.Project.StorageID || !model.MemoryScopeKind(r.Scope).IsValid() || !textOK(r.ScopeID) || r.Revision < 0 || !textOK(r.ContentDigest) || !textOK(r.StateFingerprint) || !textOK(r.Reason) || !textOK(r.AcceptanceOccurrence) || r.MaxVerificationAgeSeconds < 0 || r.MaxVerificationAgeSeconds > int64((1<<63-1)/int64(time.Second)) {
			return c, ErrInvalid
		}
		seen[key] = true
		switch r.Class {
		case Policy, Constraint, CriticalDecision, AdditionalFact, ContextOnly:
		default:
			return c, ErrInvalid
		}
		var err error
		r.Applies, err = normalizedApplicability(r.Applies)
		if err != nil {
			return c, err
		}
		for _, id := range r.Applies.Components {
			if !componentIDs[id] {
				return c, ErrInvalid
			}
		}
	}
	sort.Slice(c.Required, func(i, j int) bool {
		return c.Required[i].ProjectID+"\x00"+c.Required[i].ID < c.Required[j].ProjectID+"\x00"+c.Required[j].ID
	})
	return c, nil
}
func (c Contract) CanonicalDigest() (string, error) {
	n, err := c.Canonical()
	if err != nil {
		return "", err
	}
	return digest("marshal.project.contract.v1", n), nil
}

// MemoryFingerprint includes authority-affecting fields omitted by the record's
// content-addressing digest. Unbound extension utility and ingestion/retrieval
// clocks do not establish authority and are intentionally excluded.
func MemoryFingerprint(r model.MemoryRecordV2) string {
	// These extension keys are consumed by the canonical MemoryService
	// freshness evaluator. They are semantic repository anchors, not utility.
	var anchors map[string]any
	for _, key := range []string{"file_path", "referenced_files", "file_hashes", "symbols", "verified_tests"} {
		if value, ok := r.ExtMeta[key]; ok {
			if anchors == nil {
				anchors = map[string]any{}
			}
			anchors[key] = value
		}
	}
	r.ExtMeta = anchors
	r.IngestedAt = time.Time{}
	r.UpdatedAt = time.Time{}
	r.EvidenceIDs = canonicalSet(r.EvidenceIDs)
	r.SupersededBy = canonicalSet(r.SupersededBy)
	r.SupersedesID = canonicalSet(r.SupersedesID)
	r.ConflictIDs = canonicalSet(r.ConflictIDs)
	return digest("marshal.project.memory-state.v1", r)
}
func EligibleAcceptedRecord(r model.MemoryRecordV2) bool {
	return (r.Lifecycle == model.MemoryDurable || r.Lifecycle == model.MemoryVerified) && (r.Authority == model.AuthorityOperator || r.Authority == model.AuthorityPolicy)
}
func ValidateRequiredRecord(r model.MemoryRecordV2, ref MemoryRef, now time.Time) error {
	if r.ID != ref.ID || r.ProjectID != ref.ProjectID || r.Scope != ref.Scope || r.ScopeID != ref.ScopeID || r.Revision != ref.Revision || r.ContentDigest != ref.ContentDigest || r.CanonicalDigest() != r.ContentDigest || MemoryFingerprint(r) != ref.StateFingerprint {
		return Changed("context.go:411")
	}
	if r.Lifecycle != model.MemoryDurable && r.Lifecycle != model.MemoryVerified {
		return Changed("context.go:414")
	}
	if len(r.ConflictIDs) > 0 || len(r.SupersededBy) > 0 || now.Before(r.ValidFrom) || r.ValidTo != nil && !now.Before(*r.ValidTo) {
		return Changed("context.go:417")
	}
	if ref.Class == Policy || ref.Class == Constraint || ref.Class == CriticalDecision {
		if !EligibleAcceptedRecord(r) {
			return ErrReviewPending
		}
	}
	if ref.EvidenceRequired && (r.Confidence != model.ConfidenceVerified || len(r.EvidenceIDs) == 0) {
		return Changed("context.go:425")
	}
	if ref.MaxVerificationAgeSeconds > 0 && (r.LastVerifiedAt == nil || now.Before(*r.LastVerifiedAt) || !now.Before(r.LastVerifiedAt.Add(time.Duration(ref.MaxVerificationAgeSeconds)*time.Second))) {
		return Changed("context.go:428")
	}
	return nil
}

// Render includes every applicable essential or refuses. Authorization and
// freshness were validated by the canonical resolver; this independently
// proves exact loaded-reference coverage and complete rendered byte bounds.
func Render(m Manifest, capBytes int) (Packet, error) { return RenderWithPrefix(m, "", capBytes) }
func RenderWithPrefix(m Manifest, prefix string, capBytes int) (Packet, error) {
	if !validContextSelection(m.Selection) {
		return Packet{}, ErrInvalid
	}
	if capBytes < 1 || capBytes > MaximumPacketBytes {
		return Packet{}, ErrBudget
	}
	if (m.Goal.Confirmation != "" && !model.ValidConfirmationState(m.Goal.Confirmation)) || !utf8.ValidString(m.Goal.AuthoritySource) || strings.ContainsRune(m.Goal.AuthoritySource, 0) {
		return Packet{}, ErrInvalid
	}
	if model.ValidateGoalContextText(model.GoalContract{
		ID: m.Goal.ID, OriginalRequest: m.Goal.OriginalRequest, DesiredOutcome: m.Goal.Outcome,
		ExpectedArtifact: m.Goal.ExpectedArtifact, Constraints: m.Goal.Constraints,
		DoNotDo: m.Goal.DoNotDo, SuccessCriteria: m.Goal.SuccessCriteria,
		Scope: m.Goal.Scope, RequiredCriticalClaims: m.Goal.RequiredCriticalClaims,
	}) != nil {
		return Packet{}, ErrInvalid
	}
	c, err := m.Contract.Canonical()
	if err != nil {
		return Packet{}, err
	}
	contractDigest, err := c.CanonicalDigest()
	if err != nil {
		return Packet{}, err
	}
	if m.Binding.Project != c.Project || m.Binding.Contract != (ContractRef{ID: c.ID, Revision: c.Revision, Digest: contractDigest}) || m.Goal.ID == "" || m.Goal.Revision < 1 || m.Goal.Outcome == "" {
		return Packet{}, Changed("context.go:464")
	}
	records := map[string]model.MemoryRecordV2{}
	for _, r := range m.Records {
		if _, exists := records[r.ID]; exists {
			return Packet{}, ErrInvalid
		}
		records[r.ID] = r
	}
	type item struct {
		Ref    MemoryRef          `json:"ref"`
		Title  string             `json:"title"`
		Body   string             `json:"body"`
		Source model.MemorySource `json:"source"`
	}
	items := []item{}
	loaded := []LoadedReference{}
	for _, ref := range c.Required {
		if !ref.Mandatory() || !ref.Applies.Matches(m.Selection) {
			continue
		}
		r, ok := records[ref.ID]
		if !ok {
			return Packet{}, ErrMissing
		}
		if !utf8.ValidString(r.Title) || strings.ContainsRune(r.Title, 0) || !utf8.ValidString(r.Body) || strings.ContainsRune(r.Body, 0) {
			return Packet{}, ErrInvalid
		}
		if r.ProjectID != ref.ProjectID || r.Revision != ref.Revision || r.ContentDigest != ref.ContentDigest || r.CanonicalDigest() != r.ContentDigest || MemoryFingerprint(r) != ref.StateFingerprint {
			return Packet{}, Changed("context.go:493")
		}
		items = append(items, item{ref, r.Title, r.Body, r.Source})
		loaded = append(loaded, LoadedReference{r.ID, r.ProjectID, r.Revision, r.ContentDigest, MemoryFingerprint(r)})
	}
	binding := m.Binding
	binding.FormatVersion = FormatVersion
	binding.GoalID = m.Goal.ID
	binding.GoalRevision = m.Goal.Revision
	binding.InventoryDigest = c.ReviewedInventoryDigest
	binding.ManifestDigest = ""
	manifestDigest := digest("marshal.project.manifest.v1", struct {
		Contract string
		Goal     Goal
		Binding  Binding
		Loaded   []LoadedReference
	}{contractDigest, m.Goal, binding, loaded})
	binding.ManifestDigest = manifestDigest
	envelope := struct {
		Trust      string      `json:"trust"`
		Binding    Binding     `json:"binding"`
		Purpose    string      `json:"purpose"`
		Components []Component `json:"components"`
		Invariants []string    `json:"invariants"`
		Prohibited []string    `json:"prohibited,omitempty"`
		Goal       Goal        `json:"goal"`
		Memories   []item      `json:"memories"`
		Bytes      int         `json:"bytes"`
	}{"accepted_required_project_context", binding, c.Purpose, c.Components, c.Invariants, c.ProhibitedChanges, m.Goal, items, 0}
	marshal := func() ([]byte, error) { return json.Marshal(envelope) }
	if security.RedactsSecrets {
		// Extreme never refuses a packet because it carries a secret: the
		// secret is removed from the rendered packet (identically wherever it
		// is rendered, so its digest stays reproducible) and the packet names
		// the redacted kinds.
		fw := security.NewFirewall(security.FirewallConfig{})
		var prefixRedaction security.Redaction
		prefix, prefixRedaction = fw.Redact(prefix)
		probe, err := json.Marshal(envelope)
		if err != nil {
			return Packet{}, err
		}
		redacted, r, err := fw.RedactJSON(probe)
		if err != nil {
			return Packet{}, err
		}
		if r.Applied() || prefixRedaction.Applied() {
			doc := map[string]any{}
			dec := json.NewDecoder(bytes.NewReader(redacted))
			dec.UseNumber()
			if err := dec.Decode(&doc); err != nil {
				return Packet{}, fmt.Errorf("%w: packet redaction", ErrInvalid)
			}
			kinds := append(append([]string{}, r.Kinds...), prefixRedaction.Kinds...)
			sort.Strings(kinds)
			doc["firewall_redacted"] = kinds
			marshal = func() ([]byte, error) {
				doc["bytes"] = envelope.Bytes
				return json.Marshal(doc)
			}
		}
	}
	var raw []byte
	for i := 0; i < 8; i++ {
		raw, err = marshal()
		if err != nil {
			return Packet{}, err
		}
		actual := len(prefix) + len(packetHeader) + len(raw) + 1
		if actual == envelope.Bytes {
			break
		}
		envelope.Bytes = actual
	}
	text := prefix + packetHeader + string(raw) + "\n"
	if len(text) > capBytes || len(text) != envelope.Bytes {
		return Packet{}, fmt.Errorf("%w: complete packet requires %d bytes (cap %d)", ErrBudget, len(text), capBytes)
	}
	sum := sha256.Sum256([]byte(text))
	return Packet{Text: text, Digest: "sha256:" + hex.EncodeToString(sum[:]), ManifestDigest: manifestDigest, Bytes: len(text), Loaded: loaded}, nil
}

// ApplicableContractDigest ignores an unrelated accepted ref change while
// preserving all architecture semantics and every applicable mandatory ref.
// It proves compatibility of two genuinely accepted immutable revisions; it
// does not accept a caller's draft or authorize execution.
func ApplicableContractDigest(c Contract, selection Selection) (string, error) {
	if !validContextSelection(selection) {
		return "", ErrInvalid
	}
	n, err := c.Canonical()
	if err != nil {
		return "", err
	}
	refs := []MemoryRef{}
	for _, ref := range n.Required {
		if ref.Mandatory() && ref.Applies.Matches(selection) {
			refs = append(refs, ref)
		}
	}
	return digest("marshal.project.applicable-contract.v1", struct {
		Project                                             ProjectBinding
		ID, Purpose                                         string
		Components                                          []Component
		Invariants, Prohibited, SourceAnchors, DecisionRefs []string
		Required                                            []MemoryRef
	}{n.Project, n.ID, n.Purpose, n.Components, n.Invariants, n.ProhibitedChanges, n.SourceAnchors, n.DecisionRefs, refs}), nil
}

// Changed is ErrChanged naming the check that refused, so an operator or
// the Marshal can see which binding moved. errors.Is still matches it.
func Changed(where string) error { return fmt.Errorf("%w (%s)", ErrChanged, where) }
