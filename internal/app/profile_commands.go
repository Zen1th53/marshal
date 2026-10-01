package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// ModelPreferenceAdapters are the adapters whose governed dispatch reads the
// project's execution model preference. A selection for any other harness
// would be a preference nothing honours, so it is refused.
var ModelPreferenceAdapters = []string{"codex", "claude"}

// CommandSetModel records the model future governed runs of an adapter use,
// through the operator boundary. The model is validated against the adapter's
// catalog first; the preference, receipt and audit commit together, CAS-bound
// to the revision the operator saw (the envelope's expected version).
func (r *Runtime) CommandSetModel(ctx context.Context, e CommandEnvelope, adapter, modelName string) (model.ExecutionModelPreference, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != e.ProjectID || e.ProjectID != r.ProjectIdentity() {
		return model.ExecutionModelPreference{}, authz.ErrDenied
	}
	adapter = strings.ToLower(strings.TrimSpace(adapter))
	modelName = strings.TrimSpace(modelName)
	if adapter != "codex" && adapter != "claude" {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: %s runs do not read a model preference; supported: %s", model.ErrInvalid, adapter, strings.Join(ModelPreferenceAdapters, ", "))
	}
	if modelName == "" || e.TargetID != "model:"+adapter || e.ExpectedVersion < 0 || strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > 256 {
		return model.ExecutionModelPreference{}, model.ErrInvalid
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(e.ProjectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: "profile.model"}
	decision, err := authz.CanWithCapability(ctx, principal, authz.AuthorityTaskPlan, r.layout.Database, query, capability.NewEngine(r.store, nil))
	if err != nil {
		return model.ExecutionModelPreference{}, err
	}
	payload, err := json.Marshal(struct {
		Envelope       CommandEnvelope
		Adapter, Model string
	}{e, adapter, modelName})
	if err != nil {
		return model.ExecutionModelPreference{}, err
	}
	digest := sha256.Sum256(payload)
	key := sha256.Sum256([]byte(e.IdempotencyKey))
	record := store.CommandRecord{ProjectID: e.ProjectID, Actor: p.ID(), Key: hex.EncodeToString(key[:]), Operation: "profile.model",
		SessionID: e.SessionID, TargetID: e.TargetID, ExpectedVersion: e.ExpectedVersion, Digest: hex.EncodeToString(digest[:]),
		CapabilityGrantID: decision.CapabilityGrantID}
	read := r.CodexModelPreference
	set := r.SetCodexModelPreference
	if adapter == "claude" {
		read, set = r.ClaudeModelPreference, r.SetClaudeModelPreference
	}
	if _, found, err := r.store.CommandResult(ctx, record); err != nil {
		return model.ExecutionModelPreference{}, err
	} else if found {
		return read(ctx)
	}
	preference, err := set(store.WithCommand(ctx, record), modelName, e.ExpectedVersion)
	if err != nil {
		if _, found, replayErr := r.store.CommandResult(ctx, record); replayErr == nil && found {
			return read(ctx)
		}
		return model.ExecutionModelPreference{}, err
	}
	return preference, nil
}

// ModelPreferenceRevision is the revision a new selection must name: zero
// when the adapter has no preference yet.
func (r *Runtime) ModelPreferenceRevision(ctx context.Context, adapter string) (int64, string, error) {
	read := r.CodexModelPreference
	if adapter == "claude" {
		read = r.ClaudeModelPreference
	}
	preference, err := read(ctx)
	if errors.Is(err, model.ErrNotFound) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	return preference.Revision, preference.Model, nil
}

// CommandSetEffort records the reasoning effort future governed Codex runs
// request, through the operator boundary with the profile.effort capability.
// Only Codex runs read an effort; the value must be one the selected model's
// catalog advertises, and an empty effort returns to the model's default.
func (r *Runtime) CommandSetEffort(ctx context.Context, e CommandEnvelope, adapter, effort string) (model.ExecutionModelPreference, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != e.ProjectID || e.ProjectID != r.ProjectIdentity() {
		return model.ExecutionModelPreference{}, authz.ErrDenied
	}
	adapter = strings.ToLower(strings.TrimSpace(adapter))
	if adapter != "codex" {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: %s runs do not read a reasoning effort; supported: codex", model.ErrInvalid, adapter)
	}
	if e.TargetID != "effort:"+adapter || e.ExpectedVersion < 1 || strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > 256 {
		return model.ExecutionModelPreference{}, model.ErrInvalid
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(e.ProjectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: "profile.effort"}
	decision, err := authz.CanWithCapability(ctx, principal, authz.AuthorityTaskPlan, r.layout.Database, query, capability.NewEngine(r.store, nil))
	if err != nil {
		return model.ExecutionModelPreference{}, err
	}
	payload, err := json.Marshal(struct {
		Envelope        CommandEnvelope
		Adapter, Effort string
	}{e, adapter, effort})
	if err != nil {
		return model.ExecutionModelPreference{}, err
	}
	digest := sha256.Sum256(payload)
	key := sha256.Sum256([]byte(e.IdempotencyKey))
	record := store.CommandRecord{ProjectID: e.ProjectID, Actor: p.ID(), Key: hex.EncodeToString(key[:]), Operation: "profile.effort",
		SessionID: e.SessionID, TargetID: e.TargetID, ExpectedVersion: e.ExpectedVersion, Digest: hex.EncodeToString(digest[:]),
		CapabilityGrantID: decision.CapabilityGrantID}
	if _, found, err := r.store.CommandResult(ctx, record); err != nil {
		return model.ExecutionModelPreference{}, err
	} else if found {
		return r.CodexModelPreference(ctx)
	}
	preference, err := r.SetCodexEffortPreference(store.WithCommand(ctx, record), effort, e.ExpectedVersion)
	if err != nil {
		if _, found, replayErr := r.store.CommandResult(ctx, record); replayErr == nil && found {
			return r.CodexModelPreference(ctx)
		}
		return model.ExecutionModelPreference{}, err
	}
	return preference, nil
}
