//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
)

// rollingDispatchEnabled lets an admitted ULTRA batch refill freed worker
// slots during collection. Refill uses the production launch path: durable
// slot reservations, supervised launch receipts, staged exits with proof,
// and quota, cooldown and recovery admission. Standard stays batch.
const rollingDispatchEnabled = true

// rollingReviewPool is how many results review at once. Review seats are
// counted apart from worker slots.
const rollingReviewPool = 2

var (
	// ErrRollingDisabled refuses enabling rolling dispatch without hand-in proof.
	ErrRollingDisabled = errors.New("rolling dispatch is not enabled: hand-in proof is missing")
	// ErrRollingFenced reports a dispatcher whose lease was lost or shut
	// down: it starts no effect and keeps every record.
	ErrRollingFenced = errors.New("rolling dispatcher is fenced")

	errRollingResultRejected = errors.New("worker result quarantined")
)

// enableRollingDispatch is the production enablement point. It refuses
// while the gate is closed or no hand-in proof is supplied.
func enableRollingDispatch(proof rollingProof) error {
	if !rollingDispatchEnabled || proof == nil {
		return ErrRollingDisabled
	}
	return nil
}

// rollingRefillAvailable reports whether rolling refill may run. It needs a
// launch supervisor, which proves each worker's launch ownership and exit;
// a service without one keeps batch dispatch.
func (s *MarshalService) rollingRefillAvailable() bool {
	return rollingDispatchEnabled && s.LaunchSupervisor != nil
}

// rollingState is the dispatcher's durable record. One writer owns it: every
// save carries the writer's lease generation and the revision it read.
type rollingState struct {
	Revision   int64
	Generation int64
	Paused     bool
	// Serial is set when ULTRA is off: existing workers drain, then tasks
	// dispatch one at a time.
	Serial   bool
	Attempts []marshal.RollingAttempt
	// Rejected keeps quarantined results for inspection.
	Rejected []rollingRejected
}

type rollingRejected struct {
	Result rollingResult
	Reason string
}

// rollingStore persists the dispatcher's record. Save refuses a stale lease
// generation with ErrRollingFenced and a stale revision with a conflict.
type rollingStore interface {
	Load(ctx context.Context) (rollingState, error)
	Save(ctx context.Context, next rollingState) error
	// WithLease rejects a stale generation and excludes ownership changes until
	// effect returns. The callback must not call back into this store.
	WithLease(ctx context.Context, generation int64, effect func() error) error
}

// rollingCandidate is one plan task the dispatcher may admit.
type rollingCandidate struct {
	Task     string
	Attempt  int
	Provider string
	Files    []string
	Base     string
	Deps     []string
}

// rollingPlan is the approved plan the dispatcher works from.
type rollingPlan struct {
	Run    string
	Digest string
	Tasks  []rollingCandidate
}

// rollingLauncher starts supervised workers and proves what exists for a
// launch key after a restart.
type rollingLauncher interface {
	Launch(ctx context.Context, a marshal.RollingAttempt) (driver.LaunchReceipt, error)
	Inspect(key string) (driver.LaunchDisposition, error)
}

// rollingResult is a staged worker exit for one attempt.
type rollingResult struct {
	LaunchKey  string
	Task       string
	Attempt    int
	PlanDigest string
	Exit       marshal.StagedExit
}

// rollingResults is the transactional source of staged worker exits: a
// result stays staged until acknowledged.
type rollingResults interface {
	Staged(ctx context.Context) ([]rollingResult, error)
	Ack(ctx context.Context, key string) error
}

// rollingAdmission decides worker capacity for a candidate given the
// attempts that hold worker slots.
type rollingAdmission interface {
	Admit(c rollingCandidate, workers []marshal.RollingAttempt) DispatchAdmission
}

// rollingProof validates a staged result before it is adopted.
type rollingProof interface {
	Validate(a marshal.RollingAttempt, r rollingResult) error
}

// rollingReviewer starts a review; completion arrives by CompleteReview.
type rollingReviewer interface {
	StartReview(ctx context.Context, a marshal.RollingAttempt) error
}

type rollingDeps struct {
	Store     rollingStore
	Launcher  rollingLauncher
	Results   rollingResults
	Admission rollingAdmission
	Proof     rollingProof
	Reviewer  rollingReviewer
}

// rollingScheduler is the single writer of rolling dispatch state.
type rollingScheduler struct {
	mu     sync.Mutex
	deps   rollingDeps
	plan   rollingPlan
	st     rollingState
	fenced bool
}

