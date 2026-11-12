//go:build !governed

package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
)

// No request kind is ever denied because the Marshal did not answer in time.
// The live store once held 38 "decision timeout" memory denials.
func TestExtremeUnansweredDecisionNeverDenies(t *testing.T) {
	// Reads and network normally never reach a Marshal decision; route them
	// through it here so their unanswered path is covered too.
	marshalDecidesReads(t)
	for _, kind := range []string{"memory", "read", "network", "credential", "credential-bound", "marshal-command", "project-contract", "merge", "tool"} {
		t.Run(kind, func(t *testing.T) {
			w, rt := realControlWorkspace(t, "SESSION-extreme-unanswered-"+kind, false)
			req := permission.Request{Kind: kind, Object: "object-" + kind, Who: "Marshal", Reason: "test"}
			w.permissions.build.pending = map[string]*buildPendingDecision{"expired": {request: req, deadline: time.Now().Add(-time.Second), provider: "claude"}}
			if !w.routeBuildPermission(t.Context(), req) {
				t.Fatal("unanswered request was not handled")
			}
			events, err := rt.Store().ListEvents(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range events {
				// A network request for a run that no longer exists has
				// nothing to grant; that is not a timeout denial.
				if e.Type == "PERMISSION_DECIDED" && e.Data["object"] == req.Object && e.Data["allow"] == false && e.Data["source"] != "expired run request" {
					t.Fatalf("unanswered %s request denied: %+v", kind, e.Data)
				}
				if reason, _ := e.Data["decision_reason"].(string); reason == "decision timeout" {
					t.Fatalf("timeout denial recorded: %+v", e.Data)
				}
			}
		})
	}
}

// A high-risk kind is re-delivered with backoff and one activity line, then
// waits for the Marshal without a deadline after the bounded re-deliveries.
func TestExtremeUnansweredDecisionRedelivers(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-extreme-redeliver", false)
	req := permission.Request{ProposalID: "worker-setting", Kind: "marshal-command", Object: "/marshal settings control strict", Who: "worker-1", Reason: "Apply the exact proposed setting"}
	for attempt := 1; attempt <= extremeDecisionRedeliveries+1; attempt++ {
		w.permissions.build.pending = map[string]*buildPendingDecision{"expired": {request: req, deadline: time.Now().Add(-time.Second), provider: "claude"}}
		if !w.routeBuildPermission(t.Context(), req) {
			t.Fatal("not handled")
		}
		if len(w.permissions.build.pending) != 0 {
			t.Fatal("expired request still pending")
		}
		queued := w.permissions.queue.Take(false)
		if len(queued) != 1 || queued[0] != req {
			t.Fatalf("request not kept queued for re-delivery: %v", queued)
		}
		// Within the backoff the request stays queued and is not delivered.
		if !w.routeBuildPermission(t.Context(), req) || len(w.permissions.build.pending) != 0 || len(w.permissions.queue.Take(false)) != 1 {
			t.Fatal("re-delivered before backoff elapsed")
		}
		r := w.permissions.build.redeliver[req.Key()]
		if r == nil || r.attempts != attempt || !r.next.After(time.Now()) {
			t.Fatalf("attempt %d: %+v", attempt, r)
		}
	}
	if !w.redeliveryExhausted(req) {
		t.Fatal("bounded re-delivery not exhausted")
	}
	events, err := rt.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scheduled := 0
	for _, e := range events {
		if e.Type == "PERMISSION_DECIDED" && e.Data["object"] == req.Object {
			t.Fatalf("unanswered request decided: %+v", e.Data)
		}
		if e.Type == "PERMISSION_REDELIVERY_SCHEDULED" && e.Data["object"] == req.Object && e.Data["decided_by"] == marshalRuntimeDecider {
			scheduled++
		}
	}
	if scheduled != extremeDecisionRedeliveries+1 {
		t.Fatalf("re-delivery evidence: %d", scheduled)
	}
	w.mu.RLock()
	activity := w.state.LastOutput
	w.mu.RUnlock()
	if strings.Count(activity, extremeUnansweredActivity) != 1 || extremeUnansweredActivity != "The Marshal was busy; sending your message again." {
		t.Fatalf("unexpected activity wording or count: %s", activity)
	}
	for _, internal := range []string{"denial", "backoff", "delivery id"} {
		if strings.Contains(extremeUnansweredActivity, internal) {
			t.Fatalf("retry note contains internal term %q: %s", internal, extremeUnansweredActivity)
		}
	}
	// An answer clears the re-delivery state.
	w.finishBuildDecisionAs(t.Context(), req, "answer", false, "reviewed", "claude", "")
	if w.permissions.build.redeliver[req.Key()] != nil {
		t.Fatal("re-delivery state kept after the answer")
	}
}

func TestExtremeRedeliveryBackoffIsBounded(t *testing.T) {
	prev := time.Duration(0)
	for attempt := 1; attempt <= 20; attempt++ {
		d := extremeRedeliveryDelay(attempt)
		if d < prev || d > extremeRedeliveryBackoffMax {
			t.Fatalf("attempt %d: %v", attempt, d)
		}
		prev = d
	}
}

// An unanswered review of imported history says plainly what happened and
// shows no record ID or internal cause.
func TestExtremeUnansweredImportReviewIsPlain(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-extreme-import-plain", false)
	req := permission.Request{Kind: "memory", Object: importedHistoryPrefix + "abc123", Who: "Marshal", Reason: "test"}
	w.permissions.build.pending = map[string]*buildPendingDecision{"expired": {request: req, deadline: time.Now().Add(-time.Second), provider: "codex"}}
	if !w.routeBuildPermission(t.Context(), req) {
		t.Fatal("unanswered request was not handled")
	}
	w.mu.RLock()
	activity := w.state.LastOutput
	w.mu.RUnlock()
	if !strings.Contains(activity, "The Marshal did not review a memory imported from an earlier Codex chat") || strings.Contains(activity, "MEM-IMPORT") || strings.Contains(activity, "decision timeout") || strings.Contains(activity, "candidate (") {
		t.Fatalf("import Activity: %q", activity)
	}
}
