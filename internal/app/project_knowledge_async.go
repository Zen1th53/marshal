package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/projectknowledge"
	"github.com/Zen1th53/marshal/internal/store"
)

// ErrKnowledgeIndexing is returned when a caller's context expires or asks not
// to block while the initial project knowledge index is still being built.
var ErrKnowledgeIndexing = errors.New("project knowledge is still indexing")

// KnowledgeIndexStatus describes the current state of background project
// knowledge indexing for a Runtime.
type KnowledgeIndexStatus struct {
	Pending  bool
	Indexing bool
	Done     bool
	Phase    string // "scan" or "index"
	Count    int
	Total    int
	Err      error
}

// ProgressLine returns a concise operator-facing progress string while
// indexing is active, or "" when indexing is not running.
func (s KnowledgeIndexStatus) ProgressLine() string {
	if !s.Indexing && !s.Pending {
		return ""
	}
	if s.Total > 0 {
		if s.Phase == "scan" {
			return fmt.Sprintf("Indexing this project: scanning %s of %s files", formatCommaInt(s.Count), formatCommaInt(s.Total))
		}
		return fmt.Sprintf("Indexing this project: %s of %s files", formatCommaInt(s.Count), formatCommaInt(s.Total))
	}
	return "Indexing this project…"
}

func formatCommaInt(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	rem := len(s) % 3
	if rem == 0 {
		rem = 3
	}
	b.WriteString(s[:rem])
	for i := rem; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

type knowledgeIndexState struct {
	mu           sync.Mutex
	pending      bool
	started      bool
	done         bool
	doneCh       chan struct{}
	cancel       context.CancelFunc
	previousRoot string
	phase        string
	count        int
	total        int
	err          error
	listeners    []func(KnowledgeIndexStatus)
	ensureFn     func(context.Context, project.Layout, ...string) error
}

func newKnowledgeIndexState(previousRoot string) *knowledgeIndexState {
	return &knowledgeIndexState{
		pending:      true,
		previousRoot: previousRoot,
		doneCh:       make(chan struct{}),
		ensureFn:     ensureProjectKnowledge,
	}
}

func (k *knowledgeIndexState) statusLocked() KnowledgeIndexStatus {
	return KnowledgeIndexStatus{
		Pending:  k.pending,
		Indexing: k.started && !k.done,
		Done:     k.done,
		Phase:    k.phase,
		Count:    k.count,
		Total:    k.total,
		Err:      k.err,
	}
}

// KnowledgeIndexStatus returns the current background indexing state. For a
// runtime whose index was ensured synchronously at open time, Done is true.
func (r *Runtime) KnowledgeIndexStatus() KnowledgeIndexStatus {
	if r == nil || r.knowledge == nil {
		return KnowledgeIndexStatus{Done: true}
	}
	r.knowledge.mu.Lock()
	defer r.knowledge.mu.Unlock()
	return r.knowledge.statusLocked()
}

// StartKnowledgeIndex starts background project knowledge indexing for a
// runtime opened with Options{DeferKnowledgeIndex: true}. Calling it multiple
// times attaches additional listeners without starting duplicate work.
func (r *Runtime) StartKnowledgeIndex(ctx context.Context, onUpdate func(KnowledgeIndexStatus)) {
	if r == nil || r.knowledge == nil {
		if onUpdate != nil {
			onUpdate(KnowledgeIndexStatus{Done: true})
		}
		return
	}
	k := r.knowledge
	k.mu.Lock()
	if k.done {
		st := k.statusLocked()
		k.mu.Unlock()
		if onUpdate != nil {
			onUpdate(st)
		}
		return
	}
	if onUpdate != nil {
		k.listeners = append(k.listeners, onUpdate)
	}
	if k.started {
		st := k.statusLocked()
		k.mu.Unlock()
		if onUpdate != nil {
			onUpdate(st)
		}
		return
	}
	k.started = true
	k.pending = false
	if ctx == nil {
		ctx = context.Background()
	}
	bgCtx, cancel := context.WithCancel(ctx)
	k.cancel = cancel
	initial := k.statusLocked()
	initialListeners := slices.Clone(k.listeners)
	ensureFn := k.ensureFn
	previousRoot := k.previousRoot
	k.mu.Unlock()

	// Roll back any leftover hot journal on the disposable knowledge cache
	// quickly before returning so concurrent read-only probes (such as the
	// startup launch doctor) do not observe an old SIGQUIT hot journal while
	// the background scan is running.
	if idx, err := projectknowledge.Open(r.layout.Root, r.layout.RuntimeDir); err == nil {
		knowledgeDB := filepath.Join(r.layout.RuntimeDir, "knowledge", "index-v1.db")
		if _, statErr := os.Lstat(knowledgeDB); statErr == nil {
			if store.IsReadOnlyRecovery(store.ProbeHotJournal(bgCtx, knowledgeDB)) {
				_ = idx.RecoverHotJournal(bgCtx)
			}
		}
	}

	for _, fn := range initialListeners {
		fn(initial)
	}

	go func() {
		defer close(k.doneCh)
		defer cancel()
		progressCtx := projectknowledge.WithProgress(bgCtx, func(p projectknowledge.Progress) {
			k.mu.Lock()
			k.phase = p.Phase
			k.count = p.Done
			k.total = p.Total
			st := k.statusLocked()
			ls := slices.Clone(k.listeners)
			k.mu.Unlock()
			for _, fn := range ls {
				fn(st)
			}
		})
		err := ensureFn(progressCtx, r.layout, previousRoot)
		k.mu.Lock()
		k.done = true
		k.err = err
		st := k.statusLocked()
		ls := slices.Clone(k.listeners)
		k.listeners = nil
		k.mu.Unlock()
		for _, fn := range ls {
			fn(st)
		}
	}()
}

// WaitKnowledgeIndex waits for deferred background project knowledge indexing
// to complete. If indexing has not been started yet, it starts it. If ctx
// expires while indexing is still running, WaitKnowledgeIndex returns an error
// wrapping ErrKnowledgeIndexing that includes the current progress line.
func (r *Runtime) WaitKnowledgeIndex(ctx context.Context) error {
	if r == nil || r.knowledge == nil {
		return nil
	}
	k := r.knowledge
	k.mu.Lock()
	if k.done {
		err := k.err
		k.mu.Unlock()
		return err
	}
	started := k.started
	k.mu.Unlock()

	if !started {
		r.StartKnowledgeIndex(context.Background(), nil)
	}
	select {
	case <-k.doneCh:
		k.mu.Lock()
		err := k.err
		k.mu.Unlock()
		return err
	case <-ctx.Done():
		st := r.KnowledgeIndexStatus()
		if line := st.ProgressLine(); line != "" {
			return fmt.Errorf("%w (%s): %w", ErrKnowledgeIndexing, line, ctx.Err())
		}
		return fmt.Errorf("%w: %w", ErrKnowledgeIndexing, ctx.Err())
	}
}

func (r *Runtime) stopKnowledgeIndex() {
	if r == nil || r.knowledge == nil {
		return
	}
	k := r.knowledge
	k.mu.Lock()
	if k.done {
		k.mu.Unlock()
		return
	}
	if !k.started {
		k.pending = false
		k.done = true
		k.err = context.Canceled
		k.listeners = nil
		close(k.doneCh)
		k.mu.Unlock()
		return
	}
	cancel := k.cancel
	k.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	<-k.doneCh
}
