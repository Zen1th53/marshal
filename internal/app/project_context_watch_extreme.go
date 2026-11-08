//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

const requiredProjectContextWatchInterval = 250 * time.Millisecond
const requiredProjectContextWatchReadTimeout = 5 * time.Second

// requiredProjectContextWatchTransientBudget is how long a watch keeps
// retrying snapshot reads that only timed out (a slow SQLite under load)
// before it stops the worker.
const requiredProjectContextWatchTransientBudget = 30 * time.Second

// errRequiredWatchReadTransient marks a watch read that only timed out.
var errRequiredWatchReadTransient = errors.New("required project context read timed out")

// This watcher can only cancel its own derived context. It cannot authorize an
// invocation, publish a result, cancel another worker, or restore a live token.
// The caller supplies an authenticated exact owned run/receipt, starts after
// consumption and checks Finish BEFORE persisting/accepting the model result.
type requiredProjectContextWatch struct {
	runtime      *Runtime
	parent       context.Context
	receipt      projectcontext.Receipt
	admission    store.ProjectContextAdmission
	workerCancel context.CancelCauseFunc
	stop         context.CancelFunc
	done         chan struct{}
	mu           sync.Mutex
	err          error
	finish       sync.Once
}

func requiredWatchBinding(b projectcontext.Binding) projectcontext.Binding {
	b.InventoryDigest, b.ManifestDigest, b.RequiredStateDigest = "", "", ""
	return b
}

func (r *Runtime) startRequiredProjectContextWatch(ctx context.Context, receipt projectcontext.Receipt, a store.ProjectContextAdmission) (context.Context, *requiredProjectContextWatch, error) {
	if ctx == nil || r == nil || r.store == nil || a.ExpectedAuthority == nil || a.ExpectedAuthority.Digest == "" || receipt.ID == "" || receipt.Outcome != "QUALIFIED" || receipt.Stage != a.Stage || receipt.PacketDigest != a.ExpectedPacketDigest || receipt.Binding.RequiredStateDigest != a.ExpectedStateDigest || a.Binding.RequiredStateDigest != a.ExpectedStateDigest || requiredWatchBinding(receipt.Binding) != requiredWatchBinding(a.Binding) || a.Audience.Actor != a.Binding.Principal || a.Selection.TaskID != a.Binding.TaskID || a.Selection.CanonicalTaskID != a.Binding.CanonicalTaskID || requiredWatchBinding(a.ExpectedAuthority.Request.Binding) != requiredWatchBinding(a.Binding) || a.Binding.Project != r.ProjectContractBinding() {
		return ctx, nil, projectcontext.Changed("project_context_watch_extreme.go:45")
	}
	// Detach all mutable slices and expiry pointers: caller mutation must not
	// silently replace the originally watched coverage or authority baseline.
	// Polling needs digests and canonical bindings, never the provider prompt.
	a.Prefix, a.ByteCap = "", 0
	a.Audience.ScopeIDs = slices.Clone(a.Audience.ScopeIDs)
	a.Selection.Paths = slices.Clone(a.Selection.Paths)
	a.Selection.Components = slices.Clone(a.Selection.Components)
	observation := *a.ExpectedAuthority
	a.ExpectedAuthority = &observation
	if observation.ValidUntil != nil {
		copy := *observation.ValidUntil
		a.ExpectedAuthority.ValidUntil = &copy
	}
	receipt.Loaded = slices.Clone(receipt.Loaded)
	if receipt.ValidUntil != nil {
		copy := *receipt.ValidUntil
		receipt.ValidUntil = &copy
	}
	worker, cancel := context.WithCancelCause(ctx)
	watchCtx, stop := context.WithCancel(ctx)
	w := &requiredProjectContextWatch{runtime: r, parent: ctx, receipt: receipt, admission: a, workerCancel: cancel, stop: stop, done: make(chan struct{})}
	if err := w.check(watchCtx); err != nil {
		stop()
		cancel(err)
		return ctx, nil, err
	}
	go w.loop(watchCtx)
	return worker, w, nil
}