// acquireRollingScheduler takes the lease with a new generation, which
// fences any earlier writer, then reconciles every owned attempt against
// the plan and the launch receipts before anything replays.
func acquireRollingScheduler(ctx context.Context, plan rollingPlan, deps rollingDeps) (*rollingScheduler, error) {
	if deps.Store == nil || deps.Launcher == nil || deps.Results == nil || deps.Admission == nil || deps.Proof == nil || deps.Reviewer == nil {
		return nil, errors.New("rolling dispatcher is missing a launch, result, admission, proof or review interface")
	}
	st, err := deps.Store.Load(ctx)
	if err != nil {
		return nil, err
	}
	s := &rollingScheduler{deps: deps, plan: plan, st: st}
	next := s.clone()
	next.Generation++
	if err := s.commit(ctx, next); err != nil {
		return nil, err
	}
	if err := s.reconcile(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *rollingScheduler) clone() rollingState {
	next := s.st
	next.Attempts = slices.Clone(s.st.Attempts)
	next.Rejected = slices.Clone(s.st.Rejected)
	return next
}

// commit saves next as the following revision under this writer's lease.
func (s *rollingScheduler) commit(ctx context.Context, next rollingState) error {
	if s.fenced {
		return ErrRollingFenced
	}
	next.Revision = s.st.Revision + 1
	if err := s.deps.Store.Save(ctx, next); err != nil {
		if errors.Is(err, ErrRollingFenced) || errors.Is(err, model.ErrConflict) {
			s.fenced = true
		}
		return err
	}
	s.st = next
	return nil
}

// Fence stops the writer after lease loss or shutdown. Records, staged
// results and live reservations stay for the next owner.
func (s *rollingScheduler) Fence() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fenced = true
}

func (s *rollingScheduler) state() rollingState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clone()
}

func rollingLaunchKey(run, task string, attempt int, planDigest string) string {
	return "rolling:" + digest(run, task, strconv.Itoa(attempt), planDigest)
}

func (st rollingState) index(key string) int {
	for i := range st.Attempts {
		if st.Attempts[i].LaunchKey == key {
			return i
		}
	}
	return -1
}

func (st rollingState) workers() []marshal.RollingAttempt {
	var out []marshal.RollingAttempt
	for _, a := range st.Attempts {
		if a.Reservation {
			out = append(out, a)
		}
	}
	return out
}

func quarantineAttempt(a *marshal.RollingAttempt, reason string, released bool) {
	a.Phase = marshal.RollingQuarantined
	a.Reason = reason
	if released {
		a.Reservation = false
	}
}

// reconcile rechecks owned attempts after the lease is (re)acquired. A
// proved exit or absence releases a slot; anything unproved keeps it.
func (s *rollingScheduler) reconcile(ctx context.Context) error {
	next := s.clone()
	for i := range next.Attempts {
		a := &next.Attempts[i]
		if a.Phase != marshal.RollingReserved && a.Phase != marshal.RollingRunning {
			switch {
			case a.Phase == marshal.RollingDone || a.Phase == marshal.RollingQuarantined:
			case a.PlanDigest != s.plan.Digest:
				quarantineAttempt(a, "plan superseded", true)
			case a.Phase == marshal.RollingReview:
				// The earlier owner's review ended with it: review again.
				a.Phase = marshal.RollingStaged
			}
			continue
		}
		disp, err := s.deps.Launcher.Inspect(a.LaunchKey)
		if err != nil {
			disp = driver.LaunchAmbiguous
		}
		gone := disp == driver.LaunchExited || disp == driver.LaunchAbsent
		switch {
		case a.PlanDigest != s.plan.Digest:
			quarantineAttempt(a, "plan superseded", gone)
		case disp == driver.LaunchAmbiguous:
			quarantineAttempt(a, "worker launch is ambiguous", false)
		case a.Phase == marshal.RollingReserved && disp == driver.LaunchAbsent:
			// Proved absent: the same key may launch again.
		case a.Phase == marshal.RollingReserved:
			quarantineAttempt(a, "worker started without a recorded receipt", disp == driver.LaunchExited)
		case disp == driver.LaunchRunning:
		case disp == driver.LaunchExited:
			a.Phase = marshal.RollingExited
			a.Reservation = false
		default:
			quarantineAttempt(a, "receipted worker is absent", true)
		}
		if a.Phase != marshal.RollingQuarantined {
			a.LeaseGeneration = next.Generation
		}
	}
	return s.commit(ctx, next)
}

// Tick drains staged results, starts reviews and refills worker slots.
func (s *rollingScheduler) Tick(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fenced {
		return ErrRollingFenced
	}
	if err := s.drainResults(ctx); err != nil {
		return err
	}
	if s.st.Paused {
		return nil
	}
	if err := s.startReviews(ctx); err != nil {
		return err
	}
	return s.admit(ctx)
}

