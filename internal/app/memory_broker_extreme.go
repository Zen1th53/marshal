//go:build !governed

package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

// RegisterMCPMemoryCandidate registers only a journaled, firewall-checked
// pending snapshot in the same runtime that owns the permission sink. It is
// not a memory retention grant and never directly writes canonical memory.
func (r *Runtime) RegisterMCPMemoryCandidate(ctx context.Context, occurrence string) (model.MemoryRecordV2, error) {
	c, err := r.store.MCPMemory(ctx, r.ProjectID(), occurrence)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	if c.State != "admitted" {
		return model.MemoryRecordV2{}, fmt.Errorf("memory candidate is not pending")
	}
	if c.Record, _, err = security.NewFirewall(security.FirewallConfig{}).AdmitRecord(ctx, c.Record); err != nil {
		return model.MemoryRecordV2{}, err
	}
	r.permissionMu.Lock()
	defer r.permissionMu.Unlock()
	if r.continuationCandidates == nil {
		r.continuationCandidates = map[string]model.MemoryRecordV2{}
	}
	r.continuationCandidates[c.Record.ID] = c.Record
	return c.Record, nil
}
func (r *Runtime) commitBrokerMemoryPermission(ctx context.Context, req permission.Request, allow bool, rec model.MemoryRecordV2, audit model.Event) (bool, error) {
	return r.commitBrokerMemoryPermissionWithIndex(ctx, req, allow, rec, audit, r.memoryService.IndexRecord)
}

func (r *Runtime) commitBrokerMemoryPermissionWithIndex(ctx context.Context, req permission.Request, allow bool, rec model.MemoryRecordV2, audit model.Event, index func(context.Context, model.MemoryRecordV2) error) (bool, error) {
	if req.Kind != "memory" {
		return false, nil
	}
	c, err := r.store.MCPMemoryByRecordID(ctx, r.ProjectID(), req.Object)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	// A known broker candidate must never fall through to the legacy writer,
	// even when a fresh generic proposal has no typed memory journal row.
	if req.SourceLaunch == "" || req.ProposalID != c.Occurrence {
		return true, model.ErrConflict
	}
	source, sourceErr := r.store.MCPProposal(ctx, r.ProjectID(), req.ProposalID)
	if sourceErr != nil {
		return true, sourceErr
	}
	if source.Launch != req.SourceLaunch {
		return true, model.ErrConflict
	}
	if c.Record.ID != req.Object {
		return true, model.ErrInvalid
	}
	if err = r.store.CommitMCPMemoryDecision(ctx, r.ProjectID(), req.ProposalID, allow, rec, audit); err != nil {
		return true, err
	}
	delete(r.continuationCandidates, req.Object)
	if !allow {
		return true, nil
	}
	// A projection failure cannot undo a canonical commit. Its receipt remains
	// committed/index_pending and can be repaired without re-insertion.
	return true, r.repairMCPMemoryIndex(ctx, req.ProposalID, index)
}
func (r *Runtime) RepairMCPMemoryIndex(ctx context.Context, occurrence string) error {
	return r.repairMCPMemoryIndex(ctx, occurrence, r.memoryService.IndexRecord)
}

func (r *Runtime) repairMCPMemoryIndex(ctx context.Context, occurrence string, index func(context.Context, model.MemoryRecordV2) error) error {
	c, err := r.store.MCPMemory(ctx, r.ProjectID(), occurrence)
	if err != nil {
		return err
	}
	if c.State == "applied" {
		return nil
	}
	if c.State != "committed" {
		return fmt.Errorf("memory is not committed")
	}
	rec, err := r.store.GetMemoryV2(ctx, r.ProjectID(), c.Record.ID)
	if err != nil {
		return err
	}
	if err = index(ctx, rec); err != nil {
		return err
	}
	return r.store.SetMCPProposalState(ctx, r.ProjectID(), occurrence, "applied", "canonical memory committed and indexed")
}
