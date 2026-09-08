package learning

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// DataClass separates what Process 07 keeps from what it may drop. Durable
// metadata and digests are kept longer than raw payloads, because provenance
// must outlive the bytes it describes.
type DataClass string

const (
	ClassCanonicalClaim DataClass = "CANONICAL_CLAIM"
	ClassEvidenceMeta   DataClass = "EVIDENCE_METADATA"
	ClassRawOutput      DataClass = "RAW_TOOL_OUTPUT"
	ClassTranscript     DataClass = "PROVIDER_TRANSCRIPT"
	ClassSecret         DataClass = "SECRET"
	ClassCheckpoint     DataClass = "CHECKPOINT"
	ClassBenchmark      DataClass = "BENCHMARK"
	ClassPostmortem     DataClass = "POSTMORTEM"
	ClassBinaryArtifact DataClass = "BINARY_ARTIFACT"
)

// GCAction is what garbage collection does to one record. Collection never
// simply erases: it either keeps the record, drops the payload while keeping
// the integrity metadata, or purges a secret outright.
type GCAction string

const (
	// GCRetain keeps the record intact.
	GCRetain GCAction = "RETAIN"
	// GCExpirePayload drops the raw payload and keeps digest and provenance,
	// so an attestation that references this evidence stays verifiable.
	GCExpirePayload GCAction = "EXPIRE_PAYLOAD"
	// GCArchive moves the record out of the hot path, keeping it retrievable.
	GCArchive GCAction = "ARCHIVE"
	// GCPurge removes the record entirely. Reserved for secrets.
	GCPurge GCAction = "PURGE"
	// GCBlocked refuses collection because an active attestation depends on it.
	GCBlocked GCAction = "BLOCKED"
)

// GCCandidate is one record considered for collection.
type GCCandidate struct {
	ID    string    `json:"id"`
	Class DataClass `json:"class"`
	// AttestationRefs are the completion attestations that depend on this
	// record. A record with live references cannot be purged.
	AttestationRefs []string   `json:"attestation_refs,omitempty"`
	KeepUntil       *time.Time `json:"keep_until,omitempty"`
	LastUsed        time.Time  `json:"last_used"`
}

// GCPlan is the deterministic, auditable result of planning collection.
type GCPlan struct {
	Action GCAction  `json:"action"`
	ID     string    `json:"id"`
	Class  DataClass `json:"class"`
	Reason string    `json:"reason"`
}

// PlanGC decides what happens to each candidate. The result is deterministic
// and ordered by id so two runs over the same input produce the same plan and
// the plan itself can be reviewed before it is applied.
//
// A secret is always purged, regardless of references: keeping a credential to
// satisfy an audit trail is not a tradeoff Process 07 makes. Everything else
// that an active attestation depends on is kept, and expiry drops payloads
// rather than provenance.
func PlanGC(candidates []GCCandidate, now time.Time) []GCPlan {
	plans := make([]GCPlan, 0, len(candidates))
	for _, c := range candidates {
		plans = append(plans, planOne(c, now))
	}
	sort.SliceStable(plans, func(i, j int) bool { return plans[i].ID < plans[j].ID })
	return plans
}

func planOne(c GCCandidate, now time.Time) GCPlan {
	if c.Class == ClassSecret {
		return GCPlan{Action: GCPurge, ID: c.ID, Class: c.Class, Reason: "secret material is never retained"}
	}
	expired := c.KeepUntil != nil && !now.Before(*c.KeepUntil)
	if !expired {
		return GCPlan{Action: GCRetain, ID: c.ID, Class: c.Class, Reason: "retention window is open"}
	}
	switch c.Class {
	case ClassCanonicalClaim, ClassEvidenceMeta:
		// Canonical claims and evidence metadata are the audit trail itself.
		// They are archived, never dropped, so provenance survives.
		return GCPlan{Action: GCArchive, ID: c.ID, Class: c.Class, Reason: "canonical record archived, provenance preserved"}
	case ClassRawOutput, ClassTranscript, ClassBinaryArtifact, ClassCheckpoint:
		if len(c.AttestationRefs) > 0 {
			return GCPlan{Action: GCExpirePayload, ID: c.ID, Class: c.Class, Reason: "payload expired, digest retained for active attestation"}
		}
		return GCPlan{Action: GCExpirePayload, ID: c.ID, Class: c.Class, Reason: "payload expired, digest retained"}
	default:
		return GCPlan{Action: GCArchive, ID: c.ID, Class: c.Class, Reason: "expired record archived"}
	}
}

