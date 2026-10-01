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

// CommandRunControl pauses, resumes or cancels one execution run through the
// operator boundary with the run.control capability. The envelope targets
// "run:<id>" at the exact run version the operator saw.
//
//   - pause stops new dispatch at the next task boundary; a task already in a
//     provider turn finishes it, so the result reports whether the run is
//     still settling.
//   - cancel ends the run and also cancels the supervised provider turns of
//     its tasks; their processes stop when the turn context is cancelled.
//   - resume re-validates the goal binding, marks the run RUNNING and starts
//     canonical execution again in the background.
func (r *Runtime) CommandRunControl(ctx context.Context, e CommandEnvelope, operation string) (execution.RunControlResult, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != e.ProjectID || e.ProjectID != r.ProjectIdentity() {
		return execution.RunControlResult{}, authz.ErrDenied
	}
	if operation != "pause" && operation != "resume" && operation != "cancel" {
		return execution.RunControlResult{}, model.ErrInvalid
	}
	runID, typed := strings.CutPrefix(e.TargetID, "run:")
	if !typed || runID == "" || e.ExpectedVersion < 0 || strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > 256 {
		return execution.RunControlResult{}, model.ErrInvalid
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(e.ProjectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: "run.control"}
	decision, err := authz.CanWithCapability(ctx, principal, authz.AuthorityTaskPlan, r.layout.Database, query, capability.NewEngine(r.store, nil))
	if err != nil {
		return execution.RunControlResult{}, err
	}
	payload, err := json.Marshal(struct {
		Envelope  CommandEnvelope
		Operation string
	}{e, operation})
	if err != nil {
		return execution.RunControlResult{}, err
	}
	digest := sha256.Sum256(payload)
	key := sha256.Sum256([]byte(e.IdempotencyKey))
	record := store.CommandRecord{ProjectID: e.ProjectID, Actor: p.ID(), Key: hex.EncodeToString(key[:]), Operation: "run." + operation,
		SessionID: e.SessionID, TargetID: e.TargetID, ExpectedVersion: e.ExpectedVersion, Digest: hex.EncodeToString(digest[:]),
		CapabilityGrantID: decision.CapabilityGrantID}
	engine := r.Execution().Engine()
	if engine == nil {
		return execution.RunControlResult{}, model.ErrUnavailable
	}
	if _, found, err := r.store.CommandResult(ctx, record); err != nil {
		return execution.RunControlResult{}, err
	} else if found {
		run, err := engine.GetRun(ctx, runID)
		return execution.RunControlResult{Run: run, Executing: engine.IsExecuting(runID)}, err
	}
	current, err := engine.GetRun(ctx, runID)
	if err != nil {
		return execution.RunControlResult{}, err
	}
	if current.SessionID != e.SessionID {
		return execution.RunControlResult{}, authz.ErrDenied
	}
	result, err := engine.ControlRun(ctx, runID, operation, e.ExpectedVersion)
	if err != nil {
		return execution.RunControlResult{}, err
	}
	if err := r.store.RecordCommandReceipt(ctx, record, result.Run.Version); err != nil {
		return execution.RunControlResult{}, err
	}
	switch operation {
	case "cancel":
		r.cancelRunTurns(result.Run)
	case "resume":
		start := r.resumeRun
		if start == nil {
			start = func(runID string) { _, _ = r.Execution().ExecuteRun(context.Background(), runID) }
		}
		go start(runID)
	}
	return result, nil
}

// cancelRunTurns cancels the supervised provider turn of every task of the run.
func (r *Runtime) cancelRunTurns(run execution.ExecutionRun) {
	r.taskMu.Lock()
	defer r.taskMu.Unlock()
	for id, task := range run.Tasks {
		for _, key := range []string{task.CanonicalTaskID, id} {
			if cancel := r.taskRuns[key]; key != "" && cancel != nil {
				cancel()
			}
		}
	}
}
