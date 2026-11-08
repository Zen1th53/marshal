//go:build !governed

package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

type process05ReviewFixtureOptions struct {
	started                    time.Time
	ttl                        time.Duration
	taskSeconds                int64
	constraints                []model.Constraint
	doNotDo                    []string
	taskChargeMs, planChargeMs int64
}

func registerBoundReview(t *testing.T, options process05ReviewFixtureOptions) (*Workspace, permission.Request, string, *buildPendingDecision) {
	t.Helper()
	w, requests := process05QuestionFrontierFixture(t, false, false, options)
	bindWorkspaceMCP(t, w)
	ctx := context.WithValue(t.Context(), privateDecisionRegistrationKey{}, true)
	if !w.routeBuildPermission(ctx, requests[0]) {
		t.Fatal("bound request not registered")
	}
	id, p := pendingCodexDecision(t, w)
	return w, requests[0], id, p
}

func TestProcess05ReviewDeadlineUsesActualApprovedBounds(t *testing.T) {
	now := time.Now()
	w, _, id, p := registerBoundReview(t, process05ReviewFixtureOptions{started: now, ttl: 15 * time.Minute})
	remaining := time.Until(p.deadline)
	if remaining < 295*time.Second || remaining > 300*time.Second {
		t.Fatalf("bound task300/plan900 transport review still has generic deadline: %s", remaining)
	}
	if !strings.Contains(p.text, "deadline_utc") || !strings.Contains(p.text, "review_seconds") {
		t.Fatal("native packet omits exact immutable review deadline")
	}
	events, err := w.store.ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "PERMISSION_REQUESTED" && event.Data["request_id"] == id {
			found = true
			if event.Data["deadline_utc"] != p.deadline.UTC().Format(time.RFC3339Nano) || event.Data["review_seconds"] != float64(300) {
				t.Fatal("audited original deadline differs from actual native question")
			}
		}
	}
	if !found {
		t.Fatal("exact request audit missing")
	}
}

func TestProcess05ReviewDeadlineTighterCanonicalCeilings(t *testing.T) {
	for _, tc := range []struct {
		name string
		opt  process05ReviewFixtureOptions
		want time.Duration
	}{
		{"task", process05ReviewFixtureOptions{started: time.Now(), ttl: 15 * time.Minute, taskSeconds: 45}, 45 * time.Second},
		{"remaining-plan", process05ReviewFixtureOptions{started: time.Now().Add(-870 * time.Second), ttl: 15 * time.Minute}, 30 * time.Second},
		{"canonical-approval", process05ReviewFixtureOptions{started: time.Now(), ttl: 30 * time.Second}, 30 * time.Second},
		{"explicit-hard-review", process05ReviewFixtureOptions{started: time.Now(), ttl: 15 * time.Minute, constraints: []model.Constraint{{ID: "review-cap", Text: "Provider transport review must finish within 40 seconds", IsHard: true}}}, 40 * time.Second},
		{"explicit-do-not-do", process05ReviewFixtureOptions{started: time.Now(), ttl: 15 * time.Minute, doNotDo: []string{"Do not let approval review exceed its maximum 30 seconds"}}, 30 * time.Second},
		{"retained-task-charge", process05ReviewFixtureOptions{started: time.Now(), ttl: 15 * time.Minute, taskChargeMs: 280001}, 19 * time.Second},
		{"retained-plan-charge", process05ReviewFixtureOptions{started: time.Now(), ttl: 15 * time.Minute, planChargeMs: 880001}, 19 * time.Second},
		{"explicit-original120", process05ReviewFixtureOptions{started: time.Now(), ttl: 15 * time.Minute, constraints: []model.Constraint{{ID: "two-minute-policy", Text: "All transport review decisions must finish within two minutes", IsHard: true}}}, 120 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Build the fixture clock when this subtest begins. Earlier subtests
			// may take seconds under the race detector.
			if tc.name == "remaining-plan" {
				tc.opt.started = time.Now().Add(-870 * time.Second)
			} else {
				tc.opt.started = time.Now()
			}
			_, _, _, p := registerBoundReview(t, tc.opt)
			if tc.name == "remaining-plan" {
				// Fixture setup consumes the same plan clock as the review. Check
				// the immutable absolute ceiling, not the time left after setup.
				planDeadline := tc.opt.started.Add(tc.opt.ttl)
				if p.deadline.After(planDeadline) || p.deadline.Before(planDeadline.Add(-time.Second)) {
					t.Fatalf("review deadline missed plan ceiling: got=%s plan=%s", p.deadline, planDeadline)
				}
				return
			}
			remaining := time.Until(p.deadline)
			if remaining > tc.want || remaining < tc.want-5*time.Second {
				t.Fatalf("review exceeded tighter %s bound: remaining=%s want<=%s", tc.name, remaining, tc.want)
			}
		})
	}
}

