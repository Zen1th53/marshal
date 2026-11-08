package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// memoryCommand derives operator identity only from OpenLocalControl. The
// operation-specific scoped capability is evaluated before replay as well as
// commit; callers never supply an actor or role.
func (r *Runtime) memoryCommand(ctx context.Context, e CommandEnvelope, operation string, input any) (context.Context, authz.Principal, store.CommandRecord, error) {
	var empty authz.Principal
	var record store.CommandRecord
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != e.ProjectID || e.ProjectID != r.ProjectIdentity() {
		return ctx, empty, record, authz.ErrDenied
	}
	if e.SessionID == "" || e.TargetID == "" || e.ExpectedVersion < 0 || strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > 256 {
		return ctx, empty, record, model.ErrInvalid
	}
	// Role is trusted composition, never an input supplied by a surface.
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(e.ProjectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: operation}
	decision, err := authz.CanWithCapability(ctx, principal, authz.AuthorityPolicyAdmin, r.layout.Database, query, capability.NewEngine(r.store, nil))
	if err != nil {
		return ctx, empty, record, err
	}
	body, err := json.Marshal(struct {
		Envelope  CommandEnvelope
		Operation string
		Input     any
	}{e, operation, input})
	if err != nil {
		return ctx, empty, record, err
	}
	digest := sha256.Sum256(body)
	key := sha256.Sum256([]byte(e.IdempotencyKey))
	record = store.CommandRecord{ProjectID: e.ProjectID, SessionID: e.SessionID, TargetID: e.TargetID, ExpectedVersion: e.ExpectedVersion, Actor: p.ID(), Operation: operation, Key: hex.EncodeToString(key[:]), Digest: hex.EncodeToString(digest[:]), CapabilityGrantID: decision.CapabilityGrantID}
	if strings.HasPrefix(operation, "memory.") {
		record.TargetProjectID = r.ProjectID()
	}
	return store.WithCommand(ctx, record), principal, record, nil
}

func (r *Runtime) CommandPromoteMemory(ctx context.Context, e CommandEnvelope, req PromoteRequest) (model.MemoryRecordV2, error) {
	if req.ProjectID != e.ProjectID || req.MemoryID != e.TargetID || req.ExpectedRevision == nil || req.ExpectedDigest == "" || *req.ExpectedRevision != e.ExpectedVersion {
		return model.MemoryRecordV2{}, model.ErrInvalid
	}
	ctx, p, command, err := r.memoryCommand(ctx, e, "memory.promote", req)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	if version, found, err := r.store.MemoryCommandResult(ctx, command); err != nil {
		return model.MemoryRecordV2{}, err
	} else if found {
		rec, err := r.memoryService.Get(ctx, p, r.ProjectID(), e.TargetID)
		if err != nil {
			return rec, err
		}
		if rec.Revision != version || rec.Lifecycle != model.MemoryDurable {
			return rec, model.ErrConflict
		}
		return rec, nil
	}
	// Preserve current ACL and applicability checks before entering the service.
	rec, err := r.memoryService.Get(ctx, p, r.ProjectID(), e.TargetID)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	if err = r.memoryService.revalidateMemoryRecord(ctx, rec, MemoryReconcileRequest{}); err != nil {
		return model.MemoryRecordV2{}, err
	}
	req.ProjectID = r.ProjectID()
	return r.memoryService.Promote(ctx, p, req)
}

// Policy digest binds runtime policy, constitution and current authority
// configuration. Recovery never uses an old snapshot as policy authority.
func (r *Runtime) memoryPolicyDigest() string {
	body, _ := json.Marshal(struct {
		Policy                RuntimePolicyConfig
		Configured            bool
		Project, Constitution string
	}{r.runtimePolicy, r.policyConfigured, r.ProjectIdentity(), r.Constitution().InvariantDigest()})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func validateMemorySelection(rec model.MemoryRecordV2, project string) error {
	if rec.ProjectID != project || rec.Scope != "project" || rec.ScopeID != project || rec.ACLScope != "" {
		return fmt.Errorf("%w: only explicit public project memory is supported by this action", authz.ErrDenied)
	}
	return nil
}
