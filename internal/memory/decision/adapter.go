package decision

import (
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// ToMemoryRecordV2 converts a DecisionRecord into a canonical MemoryRecordV2.
func (d *DecisionRecord) ToMemoryRecordV2(projectID string) model.MemoryRecordV2 {
	var lifecycle model.MemoryLifecycle
	var authority model.MemoryAuthority

	switch d.Status {
	case StatusAccepted:
		lifecycle = model.MemoryDurable
		authority = model.AuthorityOperator
	case StatusProposed:
		lifecycle = model.MemoryCandidate
		authority = model.AuthorityAgent
	case StatusRejected:
		lifecycle = model.MemoryRejected
		authority = model.AuthorityOperator
	case StatusSuperseded:
		lifecycle = model.MemorySuperseded
		authority = model.AuthorityOperator
	case StatusDeprecated:
		lifecycle = model.MemoryStale
		authority = model.AuthorityOperator
	default:
		lifecycle = model.MemoryCandidate
		authority = model.AuthorityAgent
	}

	createdAt := d.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := d.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}

	extMeta := make(map[string]any)
	if d.Context != "" {
		extMeta["context"] = d.Context
	}
	if d.Consequences != "" {
		extMeta["consequences"] = d.Consequences
	}
	if d.AuthorityID != "" {
		extMeta["authority_id"] = d.AuthorityID
	}
	if d.Supersedes != "" {
		extMeta["supersedes"] = d.Supersedes
	}
	if d.SupersededBy != "" {
		extMeta["superseded_by"] = d.SupersededBy
	}

	var supersedesIDs []string
	if d.Supersedes != "" {
		supersedesIDs = []string{d.Supersedes}
	}
	var supersededByIDs []string
	if d.SupersededBy != "" {
		supersededByIDs = []string{d.SupersededBy}
	}

	rec := model.MemoryRecordV2{
		ID:           d.ID,
		ProjectID:    projectID,
		Kind:         model.MemoryKindDecision,
		Lifecycle:    lifecycle,
		Confidence:   model.ConfidenceVerified,
		Authority:    authority,
		Title:        d.Title,
		Body:         d.Decision,
		Scope:        string(model.ScopeTask),
		ScopeID:      d.TaskID,
		ObservedAt:   createdAt,
		IngestedAt:   createdAt,
		ValidFrom:    createdAt,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
		SupersedesID: supersedesIDs,
		SupersededBy: supersededByIDs,
		Source: model.MemorySource{
			Kind:      "runtime",
			Reference: d.TaskID,
			AgentID:   d.AgentID,
		},
		ExtMeta: extMeta,
	}

	rec.ContentDigest = rec.CanonicalDigest()
	return rec
}

// FromMemoryRecordV2 converts a canonical MemoryRecordV2 back into a DecisionRecord.
func FromMemoryRecordV2(rec model.MemoryRecordV2) (*DecisionRecord, error) {
	if rec.Kind != model.MemoryKindDecision {
		return nil, fmt.Errorf("cannot convert non-decision record (kind %s) to DecisionRecord", rec.Kind)
	}

	var status Status
	switch rec.Lifecycle {
	case model.MemoryDurable, model.MemoryVerified:
		status = StatusAccepted
	case model.MemoryCandidate, model.MemoryObserved:
		status = StatusProposed
	case model.MemoryRejected:
		status = StatusRejected
	case model.MemorySuperseded:
		status = StatusSuperseded
	case model.MemoryStale:
		status = StatusDeprecated
	case model.MemoryTombstoned:
		status = StatusDeprecated
	default:
		status = StatusProposed
	}

	dec := &DecisionRecord{
		ID:        rec.ID,
		TaskID:    rec.ScopeID,
		AgentID:   rec.Source.AgentID,
		Title:     rec.Title,
		Decision:  rec.Body,
		Status:    status,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	}

	if rec.ExtMeta != nil {
		if ctxStr, ok := rec.ExtMeta["context"].(string); ok {
			dec.Context = ctxStr
		}
		if consStr, ok := rec.ExtMeta["consequences"].(string); ok {
			dec.Consequences = consStr
		}
		if authStr, ok := rec.ExtMeta["authority_id"].(string); ok {
			dec.AuthorityID = authStr
		}
		if supStr, ok := rec.ExtMeta["supersedes"].(string); ok {
			dec.Supersedes = supStr
		}
		if supByStr, ok := rec.ExtMeta["superseded_by"].(string); ok {
			dec.SupersededBy = supByStr
		}
	}

	if len(rec.SupersedesID) > 0 && dec.Supersedes == "" {
		dec.Supersedes = rec.SupersedesID[0]
	}
	if len(rec.SupersededBy) > 0 && dec.SupersededBy == "" {
		dec.SupersededBy = rec.SupersededBy[0]
	}

	return dec, nil
}