// check reads one short SQLite snapshot, never a held owner/Engine fence or a
// network/model call. No working-tree or git comparison runs during writes.
func (w *requiredProjectContextWatch) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if w.receipt.CheckedAt.IsZero() || w.receipt.CheckedAt.After(now) || w.receipt.ValidUntil != nil && !now.Before(*w.receipt.ValidUntil) {
		return projectcontext.Changed("project_context_watch_extreme.go:85")
	}
	readCtx, cancel := context.WithTimeout(ctx, requiredProjectContextWatchReadTimeout)
	defer cancel()
	current, err := w.runtime.store.ReadProjectContextWatchSnapshot(readCtx, w.admission, w.receipt.ID, now)
	if err != nil {
		if err = ctx.Err(); err != nil {
			return err
		}
		// Keep model prompts, record bodies and backend error text out of this
		// cancellation cause. All failures require new authenticated admission.
		// Only a read that ran out of its own time budget (a slow store under
		// load) is retried; every other failure, including an unavailable
		// store or a revoked grant, stops the worker at once.
		if requiredWatchReadTimedOut(readCtx, err) {
			return fmt.Errorf("%w: %w (timeout)", projectcontext.Changed("project_context_watch_extreme.go:96"), errRequiredWatchReadTransient)
		}
		return fmt.Errorf("%w: required project context unavailable or changed (%s)", projectcontext.Changed("project_context_watch_extreme.go:96"), requiredWatchReadClass(err))
	}
	// Name which part moved; the values themselves stay out of the cause.
	switch {
	case !reflect.DeepEqual(current.Receipt, w.receipt):
		return fmt.Errorf("%w: admission receipt changed", projectcontext.Changed("project_context_watch_extreme.go:101"))
	case current.Snapshot.StateDigest != w.admission.ExpectedStateDigest:
		return fmt.Errorf("%w: required memory state changed", projectcontext.Changed("project_context_watch_extreme.go:103"))
	case current.Authority.Digest != w.admission.ExpectedAuthority.Digest:
		return fmt.Errorf("%w: runtime authority changed", projectcontext.Changed("project_context_watch_extreme.go:105"))
	}
	// CurrentContract/InventoryDigest may change for a private beta-only
	// accepted revision. SnapshotBound proves the scoped semantic translation.
	return ctx.Err()
}

func (w *requiredProjectContextWatch) refuse(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	if w.err == nil {
		w.err = err
	}
	cause := w.err
	w.mu.Unlock()
	w.workerCancel(cause)
}

func (w *requiredProjectContextWatch) loop(ctx context.Context) {
	defer close(w.done)
	var failingSince time.Time
	for {
		delay := requiredProjectContextWatchInterval
		if w.receipt.ValidUntil != nil {
			until := time.Until(*w.receipt.ValidUntil)
			if until < delay {
				delay = until
			}
			if delay < 0 {
				delay = 0
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			if err := w.parent.Err(); err != nil {
				w.refuse(err)
			}
			return
		case <-timer.C:
			err := w.check(ctx)
			if errors.Is(err, errRequiredWatchReadTransient) && ctx.Err() == nil && w.parent.Err() == nil {
				// A read that only failed is retried; a failure that lasts
				// past the budget still stops the worker.
				if failingSince.IsZero() {
					failingSince = time.Now()
				}
				if time.Since(failingSince) < requiredProjectContextWatchTransientBudget {
					continue
				}
			} else if err == nil {
				failingSince = time.Time{}
			}
			if err != nil {
				if w.parent.Err() != nil {
					w.refuse(w.parent.Err())
				} else if ctx.Err() == nil {
					w.refuse(err)
				}
				return
			}
		}
	}
}

// Finish is idempotent, waits for the poller, then rechecks currentness even
// when Run completed between polling ticks. A nonnil error quarantines the
// result, including a provider which ignored cancellation and returned output.
// The caller still performs its final atomic run/authority/completion fence.
func (w *requiredProjectContextWatch) Finish() error {
	if w == nil {
		return projectcontext.Changed("project_context_watch_extreme.go:165")
	}
	w.finish.Do(func() {
		w.stop()
		<-w.done
		w.mu.Lock()
		failed := w.err != nil
		w.mu.Unlock()
		if !failed {
			w.refuse(w.check(w.parent))
		}
		w.workerCancel(context.Canceled)
	})
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

var requiredCheckLocation = regexp.MustCompile(`\(([A-Za-z0-9_]+\.go:[0-9]+)\)`)

// requiredWatchReadClass names the class of a watch read failure without its
// backend text.
func requiredWatchReadClass(err error) string {
	for _, class := range []error{projectcontext.ErrReviewPending, projectcontext.ErrMissing, projectcontext.ErrChanged, projectcontext.ErrBudget, projectcontext.ErrInvalid} {
		if errors.Is(err, class) {
			// The check location (file:line) is safe to show and says which
			// binding moved; any backend text stays out.
			if m := requiredCheckLocation.FindStringSubmatch(err.Error()); m != nil {
				return class.Error() + " (" + m[1] + ")"
			}
			return class.Error()
		}
	}
	return "read failed"
}

// requiredWatchReadTimedOut reports a read that failed because its own
// deadline passed, not because the store refused or the context moved.
func requiredWatchReadTimedOut(readCtx context.Context, err error) bool {
	return err != nil && errors.Is(readCtx.Err(), context.DeadlineExceeded)
}
