package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Zen1th53/marshal/internal/project"
)

var ErrRuntimeOwned = errors.New("another MARSHAL window owns this project’s lifecycle; use that window for lifecycle commands, or close it and reopen this window to recover and take ownership")

// IsLifecycleOwner reports whether this runtime owns the project's lifecycle.
func (r *Runtime) IsLifecycleOwner() bool {
	r.ownerMu.Lock()
	defer r.ownerMu.Unlock()
	return !r.closed && r.ownerLock != nil
}

// The kernel lock is the liveness check: it survives client disconnection but
// is released when the owner closes or crashes. Never unlink the lock file;
// replacing its inode would let two processes acquire different locks.
func acquireRuntimeOwner(stateDir string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(stateDir, "runtime-owner.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open runtime owner lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrRuntimeOwned
		}
		return nil, fmt.Errorf("lock runtime owner: %w", err)
	}
	return file, nil
}

// ProjectHeldElsewhere reports whether another process (a Marshal window)
// holds root's lifecycle lock right now. It only asks the kernel: a shared,
// non-blocking probe that conflicts with the holder's exclusive lock and is
// let go at once. It never creates the lock file.
func ProjectHeldElsewhere(root string) bool {
	layout, err := project.Discover(root)
	if err != nil {
		return false
	}
	file, err := os.OpenFile(filepath.Join(layout.RuntimeDir, "runtime-owner.lock"), os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false
}

// Ownership is required for lifecycle mutations, not shared memory or operator
// access. Non-owners remain open and can take ownership by reopening after the
// owner exits; they must never interpret a live owner's operation as interrupted.
func (r *Runtime) requireLifecycleOwner() error {
	r.ownerMu.Lock()
	defer r.ownerMu.Unlock()
	if r.closed {
		return os.ErrClosed
	}
	if r.ownerLock == nil {
		return ErrRuntimeOwned
	}
	return nil
}
