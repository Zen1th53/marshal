package driver

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Zen1th53/marshal/internal/processgroup"
)

// HoldsLaunch reports whether d's launch can be held behind a durable
// receipt on this platform and edition.
func HoldsLaunch(d Driver) bool {
	if _, ok := d.(SupervisedLauncher); !ok {
		return false
	}
	if c, ok := d.(interface{ HoldsLaunch() bool }); ok {
		return c.HoldsLaunch()
	}
	return true
}

// HoldsLaunch reports whether this governed driver can hold its worker here.
func (g Governed) HoldsLaunch() bool { return g.Held && governedHoldAvailable() }

var errHeldLaunchRefused = errors.New("held worker launch refused")

type governedHoldKey struct{}

// governedHold is the startup barrier of one supervised governed launch. The
// first provider worker process the run starts is held until the supervisor
// records it; a run that ends or declares no such process launches unheld.
type governedHold struct {
	key     string
	mu      sync.Mutex
	state   int // 0 open, 1 claimed, 2 closed
	started chan ProcessIdentity
	decide  chan bool
	unheld  chan struct{}
}

func (h *governedHold) claim() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != 0 {
		return false
	}
	h.state = 1
	return true
}

// close stops later claims; it reports false when a process already claimed.
func (h *governedHold) close() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state == 1 {
		return false
	}
	if h.state == 0 {
		h.state = 2
		close(h.unheld)
	}
	return true
}

func (h *governedHold) observe(ctx context.Context, ref processgroup.Reference) error {
	boot, err := holdBootID()
	if err != nil {
		return err
	}
	if ref.PID <= 0 || ref.Start == "" {
		return errors.New("held worker exited before its identity was recorded")
	}
	h.started <- ProcessIdentity{BootID: boot, PID: ref.PID, Start: ref.Start}
	select {
	case ok := <-h.decide:
		if !ok {
			return errHeldLaunchRefused
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// HoldGovernedWorker prepares ctx to hold the provider worker process about
// to start, when it is the first one of a supervised governed launch. It
// returns the environment entry that registers the launch key in the held
// process; ok is false when nothing is held.
func HoldGovernedWorker(ctx context.Context) (held context.Context, env string, ok bool) {
	h, _ := ctx.Value(governedHoldKey{}).(*governedHold)
	if h == nil || !h.claim() {
		return ctx, "", false
	}
	return withHeldStart(ctx, func(ref processgroup.Reference) error { return h.observe(ctx, ref) }), LaunchKeyEnv + "=" + h.key, true
}

// LaunchWithoutHold reports that this governed run starts no held worker
// process for its launch, such as a run that only waits for a turn already
// running elsewhere. Its supervised launch then proceeds unheld.
func LaunchWithoutHold(ctx context.Context) {
	if h, _ := ctx.Value(governedHoldKey{}).(*governedHold); h != nil {
		h.close()
	}
}

// LaunchHeld starts the governed run with a startup barrier on its provider
// worker process. It returns once that process is held, or, when the run
// starts none, with the running handle marked unheld: the run is never
// started twice and never blocked by a missing barrier.
func (g Governed) LaunchHeld(ctx context.Context, req Request, key string) (*HeldStart, error) {
	if !g.HoldsLaunch() {
		return nil, fmt.Errorf("%w: governed worker cannot be held here", ErrLaunchUnsupported)
	}
	hold := &governedHold{key: key, started: make(chan ProcessIdentity, 1), decide: make(chan bool, 1), unheld: make(chan struct{})}
	h, err := g.Launch(context.WithValue(ctx, governedHoldKey{}, hold), req)
	if err != nil {
		return nil, err
	}
	unheld := func() (*HeldStart, error) { return &HeldStart{Unheld: h}, nil }
	select {
	case id := <-hold.started:
		return &HeldStart{
			Process:   id,
			SessionID: key,
			Release: func() (*Handle, error) {
				hold.decide <- true
				return h, nil
			},
			// A refused barrier stops the held process; the run's end proves
			// its exit.
			Abort: func() error {
				hold.decide <- false
				h.cancel()
				<-h.done
				return nil
			},
		}, nil
	case <-hold.unheld:
		return unheld()
	case <-h.done:
		// A run that held a process cannot end before the barrier is
		// decided, so the hold is still open here.
		hold.close()
		return unheld()
	case <-ctx.Done():
		h.cancel()
		<-h.done
		return nil, ctx.Err()
	}
}