// drainResults adopts each staged result that matches its attempt exactly.
// A verified exit releases the worker slot even while the run is paused;
// review waits for resume. Others are quarantined for inspection.
func (s *rollingScheduler) drainResults(ctx context.Context) error {
	staged, err := s.deps.Results.Staged(ctx)
	if err != nil {
		return err
	}
	var rejected error
	for _, r := range staged {
		next := s.clone()
		i := next.index(r.LaunchKey)
		reason := ""
		switch {
		case i < 0:
			reason = "result matches no owned attempt"
		case next.Attempts[i].ResultReceipt != nil:
			prev := next.Attempts[i].ResultReceipt
			if prev.ExitDigest == r.Exit.ExitDigest && prev.EvidenceDigest == r.Exit.EvidenceDigest {
				// The same result replayed after a restart.
				if err := s.deps.Results.Ack(ctx, r.LaunchKey); err != nil {
					return err
				}
				continue
			}
			reason = "attempt already has a result"
		default:
			reason = s.resultMismatch(next.Attempts[i], r)
		}
		if reason != "" {
			next.Rejected = append(next.Rejected, rollingRejected{Result: r, Reason: reason})
			rejected = errors.Join(rejected, fmt.Errorf("%w: %s: %s", errRollingResultRejected, r.LaunchKey, reason))
		} else {
			a := &next.Attempts[i]
			exit := r.Exit
			a.ResultReceipt = &exit
			a.Reservation = false
			a.Phase = marshal.RollingStaged
		}
		if err := s.commit(ctx, next); err != nil {
			return err
		}
		if err := s.deps.Results.Ack(ctx, r.LaunchKey); err != nil {
			return err
		}
	}
	return rejected
}

func (s *rollingScheduler) resultMismatch(a marshal.RollingAttempt, r rollingResult) string {
	if a.Task != r.Task || a.Attempt != r.Attempt || a.PlanDigest != r.PlanDigest || a.PlanDigest != s.plan.Digest {
		return "result does not match its attempt"
	}
	if a.Phase != marshal.RollingRunning && a.Phase != marshal.RollingExited {
		return "attempt is " + string(a.Phase)
	}
	if (r.Exit.Proof != marshal.ExitProofExited && r.Exit.Proof != marshal.ExitProofAbsent) || r.Exit.ExitDigest == "" {
		return "worker exit is not proved"
	}
	if err := s.deps.Proof.Validate(a, r); err != nil {
		return "hand-in proof failed: " + err.Error()
	}
	return ""
}

// startReviews fills free review seats with staged results.
func (s *rollingScheduler) startReviews(ctx context.Context) error {
	seats := rollingReviewPool
	for _, a := range s.st.Attempts {
		if a.Phase == marshal.RollingReview {
			seats--
		}
	}
	for i := 0; i < len(s.st.Attempts) && seats > 0; i++ {
		a := s.st.Attempts[i]
		if a.Phase != marshal.RollingStaged || a.AwaitingApproval {
			continue
		}
		next := s.clone()
		next.Attempts[i].Phase = marshal.RollingReview
		if err := s.commit(ctx, next); err != nil {
			return err
		}
		seats--
		if err := s.deps.Reviewer.StartReview(ctx, next.Attempts[i]); err != nil {
			// The seat is not held by a review that never started.
			back := s.clone()
			back.Attempts[i].Phase = marshal.RollingStaged
			return errors.Join(err, s.commit(ctx, back))
		}
	}
	return nil
}

func (s *rollingScheduler) dependencyHeads(c rollingCandidate) (map[string]string, bool) {
	heads := map[string]string{}
	for _, dep := range c.Deps {
		merged := false
		for _, a := range s.st.Attempts {
			if a.Run == s.plan.Run && a.PlanDigest == s.plan.Digest && a.Task == dep && a.Outcome == marshal.Merged {
				merged = true
				if a.ResultReceipt != nil {
					heads[dep] = a.ResultReceipt.HandInRef
				}
			}
		}
		if !merged {
			return nil, false
		}
	}
	return heads, true
}

