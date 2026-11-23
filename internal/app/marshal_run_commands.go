package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

const runCommandLimit = 128

type RunCommandInput struct{ Operation, TaskID, Worker, Reason string }
type runCommandCommitKey struct{}

type runWriter struct {
	mu          sync.Mutex
	stateMu     sync.Mutex
	active      bool
	executionID string
	wake        chan struct{}
}

func (w *runWriter) activeID() string {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if w.active {
		return w.executionID
	}
	return ""
}

func (w *runWriter) isActive() bool {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	return w.active
}

func (w *runWriter) startExecution(id string) (string, bool) {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if w.active {
		return w.executionID, false
	}
	w.active = true
	w.executionID = id
	return id, true
}

func (w *runWriter) endExecution() {
	w.stateMu.Lock()
	w.active = false
	w.executionID = ""
	w.stateMu.Unlock()
}

var runWriters sync.Map

func (s *MarshalService) runWriter(runID string) *runWriter {
	key := s.Repository + "\x00" + s.ProjectID + "\x00" + runID
	v, _ := runWriters.LoadOrStore(key, &runWriter{wake: make(chan struct{}, 1)})
	return v.(*runWriter)
}

// ActiveExecution identifies the writer already driving this run.
func (s *MarshalService) ActiveExecution(runID string) string {
	return s.runWriter(runID).activeID()
}

