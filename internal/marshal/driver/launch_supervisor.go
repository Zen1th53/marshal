package driver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// LaunchKeyEnv carries a supervised launch's key into the held child, so a
// restarted owner can rediscover the exact process it started.
const LaunchKeyEnv = "MARSHAL_LAUNCH_KEY"

// ErrLaunchUnsupported reports a driver or platform that has no real startup
// barrier or no verifiable process identity.
var ErrLaunchUnsupported = errors.New("supervised launch unsupported")

// LaunchStage is how far a supervised launch got before its owner stopped.
type LaunchStage string

const (
	LaunchIntent    LaunchStage = "intent"
	LaunchReceipted LaunchStage = "receipt"
	LaunchAttached  LaunchStage = "attached"
)

// ProcessIdentity binds a process to its boot and kernel start time, so a
// reused PID is never mistaken for the launched worker.
type ProcessIdentity struct {
	BootID string `json:"boot_id"`
	PID    int    `json:"pid"`
	Start  string `json:"start"`
}

func (p ProcessIdentity) verifiable() bool {
	return p.BootID != "" && p.PID > 0 && p.Start != ""
}

// LaunchReceipt is the durable record of one supervised launch. It lives
// outside the task worktree, where the worker cannot rewrite it.
type LaunchReceipt struct {
	Key         string          `json:"key"`
	Stage       LaunchStage     `json:"stage"`
	BootID      string          `json:"boot_id"`
	Provider    string          `json:"provider,omitempty"`
	Process     ProcessIdentity `json:"process"`
	SessionID   string          `json:"session_id,omitempty"`
	Quarantined bool            `json:"quarantined,omitempty"`
	Reason      string          `json:"reason,omitempty"`
}

// LaunchJournal stores launch receipts durably.
type LaunchJournal interface {
	Load(key string) (LaunchReceipt, bool, error)
	Store(LaunchReceipt) error
	Remove(key string) error
	Root() string
}

// FileLaunchJournal keeps one fsynced JSON file per launch key.
type FileLaunchJournal struct{ Dir string }

func (j FileLaunchJournal) Root() string { return j.Dir }

func (j FileLaunchJournal) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(j.Dir, hex.EncodeToString(sum[:])+".json")
}

func (j FileLaunchJournal) Load(key string) (LaunchReceipt, bool, error) {
	data, err := os.ReadFile(j.path(key))
	if os.IsNotExist(err) {
		return LaunchReceipt{}, false, nil
	}
	if err != nil {
		return LaunchReceipt{}, false, err
	}
	var rec LaunchReceipt
	if err := json.Unmarshal(data, &rec); err != nil {
		return LaunchReceipt{}, false, fmt.Errorf("launch receipt unreadable: %w", err)
	}
	if rec.Key != key {
		return LaunchReceipt{}, false, errors.New("launch receipt key differs")
	}
	return rec, true, nil
}

