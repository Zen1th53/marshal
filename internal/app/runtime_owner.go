package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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
