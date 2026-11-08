//go:build !governed

package app

import "os"

// Keep synchronous execution control and its receipts within the managed
// owner's lifetime. Bare composed services have no runtime lifecycle lease.
func admitRunControlLifecycle(r *Runtime) (func(), error) {
	if r.runtimeInstanceID == "" {
		return func() {}, nil
	}
	r.ownerMu.Lock()
	if r.closed {
		r.ownerMu.Unlock()
		return nil, os.ErrClosed
	}
	if r.ownerLock == nil {
		r.ownerMu.Unlock()
		return nil, ErrRuntimeOwned
	}
	return r.ownerMu.Unlock, nil
}