// CommandRun admits local operator requests into the run's durable FIFO.
// Queue writes and lifecycle effects use the same writer as batch collection.
func (s *MarshalService) CommandRun(ctx context.Context, e CommandEnvelope, input RunCommandInput) (marshal.RunCommand, error) {
	p, ok := auth.LocalFromContext(ctx)
	r := s.runtime
	if r == nil || !ok || ctx.Value(localControlKey{}) != r || e.ProjectID != r.ProjectIdentity() || p.ProjectID() != e.ProjectID {
		return marshal.RunCommand{}, authz.ErrDenied
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	query := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(e.ProjectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: "run.control"}
	decision, err := authz.CanWithCapability(ctx, principal, authz.AuthorityTaskPlan, r.layout.Database, query, capability.NewEngine(s.Store, nil))
	if err != nil {
		return marshal.RunCommand{}, err
	}
	c := marshal.RunCommand{ID: e.IdempotencyKey, Actor: p.ID(), GrantID: decision.CapabilityGrantID, AuthorityProject: e.ProjectID, Resource: r.layout.Database,
		Operation: input.Operation, TaskID: input.TaskID, Worker: input.Worker, Reason: input.Reason, Revision: e.ExpectedVersion}
	payload, _ := json.Marshal(struct {
		Envelope CommandEnvelope
		Input    RunCommandInput
	}{e, input})
	c.Digest = fmt.Sprintf("%x", sha256.Sum256(payload))
	if e.TargetID != input.TaskID || e.SessionID == "" || c.ID == "" || len(c.ID) > 256 {
		return c, model.ErrInvalid
	}
	return s.enqueueRunCommand(ctx, e.SessionID, c)
}

func taskCommandDigest(t marshal.Task) string {
	data, _ := json.Marshal(t)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (s *MarshalService) checkRunCommandAuthority(ctx context.Context, c marshal.RunCommand) error {
	if err := s.requireLifecycleOwner(); err != nil {
		return err
	}
	if s.runtime != nil {
		owner, err := auth.LocalOwner(s.runtime.layout.RuntimeDir, c.AuthorityProject)
		if err != nil || owner.ID() != c.Actor || s.runtime.ProjectIdentity() != c.AuthorityProject {
			return authz.ErrDenied
		}
	}
	g, err := s.Store.GetCapabilityGrant(ctx, capability.GrantID(c.GrantID))
	if err != nil {
		return err
	}
	if g.RevokedAt != nil || !time.Now().Before(g.ExpiresAt) || time.Now().Before(g.IssuedAt) || string(g.Subject) != c.Actor || string(g.TaskID) != c.AuthorityProject || g.Kind != capability.KindFilesystemWrite || g.Scope.Resource != c.Resource || len(g.Scope.Constraints) != 0 {
		return authz.ErrDenied
	}
	for _, action := range g.Scope.Actions {
		if action == "run.control" {
			return nil
		}
	}
	return authz.ErrDenied
}

func (s *MarshalService) enqueueRunCommand(ctx context.Context, runID string, c marshal.RunCommand) (marshal.RunCommand, error) {
	w := s.runWriter(runID)
	w.mu.Lock()
	defer w.mu.Unlock()
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return c, err
	}
	for _, old := range run.Commands {
		if old.ID == c.ID && old.Actor == c.Actor {
			if old.Digest != c.Digest {
				return c, model.ErrConflict
			}
			return old, nil
		}
	}
	// The run keeps changing while execution continues, so the command is
	// not bound to the run revision it was typed against. Task commands are
	// bound to the task digest below and rechecked when they are drained;
	// the save itself is still compare-and-swap.
	if err = s.checkRunCommandAuthority(ctx, c); err != nil {
		return c, err
	}
	priority := c.Operation == "pause" || c.Operation == "stop"
	limit := runCommandLimit
	if priority {
		limit++
	}
	if len(run.Commands) >= limit {
		return c, errors.New("run command journal is full")
	}
	if !priority && run.CommandFence != "" {
		return c, errors.New("run command admission is fenced: " + run.CommandFence)
	}
	switch c.Operation {
	case "return", "accept", "reassign", "cancel":
		i := taskIndex(run, c.TaskID)
		if i < 0 {
			return c, model.ErrNotFound
		}
		c.TaskDigest = taskCommandDigest(run.Tasks[i])
		if c.Operation == "accept" {
			c.Purpose, err = s.TaskAcceptance(ctx, runID, c.TaskID)
			if err != nil {
				return c, err
			}
		}
	case "pause", "stop":
		if run.CommandFence != "" {
			return c, errors.New("a priority signal is already pending")
		}
		run.CommandFence = c.Operation
	default:
		return c, model.ErrInvalid
	}
	c.PlanID, c.PlanVersion, c.ScopeDigest = run.PlanID, run.PlanVersion, run.ApprovalScopeDigest
	c.State = "queued"
	run.Commands = append(run.Commands, c)
	admissionCtx := store.WithMarshalCommandAdmission(ctx, store.CommandRecord{ProjectID: c.AuthorityProject, Actor: c.Actor, Key: c.ID, SessionID: runID, Digest: c.Digest})
	if err = s.save(admissionCtx, runID, run, rev); err != nil {
		return c, err
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
	if !w.isActive() {
		if err = s.drainRunCommands(ctx, runID); err != nil {
			return c, err
		}
		run, _, err = s.load(ctx, runID)
		if err != nil {
			return c, err
		}
		for _, receipt := range run.Commands {
			if receipt.ID == c.ID && receipt.Actor == c.Actor {
				return receipt, nil
			}
		}
	}
	return c, nil
}

// drainRunCommands runs only while the caller owns the run writer. Applying
// requests never replay after interruption: their state write carries the
// terminal receipt, and any earlier interruption is held for inspection.
func (s *MarshalService) drainRunCommands(ctx context.Context, runID string) error {
	for {
		run, rev, err := s.load(ctx, runID)
		if err != nil {
			return err
		}
		if run.Operation != nil {
			run, rev, err = s.recoverOperation(ctx, runID, run, rev)
			if err != nil {
				return err
			}
		}
		index := -1
		for i, c := range run.Commands {
			if c.State == "applying" {
				index = i
				break
			}
		}
		if index < 0 {
			live := false
			for _, task := range run.Tasks {
				if s.dispatchLive(runID, task.PlanTaskID) {
					live = true
					break
				}
			}
			for i, c := range run.Commands {
				if c.State == "reconciling" && !live {
					index = i
					break
				}
			}
		}
		if index < 0 {
			for i, c := range run.Commands {
				if c.State == "queued" && (c.Operation == "pause" || c.Operation == "stop") {
					index = i
					break
				}
			}
		}
		if index < 0 {
			for i, c := range run.Commands {
				if c.State == "queued" {
					index = i
					break
				}
			}
		}
		if index < 0 {
			return nil
		}
		c := run.Commands[index]
		if c.State == "reconciling" {
			c.State, c.Detail = "applied", "admitted workers reconciled; new starts remain fenced"
		} else if c.State == "applying" {
			run.CommandFence = "interrupted command"
			if run.State != marshal.AwaitingUser && run.State != marshal.Closed {
				pauseMarshal(&run, marshal.Pause{Reason: "interrupted task command; inspect retained evidence", Code: marshal.CodeInterruptedOperation, Action: marshal.PauseResume, Resolutions: []string{"inspect the command receipt and retained evidence"}})
			}
			c.State, c.Detail = "held", "interrupted before task state commit; inspect retained evidence before issuing a new command"
		} else if err = s.checkRunCommandAuthority(ctx, c); err != nil {
			c.State, c.Detail = "refused", err.Error()
		} else if c.PlanID != run.PlanID || c.PlanVersion != run.PlanVersion || c.ScopeDigest != run.ApprovalScopeDigest {
			c.State, c.Detail = "refused", "approved plan changed"
		} else if c.Operation == "pause" || c.Operation == "stop" {
			if run.State != marshal.AwaitingUser && run.State != marshal.Closed {
				pauseMarshal(&run, marshal.Pause{Reason: "operator requested " + c.Operation, ResumeState: run.State, Code: marshal.CodeOperatorIntervention, Action: marshal.PauseResume, Resolutions: []string{"resume after inspecting the run"}})
			}
			c.State, c.Detail = "reconciling", "new starts fenced; admitted workers are being reconciled"
		} else if run.CommandFence != "" {
			c.State, c.Detail = "refused", "run paused before command started"
		} else if i := taskIndex(run, c.TaskID); i < 0 || taskCommandDigest(run.Tasks[i]) != c.TaskDigest {
			c.State, c.Detail = "refused", "task state or attempt changed"
		} else {
			c.State = "applying"
		}
		if c.State == "refused" && (c.Operation == "pause" || c.Operation == "stop") && run.CommandFence == c.Operation {
			// A refused priority signal must not leave new starts fenced.
			run.CommandFence = ""
		}
		run.Commands[index] = c
		if err = s.save(ctx, runID, run, rev); err != nil {
			return err
		}
		if c.State != "applying" {
			continue
		}
		commandCtx := context.WithValue(ctx, runCommandCommitKey{}, c)
		authorized := *s
		authorized.ApprovalActor = func(_ context.Context, id, purpose string) (string, error) {
			if id == runID && ((c.Operation == "return" && purpose == "return:"+c.TaskID) || (c.Operation == "accept" && purpose == c.Purpose)) {
				return c.Actor, nil
			}
			return "", authz.ErrDenied
		}
		switch c.Operation {
		case "return":
			_, err = authorized.ReturnByUser(commandCtx, runID, c.TaskID, c.Reason)
		case "reassign":
			err = authorized.ReassignByOperator(commandCtx, runID, c.TaskID, c.Worker, c.Reason)
		case "cancel":
			err = authorized.CancelTask(commandCtx, runID, c.TaskID)
		case "accept":
			_, err = authorized.Review(commandCtx, runID, c.TaskID, zeroMarshalCharge())
		}
		after, nextRev, loadErr := s.load(context.WithoutCancel(ctx), runID)
		if loadErr != nil {
			return errors.Join(err, loadErr)
		}
		// If an effect committed, its atomic terminal receipt wins over a later
		// reporting error. Otherwise retain a durable refusal without replay.
		if after.Commands[index].State == "applying" {
			after.Commands[index].State = "refused"
			after.Commands[index].Detail = "command made no task state commit"
			if err != nil {
				after.Commands[index].Detail = err.Error()
			}
			if saveErr := s.save(context.WithoutCancel(ctx), runID, after, nextRev); saveErr != nil {
				return errors.Join(err, saveErr)
			}
		}
	}
}

type runWriterContextKey struct{}

func (s *MarshalService) runCommandSafePoint(ctx context.Context, runID string) error {
	if w, ok := ctx.Value(runWriterContextKey{}).(*runWriter); ok {
		w.mu.Unlock()
		w.mu.Lock()
	}
	return s.drainRunCommands(ctx, runID)
}

func (s *MarshalService) waitRunChange(ctx context.Context, runID string, changed <-chan struct{}) (bool, error) {
	w, _ := ctx.Value(runWriterContextKey{}).(*runWriter)
	var wake <-chan struct{}
	if w != nil {
		wake = w.wake
		w.mu.Unlock()
		defer w.mu.Lock()
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-changed:
		return true, nil
	case <-wake:
		return true, nil
	}
}
