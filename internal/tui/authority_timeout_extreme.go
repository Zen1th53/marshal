//go:build !governed

package tui

import (
	"context"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

// A Marshal decision that gets no answer never denies work in this edition.
// Low-risk kinds apply automatically under runtime authority; every other kind
// is re-delivered to the Marshal with exponential backoff a bounded number of
// times, and then waits for the Marshal without a deadline. Nothing reaches an
// operator popup.
const (
	extremeDecisionRedeliveries = 5
	extremeRedeliveryBackoff    = 15 * time.Second
	extremeRedeliveryBackoffMax = 5 * time.Minute
	extremeAwaitForever         = 100 * 365 * 24 * time.Hour
	extremeUnansweredActivity   = "Marshal did not answer in time; no denial in Marshal, re-delivering with backoff: "
	extremeUnansweredApplyNote  = "Marshal: the Marshal did not answer in time; this low-risk request applies automatically"
)

// extremeUnansweredAppliesKinds are request kinds whose unanswered decision
// applies automatically: they change no code, contract or run plan. Memory is
// scanned by the firewall (secrets are redacted) before it is retained.
var extremeUnansweredAppliesKinds = map[string]bool{
	"memory": true, "read": true, "network": true, "credential": true, "credential-bound": true,
}

type extremeRedelivery struct {
	attempts int
	next     time.Time
}

func extremeRedeliveryDelay(attempt int) time.Duration {
	d := extremeRedeliveryBackoff
	for i := 1; i < attempt && d < extremeRedeliveryBackoffMax; i++ {
		d *= 2
	}
	if d > extremeRedeliveryBackoffMax {
		d = extremeRedeliveryBackoffMax
	}
	return d
}

// settleUnansweredDecision handles a delivered decision request the Marshal
// did not answer (deadline passed, delivery failed, or the Marshal relaunched).
// p has already been removed from the pending set.
func (w *Workspace) settleUnansweredDecision(ctx context.Context, req permission.Request, id string, p *buildPendingDecision, cause string) {
	provider := ""
	if p != nil {
		provider = p.provider
	}
	if req.Kind == "memory" && strings.HasPrefix(req.Object, importedHistoryPrefix) {
		w.keepUnansweredImportCandidate(ctx, req, id, provider, cause)
		return
	}
	if extremeUnansweredAppliesKinds[req.Kind] {
		w.finishBuildDecisionAs(ctx, req, id, true, extremeUnansweredApplyNote+" ("+cause+")", provider, marshalRuntimeDecider)
		return
	}
	now := time.Now()
	w.permissions.mu.Lock()
	if w.permissions.build.redeliver == nil {
		w.permissions.build.redeliver = map[string]*extremeRedelivery{}
	}
	r := w.permissions.build.redeliver[req.Key()]
	if r == nil {
		r = &extremeRedelivery{}
		w.permissions.build.redeliver[req.Key()] = r
	}
	r.attempts++
	attempt := r.attempts
	r.next = now.Add(extremeRedeliveryDelay(attempt))
	w.permissions.queue.Add(req)
	w.permissions.mu.Unlock()
	if attempt == 1 {
		w.RecordActivity(extremeUnansweredActivity + req.Kind + " " + req.Object)
	}
	if w.store != nil {
		eventID, err := model.NewID("EVENT-PERMISSION-REDELIVERY-")
		if err == nil {
			_ = w.store.AppendEvent(context.WithoutCancel(ctx), nil, model.Event{ID: eventID, Type: "PERMISSION_REDELIVERY_SCHEDULED", ProjectID: w.projectID, Timestamp: now.UTC(), Data: map[string]any{
				"request_id": id, "kind": req.Kind, "object": req.Object, "cause": cause, "attempt": attempt,
				"max_redeliveries": extremeDecisionRedeliveries, "next_utc": r.next.UTC().Format(time.RFC3339Nano),
				"await_without_deadline": attempt > extremeDecisionRedeliveries, "provider": provider,
				"decided_by": marshalRuntimeDecider, "source": "marshal",
			}})
		}
	}
}

// importedHistoryPrefix marks a candidate captured from a provider's own
// session history (see importer.SessionEpisodeID).
const importedHistoryPrefix = "MEM-IMPORT-"

// keepUnansweredImportCandidate settles an unanswered review of imported
// session history without a decision: the record stays a candidate (history
// the Marshal or operator can still retain with /memory review) and is never
// promoted to durable memory merely because nobody answered in time.
func (w *Workspace) keepUnansweredImportCandidate(ctx context.Context, req permission.Request, id, provider, cause string) {
	w.permissions.mu.Lock()
	delete(w.permissions.outstanding, req.Key())
	delete(w.permissions.build.redeliver, req.Key())
	w.permissions.mu.Unlock()
	// Activity says what happened in plain words; the record ID and the
	// cause stay in the event below.
	from := "an earlier chat"
	if provider != "" {
		from = "an earlier " + providerDisplayName(provider) + " chat"
	}
	w.RecordActivity("The Marshal did not review a memory imported from " + from + ", so it is not kept as project memory yet. Review it with /memory review.")
	if w.store == nil {
		return
	}
	eventID, err := model.NewID("EVENT-PERMISSION-UNANSWERED-")
	if err != nil {
		return
	}
	_ = w.store.AppendEvent(context.WithoutCancel(ctx), nil, model.Event{ID: eventID, Type: "PERMISSION_UNANSWERED_KEPT_CANDIDATE", ProjectID: w.projectID, Timestamp: time.Now().UTC(), Data: map[string]any{
		"request_id": id, "kind": req.Kind, "object": req.Object, "cause": cause, "provider": provider,
		"lifecycle": "candidate", "source": "marshal",
	}})
}

// redeliveryWaiting keeps a request queued until its backoff elapses.
func (w *Workspace) redeliveryWaiting(req permission.Request) bool {
	w.permissions.mu.Lock()
	defer w.permissions.mu.Unlock()
	r := w.permissions.build.redeliver[req.Key()]
	if r == nil || !time.Now().Before(r.next) {
		return false
	}
	w.permissions.queue.Add(req)
	return true
}

// redeliveryExhausted reports whether the bounded re-deliveries are used up.
func (w *Workspace) redeliveryExhausted(req permission.Request) bool {
	w.permissions.mu.Lock()
	defer w.permissions.mu.Unlock()
	r := w.permissions.build.redeliver[req.Key()]
	return r != nil && r.attempts >= extremeDecisionRedeliveries
}
