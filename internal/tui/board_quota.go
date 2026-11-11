package tui

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// rerouteActivityRE matches the one plain reroute line the Marshal records,
// capturing the from provider, the count and the to provider.
var rerouteActivityRE = regexp.MustCompile(`^(\S+) reached its limit — I moved (\d+) tasks? to (\S+)\.$`)

// rerouteActivityLine is the reroute line for n tasks, in the exact wording
// the Marshal records, so a collapsed line reads the same.
func rerouteActivityLine(from string, n int, to string) string {
	noun := "task"
	if n != 1 {
		noun = "tasks"
	}
	return fmt.Sprintf("%s reached its limit — I moved %d %s to %s.", from, n, noun, to)
}

// collapseReroute folds a reroute line into the newest Activity entry when
// that entry is a reroute of the same provider pair younger than 60 s: it
// replaces the entry in place with the summed count and returns the new and
// the previous text. It reports false, and changes nothing, otherwise.
func (w *Workspace) collapseReroute(msg string) (collapsed, prev string, ok bool) {
	m := rerouteActivityRE.FindStringSubmatch(msg)
	if m == nil {
		return "", "", false
	}
	add, _ := strconv.Atoi(m[2])
	from, to := m[1], m[3]
	n := &w.notes
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.list) == 0 {
		return "", "", false
	}
	top := &n.list[0]
	if top.Err || time.Since(top.At) >= time.Minute {
		return "", "", false
	}
	pm := rerouteActivityRE.FindStringSubmatch(top.Text)
	if pm == nil || pm[1] != from || pm[3] != to {
		return "", "", false
	}
	had, _ := strconv.Atoi(pm[2])
	prev = top.Text
	collapsed = rerouteActivityLine(from, had+add, to)
	top.Text = collapsed
	return collapsed, prev, true
}

// The quota card: the owner card for a run paused because every provider is
// at its limit. The work is saved; the owner either arms auto-resume with r
// (the run resumes as soon as a provider frees up or the reset time passes)
// or stops here with s. It is the inbox's first entry while the run waits.

// quotaResumeCheck is how often an armed quota card polls for a freed limit.
const quotaResumeCheck = 60 * time.Second

// quotaCard arms auto-resume for the all-providers-at-limit pause. While
// armed a background loop resumes the run the moment a provider frees up or
// the reset time arrives, then disarms.
type quotaCard struct {
	mu     sync.Mutex
	armed  bool
	cancel context.CancelFunc
	// Test seams; nil means the production clock, wait, resume call and
	// free-provider check.
	nowFn    func() time.Time
	waitFn   func(ctx context.Context, until time.Time) bool
	resumeFn func(ctx context.Context)
	freeFn   func() bool
}

func (qc *quotaCard) isArmed() bool {
	qc.mu.Lock()
	defer qc.mu.Unlock()
	return qc.armed
}

// providerLeftOrder is the providers a freed limit is looked for among.
var providerLeftOrder = []string{"codex", "claude", "agy", "opencode"}

// armQuotaResume arms auto-resume for the quota pause: a background loop
// resumes the run (the same path /marshal resume takes) as soon as the
// limits reader reports a candidate provider with limit left, or the reset
// time passes, then disarms. Arming twice is a no-op.
func (w *Workspace) armQuotaResume(parent context.Context, until time.Time) {
	qc := &w.quotaCard
	qc.mu.Lock()
	if qc.armed {
		qc.mu.Unlock()
		return
	}
	qc.armed = true
	ctx, cancel := context.WithCancel(parent)
	qc.cancel = cancel
	now, wait, resume, free := qc.nowFn, qc.waitFn, qc.resumeFn, qc.freeFn
	qc.mu.Unlock()

	if now == nil {
		now = time.Now
	}
	if wait == nil {
		wait = defaultQuotaWait
	}
	if resume == nil {
		resume = func(c context.Context) { _, _ = w.marshalResume(c) }
	}
	if free == nil {
		free = w.anyProviderFree
	}

	w.requestRepaint()
	go func() {
		defer w.disarmQuotaResume()
		for {
			if free() || (!until.IsZero() && !now().Before(until)) {
				resume(ctx)
				return
			}
			if !wait(ctx, until) {
				return
			}
		}
	}()
}

