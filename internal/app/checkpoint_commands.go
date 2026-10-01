package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// checkpointCommand authorizes one checkpoint operation and returns its
// receipt record; found reports a replay of an already applied command.
func (r *Runtime) checkpointCommand(ctx context.Context, e CommandEnvelope, action string, input any) (store.CommandRecord, bool, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != e.ProjectID || e.ProjectID != r.ProjectIdentity() {
		return store.CommandRecord{}, false, authz.ErrDenied
	}
	if e.SessionID == "" || e.TargetID == "" || strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > 256 {
		return store.CommandRecord{}, false, model.ErrInvalid
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(e.ProjectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: action}
	grant, err := authz.CanWithCapability(ctx, principal, authz.AuthorityTaskPlan, r.layout.Database, query, capability.NewEngine(r.store, nil))
	if err != nil {
		return store.CommandRecord{}, false, err
	}
	payload, err := json.Marshal(struct {
		Envelope CommandEnvelope
		Input    any
	}{e, input})
	if err != nil {
		return store.CommandRecord{}, false, err
	}
	digest := sha256.Sum256(payload)
	key := sha256.Sum256([]byte(e.IdempotencyKey))
	record := store.CommandRecord{ProjectID: e.ProjectID, Actor: p.ID(), Key: hex.EncodeToString(key[:]), Operation: action,
		SessionID: e.SessionID, TargetID: e.TargetID, ExpectedVersion: e.ExpectedVersion, Digest: hex.EncodeToString(digest[:]),
		CapabilityGrantID: grant.CapabilityGrantID}
	_, found, err := r.store.CommandResult(ctx, record)
	return record, found, err
}

// CommandCaptureCheckpoint snapshots the project's files as the operator.
func (r *Runtime) CommandCaptureCheckpoint(ctx context.Context, e CommandEnvelope, reason string) (execution.CheckpointRecord, error) {
	if strings.TrimSpace(reason) == "" {
		return execution.CheckpointRecord{}, model.ErrInvalid
	}
	record, found, err := r.checkpointCommand(ctx, e, "checkpoint.create", reason)
	if err != nil {
		return execution.CheckpointRecord{}, err
	}
	if found {
		return execution.CheckpointRecord{}, model.ErrConflict
	}
	captured, err := r.Execution().Engine().CaptureOperatorCheckpoint(ctx, execution.RedactSecrets(reason))
	if err != nil {
		return execution.CheckpointRecord{}, err
	}
	return captured, r.store.RecordCommandReceipt(ctx, record, 1)
}

// CommandRestoreCheckpoint restores the checkpoint named by "checkpoint:<id>"
// when the operator confirmed its exact snapshot digest. The engine refuses
// while any run is active, captures a recovery point first and verifies the
// restored files before reporting success.
func (r *Runtime) CommandRestoreCheckpoint(ctx context.Context, e CommandEnvelope, confirmedDigest string) (execution.RestoreResult, error) {
	checkpointID, ok := strings.CutPrefix(e.TargetID, "checkpoint:")
	if !ok || checkpointID == "" || confirmedDigest == "" {
		return execution.RestoreResult{}, model.ErrInvalid
	}
	record, found, err := r.checkpointCommand(ctx, e, "checkpoint.restore", confirmedDigest)
	if err != nil {
		return execution.RestoreResult{}, err
	}
	if found {
		return execution.RestoreResult{}, model.ErrConflict
	}
	result, err := r.Execution().Engine().RestoreVerified(ctx, checkpointID, confirmedDigest)
	if err != nil {
		return result, err
	}
	return result, r.store.RecordCommandReceipt(ctx, record, 1)
}
