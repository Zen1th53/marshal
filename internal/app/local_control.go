package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// CommandEnvelope identifies one exact canonical mutation; identity and roles
// are deliberately absent. The result is an immutable stored goal revision.
type CommandEnvelope struct {
	ProjectID       string `json:"project_id"`
	SessionID       string `json:"session_id"`
	TargetID        string `json:"target_id"`
	ExpectedVersion int64  `json:"expected_version"`
	IdempotencyKey  string `json:"idempotency_key"`
}

type LocalControl struct {
	runtime   *Runtime
	principal auth.LocalPrincipal
}
type localControlKey struct{}

func (c *LocalControl) Context(ctx context.Context) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(c.principal.Context(ctx), localControlKey{}, c.runtime)
}

// OpenLocalControl is trusted workspace composition, not an exported wire
// operation. No CLI, socket, agent registration, MCP or A2A handler calls it.
// Same-UID host processes are not authenticated as humans by this mechanism.
func (r *Runtime) OpenLocalControl(ctx context.Context) (*LocalControl, error) {
	if r == nil || r.store == nil {
		return nil, model.ErrUnavailable
	}
	projectID := r.ProjectIdentity()
	p, err := auth.LocalOwner(r.layout.RuntimeDir, projectID)
	if err != nil {
		return nil, fmt.Errorf("%w: local owner unavailable", authz.ErrDenied)
	}
	for _, action := range []string{"goal.revise", "goal.create", "goal.edit", "goal.add-constraint", "goal.rm-constraint", "approval.decide", "task.create", "task.assign", "task.control", "collab.message", "collab.handoff", "profile.model", "profile.effort", "run.control"} {
		key := "local-" + strings.ReplaceAll(action, ".", "-") + ":" + projectID + ":" + p.ID()
		// Provision once. Restart must never resurrect a revoked scoped grant.
		if _, found, err := r.store.FindCapabilityGrantByIdempotencyKey(ctx, key); err != nil {
			return nil, err
		} else if !found {
			sum := sha256.Sum256([]byte(key))
			now := time.Now().UTC()
			grant := capability.Grant{ID: capability.GrantID("cap-local-" + hex.EncodeToString(sum[:])), Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(projectID), Kind: capability.KindFilesystemWrite, Scope: capability.Scope{Resource: r.layout.Database, Actions: []string{action}}, IssuedAt: now, ExpiresAt: now.AddDate(100, 0, 0), Issuer: capability.SubjectID(p.ID()), IdempotencyKey: key}
			if err := r.store.PutCapabilityGrant(ctx, grant); err != nil {
				return nil, err
			}
		}
	}
	return &LocalControl{runtime: r, principal: p}, nil
}

func (r *Runtime) CommandReviseGoal(ctx context.Context, e CommandEnvelope, interpretation, reason string) (model.GoalContract, error) {
	return r.commandGoal(ctx, e, "goal.revise", struct{ Interpretation, Reason string }{interpretation, reason}, func(ctx context.Context, actor string) (model.GoalContract, error) {
		return r.reviseGoal(ctx, e.SessionID, e.ExpectedVersion, interpretation, reason)
	})
}

func (r *Runtime) commandGoal(ctx context.Context, e CommandEnvelope, operation string, input any, mutate func(context.Context, string) (model.GoalContract, error)) (model.GoalContract, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != e.ProjectID {
		return model.GoalContract{}, authz.ErrDenied
	}
	projectID := r.ProjectIdentity()
	if projectID != e.ProjectID {
		return model.GoalContract{}, authz.ErrDenied
	}
	if e.SessionID == "" || e.TargetID == "" || e.ExpectedVersion < 0 || (operation != "goal.create" && e.ExpectedVersion < 1) || (operation == "goal.create" && e.ExpectedVersion != 0) || strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > 256 {
		return model.GoalContract{}, model.ErrInvalid
	}
	subject := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(projectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: operation}
	decision, err := authz.CanWithCapability(ctx, subject, authz.AuthorityTaskPlan, r.layout.Database, query, capability.NewEngine(r.store, nil))
	if err != nil {
		return model.GoalContract{}, err
	}
	payload, err := json.Marshal(struct {
		Envelope  CommandEnvelope
		Operation string
		Input     any
	}{e, operation, input})
	// Retain the stage-1 digest encoding for durable receipt compatibility.
	if operation == "goal.revise" {
		legacy := input.(struct{ Interpretation, Reason string })
		payload, err = json.Marshal(struct {
			Envelope               CommandEnvelope
			Interpretation, Reason string
		}{e, legacy.Interpretation, legacy.Reason})
	}
	if err != nil {
		return model.GoalContract{}, err
	}
	sum := sha256.Sum256(payload)
	keyDigest := sha256.Sum256([]byte(e.IdempotencyKey))
	record := store.CommandRecord{ProjectID: projectID, Actor: p.ID(), Key: hex.EncodeToString(keyDigest[:]), Operation: operation, SessionID: e.SessionID, TargetID: e.TargetID, ExpectedVersion: e.ExpectedVersion, Digest: hex.EncodeToString(sum[:])}
	record.CapabilityGrantID = decision.CapabilityGrantID
	if version, found, err := r.store.CommandResult(ctx, record); err != nil {
		return model.GoalContract{}, err
	} else if found {
		return r.store.GetGoalContract(ctx, e.TargetID, version)
	}
	if operation != "goal.create" {
		goal, err := r.store.GetActiveGoalContract(ctx, e.SessionID)
		if err != nil {
			return model.GoalContract{}, err
		}
		if goal.ProjectID != projectID || goal.ID != e.TargetID {
			return model.GoalContract{}, authz.ErrDenied
		}
		if goal.Revision != e.ExpectedVersion {
			return model.GoalContract{}, model.ErrGoalConflict
		}
	}
	result, err := mutate(store.WithCommand(ctx, record), p.ID())
	// Another submission may have won the transaction while this one read.
	if err != nil {
		if version, found, replayErr := r.store.CommandResult(ctx, record); replayErr != nil {
			return model.GoalContract{}, replayErr
		} else if found {
			return r.store.GetGoalContract(ctx, e.TargetID, version)
		}
	}
	return result, err
}