// ValidateRetention rejects a retention decision that would silently break
// provenance or keep a secret.
func ValidateRetention(d RetentionDecision) error {
	if strings.TrimSpace(d.Class) == "" {
		return fmt.Errorf("%w: retention class", ErrInvalid)
	}
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("%w: retention needs an explicit reason", ErrInvalid)
	}
	if DataClass(d.Class) == ClassSecret && !d.Archive && d.KeepUntil != nil {
		return fmt.Errorf("%w: secrets cannot be retained", ErrSecretMaterial)
	}
	return nil
}

// ExportBundle is a secret-safe, integrity-checked memory export.
type ExportBundle struct {
	SchemaVersion int    `json:"schema_version"`
	ProjectID     string `json:"project_id,omitempty"`
	Items         []Item `json:"items"`
	// Redacted lists ids omitted because they carried secret material. An
	// export that silently dropped records would be a false backup.
	Redacted  []string  `json:"redacted,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Digest    string    `json:"digest"`
}

// Export builds a backup bundle. Items carrying secret material are omitted
// and named in Redacted, so the export is honest about being incomplete rather
// than pretending to be a full copy.
func Export(items []Item, schemaVersion int, projectID string, now time.Time) (ExportBundle, error) {
	if schemaVersion <= 0 {
		return ExportBundle{}, fmt.Errorf("%w: export needs a schema version", ErrInvalid)
	}
	b := ExportBundle{SchemaVersion: schemaVersion, ProjectID: projectID, CreatedAt: now}
	for _, it := range items {
		if CarriesSecret(it.Claim) {
			b.Redacted = append(b.Redacted, it.ID)
			continue
		}
		b.Items = append(b.Items, it)
	}
	sort.SliceStable(b.Items, func(i, j int) bool { return b.Items[i].ID < b.Items[j].ID })
	sort.Strings(b.Redacted)
	d, err := digest(struct {
		SchemaVersion int
		ProjectID     string
		Items         []Item
		Redacted      []string
		CreatedAt     time.Time
	}{b.SchemaVersion, b.ProjectID, b.Items, b.Redacted, b.CreatedAt})
	if err != nil {
		return ExportBundle{}, err
	}
	b.Digest = d
	return b, nil
}

// VerifyExport reports whether a bundle still matches its digest.
func VerifyExport(b ExportBundle) error {
	if b.Digest == "" {
		return fmt.Errorf("%w: export digest", ErrInvalid)
	}
	got, err := digest(struct {
		SchemaVersion int
		ProjectID     string
		Items         []Item
		Redacted      []string
		CreatedAt     time.Time
	}{b.SchemaVersion, b.ProjectID, b.Items, b.Redacted, b.CreatedAt})
	if err != nil {
		return err
	}
	if got != b.Digest {
		return ErrTampered
	}
	return nil
}

// RestorePlan is the reconciliation decision for one restored item.
type RestorePlan struct {
	ItemID string `json:"item_id"`
	// Apply reports whether the restored version replaces the stored one.
	Apply  bool   `json:"apply"`
	Reason string `json:"reason"`
}

// Restore reconciles a bundle against current memory.
//
// A backup is older knowledge by construction, so it never overwrites a newer
// canonical version: restore fills gaps and loses ties to the stored state.
// An invalidation in the current store is never undone by a restore that
// predates it.
func Restore(b ExportBundle, current []Item, schemaVersion int) ([]RestorePlan, error) {
	if err := VerifyExport(b); err != nil {
		return nil, err
	}
	if b.SchemaVersion != schemaVersion {
		return nil, fmt.Errorf("%w: export schema %d does not match store schema %d", ErrInvalid, b.SchemaVersion, schemaVersion)
	}
	byID := make(map[string]Item, len(current))
	for _, it := range current {
		byID[it.ID] = it
	}
	plans := make([]RestorePlan, 0, len(b.Items))
	for _, restored := range b.Items {
		if CarriesSecret(restored.Claim) {
			plans = append(plans, RestorePlan{ItemID: restored.ID, Reason: "restored item carries secret material"})
			continue
		}
		existing, ok := byID[restored.ID]
		if !ok {
			plans = append(plans, RestorePlan{ItemID: restored.ID, Apply: true, Reason: "item absent from current memory"})
			continue
		}
		if existing.Version >= restored.Version {
			plans = append(plans, RestorePlan{ItemID: restored.ID, Reason: "current memory is newer or equal"})
			continue
		}
		if existing.State == model.ClaimStateInvalidated {
			plans = append(plans, RestorePlan{ItemID: restored.ID, Reason: "current memory is invalidated"})
			continue
		}
		plans = append(plans, RestorePlan{ItemID: restored.ID, Apply: true, Reason: "restored version is newer"})
	}
	sort.SliceStable(plans, func(i, j int) bool { return plans[i].ItemID < plans[j].ItemID })
	return plans, nil
}