func (j FileLaunchJournal) Store(rec LaunchReceipt) error {
	if err := os.MkdirAll(j.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(j.Dir, ".receipt-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(name, j.path(rec.Key)); err != nil {
		return err
	}
	return syncDir(j.Dir)
}

func (j FileLaunchJournal) Remove(key string) error {
	if err := os.Remove(j.path(key)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncDir(j.Dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// ProcessAdapter observes and stops processes by exact identity.
type ProcessAdapter interface {
	// BootID names the running OS boot; an error means rolling is unavailable.
	BootID() (string, error)
	// Find lists live processes that registered the launch key.
	Find(key string) ([]ProcessIdentity, error)
	// Alive reports whether exactly this process still runs.
	Alive(ProcessIdentity) (bool, error)
	// Stop terminates exactly this process and returns nil only once its exit
	// is proven.
	Stop(ProcessIdentity) error
}

// HeldStart is a started worker blocked at its startup barrier. It does no
// work until Release opens the barrier.
type HeldStart struct {
	Process   ProcessIdentity
	SessionID string
	// Release opens the barrier and returns the running worker's handle.
	Release func() (*Handle, error)
	// Abort refuses the barrier, stops the child and proves its exit.
	Abort func() error
}

// SupervisedLauncher is implemented by drivers whose launch has a real
// startup barrier and a verifiable process identity on this platform.
type SupervisedLauncher interface {
	LaunchHeld(ctx context.Context, req Request, key string) (*HeldStart, error)
}

// LaunchDisposition is what a restarted owner can prove about a launch key.
type LaunchDisposition string

const (
	// LaunchAbsent: no worker process exists for the key and none can.
	LaunchAbsent LaunchDisposition = "absent"
	// LaunchRunning: the receipted worker is still running.
	LaunchRunning LaunchDisposition = "running"
	// LaunchExited: the receipted worker ran and has exited.
	LaunchExited LaunchDisposition = "exited"
	// LaunchAmbiguous: what exists cannot be proven; the slot stays held.
	LaunchAmbiguous LaunchDisposition = "ambiguous"
)

// LaunchHeldError refuses a launch and names the operator's way out.
type LaunchHeldError struct {
	Key         string
	Disposition LaunchDisposition
	Reason      string
	Recovery    string
	// ProcessMayExist is true when a worker might be running for the key.
	ProcessMayExist bool
}

func (e *LaunchHeldError) Error() string {
	return fmt.Sprintf("launch held (%s): %s; %s", e.Disposition, e.Reason, e.Recovery)
}

func (e *LaunchHeldError) Is(target error) bool {
	return target == ErrLaunchUnsupported && e.Disposition == "unsupported"
}

// LaunchSupervisor serializes launches per key and keeps worker work behind
// a durable receipt, so a restarted owner never starts the same work twice.
type LaunchSupervisor struct {
	Journal   LaunchJournal
	Processes ProcessAdapter

	mu   sync.Mutex
	keys map[string]*sync.Mutex
}

func (s *LaunchSupervisor) lock(key string) func() {
	s.mu.Lock()
	if s.keys == nil {
		s.keys = map[string]*sync.Mutex{}
	}
	m := s.keys[key]
	if m == nil {
		m = &sync.Mutex{}
		s.keys[key] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

func unsupported(key, reason string) error {
	return &LaunchHeldError{Key: key, Disposition: "unsupported", Reason: reason,
		Recovery: "turn rolling launch supervision off or use a native worker on a supported platform"}
}

// Launch starts req under key. An existing record for key is resolved first:
// only proven absence, plus admit, lets the same key launch again.
func (s *LaunchSupervisor) Launch(ctx context.Context, key string, d Driver, req Request, admit func(context.Context) error) (*Handle, error) {
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("%w: launch key missing", ErrInvalidRequest)
	}
	unlock := s.lock(key)
	defer unlock()
	held, ok := d.(SupervisedLauncher)
	if !ok {
		return nil, unsupported(key, "worker driver has no startup barrier")
	}
	if s.Journal == nil || s.Processes == nil {
		return nil, unsupported(key, "launch receipt store or process adapter missing")
	}
	if inside(req.Worktree, s.Journal.Root()) {
		return nil, unsupported(key, "launch receipts must live outside the task worktree")
	}
	boot, err := s.Processes.BootID()
	if err != nil {
		return nil, unsupported(key, err.Error())
	}
	disp, rec, err := s.inspect(key)
	if err != nil {
		return nil, err
	}
	switch disp {
	case LaunchRunning:
		return nil, &LaunchHeldError{Key: key, Disposition: disp, Reason: "the receipted worker is already running", Recovery: "reconnect to it by its receipt", ProcessMayExist: true}
	case LaunchExited:
		return nil, &LaunchHeldError{Key: key, Disposition: disp, Reason: "the receipted worker already ran", Recovery: "collect its worktree instead of launching again"}
	case LaunchAmbiguous:
		return nil, &LaunchHeldError{Key: key, Disposition: disp, Reason: rec.Reason, Recovery: "inspect the worker process and its worktree, then resolve the held launch", ProcessMayExist: true}
	}
	if admit != nil {
		if err := admit(ctx); err != nil {
			return nil, err
		}
	}
	if err := s.Journal.Store(LaunchReceipt{Key: key, Stage: LaunchIntent, BootID: boot, Provider: req.Task.Worker}); err != nil {
		return nil, err
	}
	start, err := held.LaunchHeld(ctx, req, key)
	if errors.Is(err, ErrLaunchUnsupported) {
		// Refused before any process started: nothing to rediscover.
		return nil, errors.Join(unsupported(key, err.Error()), s.Journal.Remove(key))
	}
	if err != nil {
		// The intent stays: a later launch proves absence before reusing it.
		return nil, err
	}
	abort := func(cause error) error {
		if stopErr := start.Abort(); stopErr != nil {
			return errors.Join(cause, s.quarantine(key, "held worker exit unproven: "+stopErr.Error()), stopErr)
		}
		return cause
	}
	if !start.Process.verifiable() || start.Process.BootID != boot {
		return nil, abort(unsupported(key, "launched process identity is unverifiable"))
	}
	session := start.SessionID
	if session == "" {
		session = key
	}
	receipt := LaunchReceipt{Key: key, Stage: LaunchReceipted, BootID: boot, Provider: req.Task.Worker, Process: start.Process, SessionID: session}
	if err := s.Journal.Store(receipt); err != nil {
		return nil, abort(err)
	}
	h, err := start.Release()
	if err != nil {
		return nil, abort(err)
	}
	if h == nil {
		return nil, abort(errors.New("released worker returned no handle"))
	}
	h.receipt = &receipt
	return h, nil
}

// Inspect proves what exists for key after a restart.
func (s *LaunchSupervisor) Inspect(key string) (LaunchDisposition, error) {
	unlock := s.lock(key)
	defer unlock()
	disp, _, err := s.inspect(key)
	return disp, err
}

func (s *LaunchSupervisor) inspect(key string) (LaunchDisposition, LaunchReceipt, error) {
	rec, ok, err := s.Journal.Load(key)
	if err != nil {
		return LaunchAmbiguous, LaunchReceipt{Key: key, Reason: err.Error()}, nil
	}
	if !ok {
		return LaunchAbsent, rec, nil
	}
	if rec.Quarantined {
		return LaunchAmbiguous, rec, nil
	}
	boot, err := s.Processes.BootID()
	if err != nil {
		return LaunchAmbiguous, rec, s.quarantine(key, "boot identity unavailable: "+err.Error())
	}
	if rec.Stage == LaunchIntent {
		// No receipt: any process found is still blocked at its barrier.
		found, err := s.Processes.Find(key)
		if err != nil {
			return LaunchAmbiguous, rec, s.quarantine(key, "process discovery failed: "+err.Error())
		}
		if len(found) > 1 {
			return LaunchAmbiguous, rec, s.quarantine(key, "more than one process registered the launch key")
		}
		if len(found) == 1 {
			if err := s.Processes.Stop(found[0]); err != nil {
				return LaunchAmbiguous, rec, s.quarantine(key, "blocked worker exit unproven: "+err.Error())
			}
		}
		return LaunchAbsent, rec, nil
	}
	if !rec.Process.verifiable() {
		return LaunchAmbiguous, rec, s.quarantine(key, "receipt has no verifiable process identity")
	}
	if rec.Process.BootID != boot {
		// The OS restarted since the receipt: the worker cannot still run.
		return LaunchExited, rec, nil
	}
	alive, err := s.Processes.Alive(rec.Process)
	if err != nil {
		return LaunchAmbiguous, rec, s.quarantine(key, "process identity check failed: "+err.Error())
	}
	if alive {
		return LaunchRunning, rec, nil
	}
	return LaunchExited, rec, nil
}

func (s *LaunchSupervisor) quarantine(key, reason string) error {
	rec, ok, err := s.Journal.Load(key)
	if err != nil {
		return err
	}
	if !ok {
		rec = LaunchReceipt{Key: key, Stage: LaunchIntent}
	}
	rec.Quarantined = true
	rec.Reason = reason
	return s.Journal.Store(rec)
}

// Attached records that the owner adopted the receipted worker. A session
// that differs from the receipt quarantines the launch.
func (s *LaunchSupervisor) Attached(key, session string) error {
	unlock := s.lock(key)
	defer unlock()
	rec, ok, err := s.Journal.Load(key)
	if err != nil {
		return err
	}
	if !ok || rec.Stage == LaunchIntent || rec.Quarantined {
		return &LaunchHeldError{Key: key, Disposition: LaunchAmbiguous, Reason: "no receipt to attach", Recovery: "inspect the worker before adopting its result", ProcessMayExist: true}
	}
	if rec.SessionID != session {
		reason := "worker session differs from its launch receipt"
		return errors.Join(&LaunchHeldError{Key: key, Disposition: LaunchAmbiguous, Reason: reason, Recovery: "inspect the worker before adopting its result", ProcessMayExist: true}, s.quarantine(key, reason))
	}
	rec.Stage = LaunchAttached
	return s.Journal.Store(rec)
}

// Quarantined reports whether output for key must not be adopted.
func (s *LaunchSupervisor) Quarantined(key string) bool {
	rec, ok, err := s.Journal.Load(key)
	return err != nil || (ok && rec.Quarantined)
}

// Finish removes the record once the worker's result is adopted.
func (s *LaunchSupervisor) Finish(key string) error {
	unlock := s.lock(key)
	defer unlock()
	return s.Journal.Remove(key)
}

// LaunchReceipt returns the receipt a supervised launch recorded, if any.
func (h *Handle) LaunchReceipt() (LaunchReceipt, bool) {
	if h == nil || h.receipt == nil {
		return LaunchReceipt{}, false
	}
	return *h.receipt, true
}

func inside(root, path string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}
