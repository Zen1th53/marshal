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
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// CommandAlignmentDecision records the operator's response to one alignment
// violation through LocalControl with the alignment.decide capability. The
// envelope targets "run:<id>/<task>#<n>" at the run version the operator saw.
// The violation itself is never removed or downgraded.
func (r *Runtime) CommandAlignmentDecision(ctx context.Context, e CommandEnvelope, decision, reason string) (execution.ExecutionRun, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != e.ProjectID || e.ProjectID != r.ProjectIdentity() {
		return execution.ExecutionRun{}, authz.ErrDenied
	}
	runID, taskID, violation, err := ParseAlignmentTarget(e.TargetID)
	if err != nil || e.ExpectedVersion < 0 || strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > 256 {
		return execution.ExecutionRun{}, model.ErrInvalid
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(e.ProjectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: "alignment.decide"}
	grant, err := authz.CanWithCapability(ctx, principal, authz.AuthorityTaskPlan, r.layout.Database, query, capability.NewEngine(r.store, nil))
	if err != nil {
		return execution.ExecutionRun{}, err
	}
	payload, err := json.Marshal(struct {
		Envelope         CommandEnvelope
		Decision, Reason string
	}{e, decision, reason})
	if err != nil {
		return execution.ExecutionRun{}, err
	}
	digest := sha256.Sum256(payload)
	key := sha256.Sum256([]byte(e.IdempotencyKey))
	record := store.CommandRecord{ProjectID: e.ProjectID, Actor: p.ID(), Key: hex.EncodeToString(key[:]), Operation: "alignment.decide",
		SessionID: e.SessionID, TargetID: e.TargetID, ExpectedVersion: e.ExpectedVersion, Digest: hex.EncodeToString(digest[:]),
		CapabilityGrantID: grant.CapabilityGrantID}
	engine := r.Execution().Engine()
	if engine == nil {
		return execution.ExecutionRun{}, model.ErrUnavailable
	}
	if _, found, err := r.store.CommandResult(ctx, record); err != nil {
		return execution.ExecutionRun{}, err
	} else if found {
		return engine.GetRun(ctx, runID)
	}
	current, err := engine.GetRun(ctx, runID)
	if err != nil {
		return execution.ExecutionRun{}, err
	}
	if current.SessionID != e.SessionID {
		return execution.ExecutionRun{}, authz.ErrDenied
	}
	run, err := engine.RecordAlignmentDecision(ctx, runID, taskID, violation, decision, execution.RedactSecrets(reason), p.ID(), e.ExpectedVersion)
	if err != nil {
		return execution.ExecutionRun{}, err
	}
	if err := r.store.RecordCommandReceipt(ctx, record, run.Version); err != nil {
		return execution.ExecutionRun{}, err
	}
	return run, nil
}

// ParseAlignmentTarget reads "run:<run>/<task>#<n>".
func ParseAlignmentTarget(target string) (string, string, int, error) {
	rest, ok := strings.CutPrefix(target, "run:")
	runID, rest, ok2 := strings.Cut(rest, "/")
	taskID, n, ok3 := strings.Cut(rest, "#")
	var violation int
	if _, err := fmt.Sscanf(n, "%d", &violation); !ok || !ok2 || !ok3 || err != nil || runID == "" || taskID == "" {
		return "", "", 0, fmt.Errorf("%w: alignment target must be run:<run>/<task>#<n>", model.ErrInvalid)
	}
	return runID, taskID, violation, nil
}