func TestProcess05ReviewExhaustedOrAmbiguousBoundsCannotExposeQuestion(t *testing.T) {
	for _, tc := range []struct {
		name string
		opt  process05ReviewFixtureOptions
	}{
		{"exhausted-task", process05ReviewFixtureOptions{started: time.Now(), ttl: 15 * time.Minute, taskChargeMs: 300000}},
		{"exhausted-plan", process05ReviewFixtureOptions{started: time.Now().Add(-901 * time.Second), ttl: 15 * time.Minute}},
		{"expired-canonical", process05ReviewFixtureOptions{started: time.Now(), ttl: time.Millisecond}},
		{"future-parent-clock", process05ReviewFixtureOptions{started: time.Now().Add(time.Minute), ttl: 15 * time.Minute}},
		{"unknown-explicit-policy", process05ReviewFixtureOptions{started: time.Now(), ttl: 15 * time.Minute, constraints: []model.Constraint{{ID: "opaque-review-policy", Text: "All transport review must obey the deadline in the signed operator policy", IsHard: true}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, reqs := process05QuestionFrontierFixture(t, false, false, tc.opt)
			bindWorkspaceMCP(t, w)
			ctx := context.WithValue(t.Context(), privateDecisionRegistrationKey{}, true)
			if !w.routeBuildPermission(ctx, reqs[0]) {
				t.Fatal("not routed")
			}
			w.permissions.mu.Lock()
			defer w.permissions.mu.Unlock()
			if len(w.permissions.build.pending) != 0 {
				t.Fatal("exhausted or ambiguous bounded review exposed fresh question")
			}
		})
	}
}

func TestProcess05OrdinaryDecisionDeadlineRemains120Seconds(t *testing.T) {
	w := startupCodexDecisionWorkspace(t)
	bindWorkspaceMCP(t, w)
	ctx := context.WithValue(t.Context(), privateDecisionRegistrationKey{}, true)
	req := permission.Request{Kind: "marshal-command", Object: "/marshal pause", RunID: "ordinary-unbound", Scope: "ordinary serial command"}
	w.routeBuildPermission(ctx, req)
	_, p := pendingCodexDecision(t, w)
	if remaining := time.Until(p.deadline); remaining < 119*time.Second || remaining > 120*time.Second {
		t.Fatalf("ordinary deadline changed: %s", remaining)
	}
	if strings.Contains(p.text, "review_seconds") || strings.Contains(p.text, "deadline_utc") {
		t.Fatal("ordinary packet changed")
	}
}

func TestProcess05ReviewPastGenericLimitStillRequiresProtectedApplication(t *testing.T) {
	w, requests := process05QuestionFrontierFixture(t, false, false, process05ReviewFixtureOptions{started: time.Now().Add(-121 * time.Second), ttl: 15 * time.Minute})
	cfg := bindWorkspaceMCP(t, w)
	if err := w.queueMarshalRunProposal(marshalProposal{action: "approve-task", id: strings.TrimPrefix(requests[0].Object, "/marshal approve-task "), who: "MARSHAL runtime"}); err != nil {
		t.Fatal(err)
	}
	requests = w.permissions.queue.Take(false)
	if len(requests) != 1 {
		t.Fatal("exact production proposal question missing")
	}
	ctx := context.WithValue(t.Context(), privateDecisionRegistrationKey{}, true)
	w.routeBuildPermission(ctx, requests[0])
	id, p := pendingCodexDecision(t, w)
	// Advance only the fixture question clock by 121s, without waiting two
	// minutes or creating native authority. Canonical 15m expiry is still live.
	w.permissions.mu.Lock()
	p.deadline = p.deadline.Add(-121 * time.Second)
	w.permissions.mu.Unlock()
	r := submitMCP(t, cfg, fmt.Sprintf(`{"submission_id":"after-generic-limit","proposal":{"action":"decide","id":%q,"allow":true,"reason":"model-free authenticated review timing fixture"}}`, id))
	if r.State != "answer_accepted" {
		t.Fatalf("protected answer not accepted: %+v", r)
	}
	if !w.routeBuildPermission(ctx, requests[0]) {
		t.Fatal("asynchronous protected callback not settled")
	}
	if _, err := w.store.AppliedMCPTransportReview(t.Context(), w.projectID, strings.TrimPrefix(requests[0].Object, "/marshal approve-task ")); err != nil {
		t.Fatal("protected canonical application missing", err)
	}
}

func TestProcess05ReviewPollingNeverRenewsOriginalDeadline(t *testing.T) {
	w, req, id, p := registerBoundReview(t, process05ReviewFixtureOptions{started: time.Now(), ttl: 15 * time.Minute})
	before := p.deadline
	ctx := context.WithValue(t.Context(), privateDecisionRegistrationKey{}, true)
	for i := 0; i < 3; i++ {
		w.routeBuildPermission(ctx, req)
	}
	w.permissions.mu.Lock()
	defer w.permissions.mu.Unlock()
	if w.permissions.build.pending[id] != p || !p.deadline.Equal(before) {
		t.Fatal("polling replaced question/deadline")
	}
}

func TestProcess05ReviewDuplicateProtectedAnswerRetainsOriginalDeadline(t *testing.T) {
	w, reqs := process05QuestionFrontierFixture(t, false, false, process05ReviewFixtureOptions{started: time.Now(), ttl: 15 * time.Minute})
	cfg := bindWorkspaceMCP(t, w)
	ctx := context.WithValue(t.Context(), privateDecisionRegistrationKey{}, true)
	w.routeBuildPermission(ctx, reqs[0])
	id, p := pendingCodexDecision(t, w)
	before := p.deadline
	raw := fmt.Sprintf(`{"submission_id":"same-original-review","proposal":{"action":"decide","id":%q,"allow":false,"reason":"independent model-free denial remains binding"}}`, id)
	first := submitMCP(t, cfg, raw)
	second := submitMCP(t, cfg, raw)
	if first.OccurrenceID != second.OccurrenceID || !p.deadline.Equal(before) || p.answer == nil || *p.answer {
		t.Fatal("protected duplicate changed original deadline or native denial")
	}
}

func TestProcess05ReviewPolicyAmbiguousDurationsFailClosed(t *testing.T) {
	for _, text := range []string{
		"Transport approval must complete within 1.5 minutes",
		"Review deadline is 0.5 seconds",
		"Approval deadline twenty seconds; model execution deadline 300 seconds",
		"Approval deadline twenty seconds and model execution deadline 300 seconds",
		"Transport approval review deadline -5 seconds",
		"Approval review within 1e2 seconds",
		"Approval review must finish; its deadline is twenty seconds",
		"Approval review must finish within .5 seconds",
		"Approval review must finish within 0,5 seconds",
	} {
		t.Run(text, func(t *testing.T) {
			_, specified, known := privateReviewPolicyLimit(text)
			if !specified || known {
				t.Fatal("ambiguous explicit review duration admitted")
			}
		})
	}
}

func TestProcess05ReviewPolicySupportedDurationsRemainExact(t *testing.T) {
	for _, tc := range []struct {
		text    string
		seconds int64
	}{
		{"Approval review must finish within two minutes", 120},
		{"Transport review deadline 40s", 40},
		{"All approval reviews have a maximum 30 seconds", 30},
		{"Approval review within 20 seconds; model execution within 300 seconds", 20},
		{"Model execution deadline 10 seconds; approval review within 60 seconds", 60},
	} {
		t.Run(tc.text, func(t *testing.T) {
			got, specified, known := privateReviewPolicyLimit(tc.text)
			if !specified || !known || got != time.Duration(tc.seconds)*time.Second {
				t.Fatalf("wrong limit: %s specified=%t known=%t", got, specified, known)
			}
		})
	}
}

func TestProcess05ReviewDecisionVerbsRetainWrittenCaps(t *testing.T) {
	for _, tc := range []struct {
		text    string
		seconds int64
	}{
		{"Decide within 30 seconds.", 30},
		{"Approve within 40 seconds.", 40},
		{"Finish deciding within 20 seconds", 20},
		{"Finish approving within one minute", 60},
	} {
		t.Run(tc.text, func(t *testing.T) {
			got, specified, known := privateReviewPolicyLimit(tc.text)
			if !specified || !known || got > time.Duration(tc.seconds)*time.Second {
				t.Fatal("explicit decision verb cap lost")
			}
		})
	}
}