// admit relaunches proved-absent reservations, then admits candidates while
// worker capacity, file scopes and dependencies allow.
func (s *rollingScheduler) admit(ctx context.Context) error {
	for i := range s.st.Attempts {
		if a := s.st.Attempts[i]; a.Phase == marshal.RollingReserved && a.Reservation {
			if err := s.launch(ctx, a.LaunchKey); err != nil {
				return err
			}
		}
	}
	for _, c := range s.plan.Tasks {
		workers := s.st.workers()
		if s.st.Serial && len(workers) > 0 {
			return nil
		}
		key := rollingLaunchKey(s.plan.Run, c.Task, c.Attempt, s.plan.Digest)
		if s.st.index(key) >= 0 {
			continue
		}
		heads, ok := s.dependencyHeads(c)
		if !ok || s.scopeBlocked(c) {
			continue
		}
		if !s.deps.Admission.Admit(c, workers).Allowed {
			continue
		}
		next := s.clone()
		next.Attempts = append(next.Attempts, marshal.RollingAttempt{
			Run: s.plan.Run, Task: c.Task, Attempt: c.Attempt, Revision: s.st.Revision + 1,
			PlanDigest: s.plan.Digest, LeaseGeneration: s.st.Generation, LaunchKey: key,
			Base: c.Base, DependencyHeads: heads, Provider: c.Provider, Files: slices.Clone(c.Files),
			Reservation: true, Phase: marshal.RollingReserved,
		})
		// The reservation is durable before the launch effect.
		if err := s.commit(ctx, next); err != nil {
			return err
		}
		if err := s.launch(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

// scopeBlocked keeps file scopes fenced through merge, return or cancel.
func (s *rollingScheduler) scopeBlocked(c rollingCandidate) bool {
	for _, a := range s.st.Attempts {
		if a.ScopeHeld() && taskScopeConflict(c.Files, a.Files) != "" {
			return true
		}
	}
	return false
}

// launch starts the reserved attempt under key and records its receipt. A
// failed or unproved launch is quarantined; its slot is released only when
// the launch is proved absent.
func (s *rollingScheduler) launch(ctx context.Context, key string) error {
	if s.fenced {
		return ErrRollingFenced
	}
	i := s.st.index(key)
	var receipt driver.LaunchReceipt
	err := s.deps.Store.WithLease(ctx, s.st.Generation, func() error {
		var launchErr error
		receipt, launchErr = s.deps.Launcher.Launch(ctx, s.st.Attempts[i])
		return launchErr
	})
	if errors.Is(err, ErrRollingFenced) {
		s.fenced = true
		return err
	}
	next := s.clone()
	a := &next.Attempts[i]
	switch {
	case err != nil:
		disp, inspectErr := s.deps.Launcher.Inspect(key)
		quarantineAttempt(a, "launch failed: "+err.Error(), inspectErr == nil && disp == driver.LaunchAbsent)
	case receipt.Key != key || receipt.Stage != driver.LaunchReceipted:
		quarantineAttempt(a, "launch receipt does not prove this attempt", false)
	default:
		a.Phase = marshal.RollingRunning
		a.ProcessReceipt = digest(receipt.Key, receipt.BootID, strconv.Itoa(receipt.Process.PID), receipt.Process.Start)
	}
	return s.commit(ctx, next)
}

func (s *rollingScheduler) update(ctx context.Context, key string, fn func(*marshal.RollingAttempt) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fenced {
		return ErrRollingFenced
	}
	next := s.clone()
	i := next.index(key)
	if i < 0 {
		return model.ErrNotFound
	}
	if err := fn(&next.Attempts[i]); err != nil {
		return err
	}
	return s.commit(ctx, next)
}

// WorkerSlotReleased records a proved exit the slot accounting released.
func (s *rollingScheduler) WorkerSlotReleased(_ string, key string) error {
	return s.update(context.Background(), key, func(a *marshal.RollingAttempt) error {
		if a.Phase == marshal.RollingRunning || a.Phase == marshal.RollingReserved {
			a.Phase = marshal.RollingExited
		}
		a.Reservation = false
		return nil
	})
}

// ParkApproval parks an attempt on an approval. A live worker keeps its
// slot; after a verified exit the approval holds none.
func (s *rollingScheduler) ParkApproval(ctx context.Context, key string, waiting bool) error {
	return s.update(ctx, key, func(a *marshal.RollingAttempt) error {
		a.AwaitingApproval = waiting
		return nil
	})
}

// CompleteReview ends a review with merge, return or cancel, which frees
// the attempt's file scope and its review seat.
func (s *rollingScheduler) CompleteReview(ctx context.Context, key string, outcome marshal.TaskState) error {
	return s.update(ctx, key, func(a *marshal.RollingAttempt) error {
		if a.Phase != marshal.RollingReview {
			return fmt.Errorf("attempt %s is not in review", key)
		}
		if outcome != marshal.Merged && outcome != marshal.Returned && outcome != marshal.Cancelled {
			return fmt.Errorf("review outcome %s does not end the attempt", outcome)
		}
		a.Phase = marshal.RollingDone
		a.Outcome = outcome
		return nil
	})
}

// SetPaused stops or resumes admission and review. Exits still stage.
func (s *rollingScheduler) SetPaused(ctx context.Context, paused bool) error {
	return s.setMode(ctx, func(st *rollingState) { st.Paused = paused })
}

// SetUltra turns rolling capacity on or off; off drains to serial dispatch.
func (s *rollingScheduler) SetUltra(ctx context.Context, on bool) error {
	return s.setMode(ctx, func(st *rollingState) { st.Serial = !on })
}

func (s *rollingScheduler) setMode(ctx context.Context, fn func(*rollingState)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.clone()
	fn(&next)
	return s.commit(ctx, next)
}
