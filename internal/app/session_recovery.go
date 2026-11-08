package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// SessionRecoveryHooks are trusted composition, never wire inputs. Freeze must
// serialize workspace/environment/budget mutations until release. Observe must
// recompute root/worktree/constitution/policy/budget and exact Goal/plan/run
// bindings, and independently prove the source workspace has stopped before
// adoption. No hooks means recovery is unavailable. SQLite independently
// rechecks its canonical Goal/policy/memory rows and capability at commit.
type SessionRecoveryHooks struct {
	Freeze  func(context.Context) (release func(), err error)
	Observe func(context.Context, model.SessionRecovery) (binding model.SessionRecoveryBinding, freshness MemoryReconcileRequest, sourceStopped bool, err error)
}

type SessionRecoveryService struct {
	runtime *Runtime
	hooks   SessionRecoveryHooks
}

func (r *Runtime) WorkspaceRecovery(hooks SessionRecoveryHooks) *SessionRecoveryService {
	return &SessionRecoveryService{r, hooks}
}

func (s *SessionRecoveryService) Get(ctx context.Context, id string, generation int64) (model.SessionRecovery, error) {
	if s == nil || s.runtime == nil || s.runtime.store == nil {
		return model.SessionRecovery{}, model.ErrUnavailable
	}
	r, err := s.runtime.store.SessionRecovery(ctx, id, generation)
	if err == nil && r.Binding.ProjectID != s.runtime.ProjectIdentity() {
		return model.SessionRecovery{}, authz.ErrDenied
	}
	return r, err
}

func (s *SessionRecoveryService) List(ctx context.Context) ([]model.SessionRecovery, error) {
	if s == nil || s.runtime == nil || s.runtime.store == nil {
		return nil, model.ErrUnavailable
	}
	return s.runtime.store.ListSessionRecoveries(ctx, s.runtime.ProjectIdentity())
}