// disarmQuotaResume stops the auto-resume loop and leaves the card on its
// resume key. Pressing s or a finished resume both call it.
func (w *Workspace) disarmQuotaResume() {
	qc := &w.quotaCard
	qc.mu.Lock()
	changed := qc.armed
	qc.armed = false
	if qc.cancel != nil {
		qc.cancel()
		qc.cancel = nil
	}
	qc.mu.Unlock()
	if changed {
		w.requestRepaint()
	}
}

// defaultQuotaWait blocks until the next poll (at most quotaResumeCheck, and
// never past the reset time) or ctx is done; it reports false on ctx done.
func defaultQuotaWait(ctx context.Context, until time.Time) bool {
	d := quotaResumeCheck
	if !until.IsZero() {
		if u := time.Until(until); u > 0 && u < d {
			d = u
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// anyProviderFree reports whether the limits reader shows any candidate
// provider with known limit left.
func (w *Workspace) anyProviderFree() bool {
	for _, p := range providerLeftOrder {
		if left, known := w.providerLeft(p); known && left > 0 {
			return true
		}
	}
	return false
}

// providerLeft reports a provider's percent limit left from the last local
// reading and whether it is known: the most-constrained known window's
// percent, the same figure the failover ranker reads.
func (w *Workspace) providerLeft(provider string) (int, bool) {
	qs, ok := w.usageQuotas()
	if !ok {
		return 0, false
	}
	key := strings.ToLower(strings.TrimSpace(provider))
	switch key {
	case "antigravity":
		key = "agy"
	case "claude-code":
		key = "claude"
	}
	for _, q := range qs {
		if q.Provider != key {
			continue
		}
		win, known := q.Binding()
		if !known {
			return 0, false
		}
		return usageLeft(win), true
	}
	return 0, false
}

// handleQuotaKey is the quota card's keys: r arms auto-resume, s stops here.
// It runs only while the run waits on the quota pause.
func (w *Workspace) handleQuotaKey(event KeyEvent, run boardmodel.RunHead) bool {
	if run.State != marshal.AwaitingUser || run.PauseKind != "quota" {
		return false
	}
	if event.Type != KeyRune {
		return false
	}
	switch event.Rune {
	case 'r':
		w.armQuotaResume(context.Background(), run.PauseUntil)
		return true
	case 's':
		w.disarmQuotaResume()
		w.RecordActivity("Stopped here. The work is saved; press r on the card to resume later.")
		return true
	}
	return false
}

// clickQuota is a click on a quota card button: the same path its key takes.
func (w *Workspace) clickQuota(action rune) {
	w.mu.RLock()
	p := w.state.Marshal
	w.mu.RUnlock()
	if p == nil {
		return
	}
	w.handleQuotaKey(KeyEvent{Type: KeyRune, Rune: action}, p.Board.Run)
}

// quotaCardActive reports whether the Board's run waits on the quota pause.
func (v boardVisual) quotaCardActive() bool {
	return v.board.Run.State == marshal.AwaitingUser && v.board.Run.PauseKind == "quota"
}

// quotaEntry is the quota card as the inbox's first entry, two rows: the
// plain line, then the keys. Armed, the second row says when it resumes.
func (v boardVisual) quotaEntry(x, row, w int) []string {
	flag := "⚑ "
	if !v.unicode() {
		flag = "! "
	}
	first := v.fg("waiting", flag+"limits") + v.fg("dim", " · ") + v.bold(v.fg("white", "All providers are at their limit. Work is saved."))
	second, at := "    ", 4
	button := func(label string, action rune) {
		v.hit(boardHit{row: row + 1, x0: x + at, x1: x + min(w, at+VisibleLen(label)), kind: hitQuota, id: string(action), action: action})
		second += label
		at += VisibleLen(label)
	}
	if v.quotaArmed {
		resuming := "Resuming when a limit frees up"
		if until := v.board.Run.PauseUntil; !until.IsZero() {
			resuming += " (about " + until.In(v.now.Location()).Format("15:04") + ")"
		}
		second += v.fg("waiting", resuming)
		at += VisibleLen(resuming)
		second += "  "
		at += 2
		button(v.bg("rule", "[s]")+" stop here", 's')
	} else {
		button(v.bg("rule", "[r]")+" resume when available", 'r')
		second += "  "
		at += 2
		button(v.bg("rule", "[s]")+" stop here", 's')
	}
	return []string{PadCell(first, w), PadCell(second, w)}
}
