package app

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// MemoryReference is an exact reference, never a copy of memory content. A
// recovery manifest may retain these references but must revalidate before use.
type MemoryReference struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
}

type MemoryReferenceRequest struct {
	ProjectID  string
	References []MemoryReference
	Freshness  MemoryReconcileRequest
}

// RevalidateReferences fails the entire bounded set closed. It never follows
// supersession pointers (including cycles), substitutes newer records, repairs
// ACLs, or resurrects missing records. A superseded reference needs new review.
// Recovery E can consume this API without depending on restore storage.
func (s *MemoryService) RevalidateReferences(ctx context.Context, p authz.Principal, req MemoryReferenceRequest) ([]MemoryReference, error) {
	if s == nil || s.store == nil {
		return nil, model.ErrUnavailable
	}
	if req.ProjectID == "" || len(req.References) > 128 {
		return nil, model.ErrInvalid
	}
	project, err := s.store.Project(ctx)
	if err != nil {
		return nil, err
	}
	if req.ProjectID != project.ID {
		binding, found := projectid.LoadBinding(filepath.Join(project.Repository, projectid.StateDirName))
		if !found || string(binding.ID) != req.ProjectID {
			return nil, authz.ErrDenied
		}
		req.ProjectID = project.ID
	}
	seen := map[string]bool{}
	out := make([]MemoryReference, 0, len(req.References))
	for _, ref := range req.References {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if ref.ID == "" || ref.Revision < 0 || ref.Digest == "" || seen[ref.ID] {
			return nil, fmt.Errorf("%w: duplicate, cyclic or invalid memory reference", model.ErrInvalid)
		}
		seen[ref.ID] = true
		rec, err := s.Get(ctx, p, req.ProjectID, ref.ID)
		if err != nil {
			return nil, err
		}
		if rec.Revision != ref.Revision || rec.ContentDigest != ref.Digest || rec.CanonicalDigest() != ref.Digest {
			return nil, fmt.Errorf("%w: memory reference changed", model.ErrConflict)
		}
		if err := s.revalidateMemoryRecord(ctx, rec, req.Freshness); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, nil
}

func (s *MemoryService) revalidateMemoryRecord(ctx context.Context, rec model.MemoryRecordV2, freshness MemoryReconcileRequest) error {
	switch rec.Lifecycle {
	case model.MemoryCandidate, model.MemoryVerified, model.MemoryDurable:
	default:
		return fmt.Errorf("%w: memory lifecycle no longer applicable", model.ErrConflict)
	}
	now := time.Now().UTC()
	if len(rec.SupersededBy) > 0 || len(rec.ConflictIDs) > 0 || now.Before(rec.ValidFrom) || (rec.ValidTo != nil && !now.Before(*rec.ValidTo)) {
		return fmt.Errorf("%w: expired, superseded or conflicted memory", model.ErrConflict)
	}
	if err := security.NewFirewall(security.FirewallConfig{}).ScanRecord(ctx, rec); err != nil {
		return err
	}
	for _, id := range rec.EvidenceIDs {
		node, err := s.store.Get(ctx, evidence.NodeID(id))
		if err != nil || (node.State != evidence.StateStored && node.State != evidence.StateLinked) {
			return fmt.Errorf("%w: memory evidence unavailable", model.ErrConflict)
		}
	}
	// Freshness is evaluated with the current caller-supplied workspace facts;
	// no facts are inferred from the historical record itself.
	if ev := s.EvaluateFreshness(rec, freshness); ev.Classification != FreshnessFresh {
		return fmt.Errorf("%w: memory workspace binding is stale", model.ErrConflict)
	}
	return nil
}