// recoveryCommand only accepts the unforgeable local context minted by this
// Runtime. Actor/role/grant are never accepted from the recovery payload.
func (r *Runtime) recoveryCommand(ctx context.Context, e CommandEnvelope, input any) (store.CommandRecord, authz.Principal, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != e.ProjectID || r.ProjectIdentity() != e.ProjectID {
		return store.CommandRecord{}, authz.Principal{}, authz.ErrDenied
	}
	if e.SessionID == "" || e.TargetID == "" || e.ExpectedVersion < 0 || strings.TrimSpace(e.IdempotencyKey) == "" || len(e.IdempotencyKey) > 256 {
		return store.CommandRecord{}, authz.Principal{}, model.ErrInvalid
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	q := capability.Query{Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(e.ProjectID), Kind: capability.KindFilesystemWrite, Resource: r.layout.Database, Action: "session.recover"}
	decision, err := authz.CanWithCapability(ctx, principal, authz.AuthorityTaskPlan, r.layout.Database, q, capability.NewEngine(r.store, nil))
	if err != nil {
		return store.CommandRecord{}, authz.Principal{}, err
	}
	payload, err := json.Marshal(struct {
		Envelope CommandEnvelope
		Input    any
	}{e, input})
	if err != nil {
		return store.CommandRecord{}, authz.Principal{}, err
	}
	digest := sha256.Sum256(payload)
	key := sha256.Sum256([]byte(e.IdempotencyKey))
	return store.CommandRecord{ProjectID: e.ProjectID, Actor: p.ID(), Key: hex.EncodeToString(key[:]), Operation: "session.recover", SessionID: e.SessionID, TargetID: e.TargetID, ExpectedVersion: e.ExpectedVersion, Digest: hex.EncodeToString(digest[:]), CapabilityGrantID: decision.CapabilityGrantID}, principal, nil
}

// Checkpoint persists references to the original intent/Goal, not a provider
// turn or confirmation. Repeated checkpoints require exact head digest/CAS.
func (s *SessionRecoveryService) Checkpoint(ctx context.Context, e CommandEnvelope, binding model.SessionRecoveryBinding, memory []model.SessionRecoveryMemoryRef, expectedDigest string) (model.SessionRecovery, error) {
	return s.mutate(ctx, e, model.SessionRecovery{Version: 1, ID: e.TargetID, SourceSessionID: e.SessionID, Binding: binding, Memory: memory, State: "recoverable"}, expectedDigest)
}

func (s *SessionRecoveryService) Close(ctx context.Context, e CommandEnvelope, expectedDigest string) (model.SessionRecovery, error) {
	r, err := s.Get(ctx, e.TargetID, e.ExpectedVersion)
	if err != nil {
		return r, err
	}
	r.State = "closed"
	return s.mutate(ctx, e, r, expectedDigest)
}

func (s *SessionRecoveryService) Adopt(ctx context.Context, e CommandEnvelope, expectedDigest, destinationSession string) (model.SessionRecovery, error) {
	if e.ExpectedVersion < 1 || destinationSession == "" || len(destinationSession) > 256 || destinationSession == e.SessionID {
		return model.SessionRecovery{}, model.ErrInvalid
	}
	r, err := s.Get(ctx, e.TargetID, e.ExpectedVersion)
	if err != nil {
		return r, err
	}
	r.State = "adopted"
	r.AdoptedSessionID = destinationSession
	return s.mutate(ctx, e, r, expectedDigest)
}

func (s *SessionRecoveryService) mutate(ctx context.Context, e CommandEnvelope, next model.SessionRecovery, expectedDigest string) (model.SessionRecovery, error) {
	if s == nil || s.runtime == nil || s.hooks.Freeze == nil || s.hooks.Observe == nil {
		return model.SessionRecovery{}, model.ErrUnavailable
	}
	r := s.runtime
	next.Memory = append([]model.SessionRecoveryMemoryRef(nil), next.Memory...)
	input := struct {
		State, ExpectedDigest, Destination string
		Binding                            model.SessionRecoveryBinding
		Memory                             []model.SessionRecoveryMemoryRef
	}{next.State, expectedDigest, next.AdoptedSessionID, next.Binding, next.Memory}
	command, principal, err := r.recoveryCommand(ctx, e, input)
	if err != nil {
		return model.SessionRecovery{}, err
	}
	// Revocation and freshness apply even on replay. A receipt never confers
	// authority to reuse a now-inapplicable context.
	release, err := s.hooks.Freeze(ctx)
	if err != nil {
		return model.SessionRecovery{}, err
	}
	if release == nil {
		return model.SessionRecovery{}, model.ErrUnavailable
	}
	defer release()
	observed, freshness, stopped, err := s.hooks.Observe(ctx, next)
	if err != nil {
		return model.SessionRecovery{}, err
	}
	if observed != next.Binding || observed.ProjectID != r.ProjectIdentity() || observed.ProjectRoot != r.ProjectRoot() || observed.ConstitutionVersion != constitution.Current.String() || (next.State == "adopted" && !stopped) {
		return model.SessionRecovery{}, fmt.Errorf("%w: recovery environment changed or source is live", model.ErrConflict)
	}
	if r.memoryService == nil {
		return model.SessionRecovery{}, model.ErrUnavailable
	}
	refs := make([]MemoryReference, len(next.Memory))
	for i, m := range next.Memory {
		refs[i] = MemoryReference{ID: m.ID, Revision: m.Revision, Digest: m.Digest}
	}
	if _, err = r.memoryService.RevalidateReferences(ctx, principal, MemoryReferenceRequest{ProjectID: e.ProjectID, References: refs, Freshness: freshness}); err != nil {
		return model.SessionRecovery{}, err
	}
	if version, found, replayErr := r.store.CommandResult(ctx, command); replayErr != nil {
		return model.SessionRecovery{}, replayErr
	} else if found {
		// The canonical Goal/policy are rechecked by the read validation below.
		if err = r.store.ValidateSessionRecoveryCommand(ctx, next, command); err != nil {
			return model.SessionRecovery{}, err
		}
		return s.Get(ctx, e.TargetID, version)
	}
	next.Generation = e.ExpectedVersion + 1
	next.UpdatedAt = time.Now().UTC()
	next.Digest = next.ContentDigest()
	if next.Validate() != nil || next.SourceSessionID != e.SessionID {
		return model.SessionRecovery{}, model.ErrInvalid
	}
	// The external freeze remains held through commit. SQLite revalidates
	// canonical rows in that transaction; no callback queries its connection.
	err = r.store.WriteSessionRecovery(store.WithCommand(ctx, command), next, e.ExpectedVersion, expectedDigest, func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		if version, found, replayErr := r.store.CommandResult(ctx, command); replayErr == nil && found {
			if err = r.store.ValidateSessionRecoveryCommand(ctx, next, command); err != nil {
				return model.SessionRecovery{}, err
			}
			return s.Get(ctx, e.TargetID, version)
		}
		return model.SessionRecovery{}, err
	}
	return s.Get(ctx, next.ID, next.Generation)
}

func sessionProcessStamp(pid int) (string, error) {
	if runtime.GOOS != "linux" {
		if err := syscall.Kill(pid, 0); err != nil {
			return "", err
		}
		return "live-process-nonlinux", nil
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", model.ErrInvalid
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return "", model.ErrInvalid
	}
	return fields[19], nil
}

func sessionLivenessPath(root, sessionID string) (string, bool) {
	if root == "" || !marshalIdentifier(sessionID) || sessionID[0] == '.' {
		return "", false
	}
	return statepath.Path(root, "recovery", sessionID+".pid"), true
}

// RecordSessionLiveness records the PID and process birth stamp for an active workspace session.
func RecordSessionLiveness(root, sessionID string) {
	pidPath, ok := sessionLivenessPath(root, sessionID)
	if !ok {
		return
	}
	dir := filepath.Dir(pidPath)
	_ = os.MkdirAll(dir, 0700)
	pid := os.Getpid()
	stamp, err := sessionProcessStamp(pid)
	if err != nil || stamp == "" {
		return
	}
	_ = os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n%s\n", pid, stamp)), 0600)
}

// ClearSessionLiveness removes the session liveness marker on orderly exit.
func ClearSessionLiveness(root, sessionID string) {
	pidPath, ok := sessionLivenessPath(root, sessionID)
	if !ok {
		return
	}
	_ = os.Remove(pidPath)
}

// VerifySourceProcessStopped returns true if the source session's process is no longer running.
func VerifySourceProcessStopped(root, sourceSession, currentSession string) bool {
	if sourceSession == currentSession {
		return false
	}
	pidPath, ok := sessionLivenessPath(root, sourceSession)
	if !ok {
		return false
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 1 || len(lines) > 2 {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || pid <= 0 {
		return false
	}
	// Probe process existence
	if err := syscall.Kill(pid, 0); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return true
		}
		return false
	}
	if len(lines) != 2 {
		return false
	}
	stamp := strings.TrimSpace(lines[1])
	if stamp == "" {
		return false
	}
	// Only a matching live process stamp proves this is still the same source.
	// A changed stamp positively identifies PID reuse; any unreadable or
	// malformed process state remains unknown and therefore cannot be adopted.
	curStamp, err := sessionProcessStamp(pid)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true
		}
		return false
	}
	return curStamp != stamp
}
