package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// A cancelled Marshal step carries who cancelled it as its context's cause:
// the owner's stop, the shutdown, or an explicitly unknown source.
func TestCancellationRecordsSourceOrUnknown(t *testing.T) {
	w := NewWorkspace(nil, "owned-project", "owned-session")
	t.Cleanup(w.Close)
	m := w.marshalSession()
	cancelled := func(stop func(cancel context.CancelFunc)) (context.Context, string) {
		t.Helper()
		ctx, holder, cancel, err := m.reserveWithHolder(context.Background(), marshal.OperationHolder{Kind: "resume", Title: "test step"})
		if err != nil {
			t.Fatal(err)
		}
		stop(cancel)
		if ctx.Err() == nil {
			t.Fatal("step not cancelled")
		}
		m.mu.Lock()
		recorded := m.stopCause
		m.mu.Unlock()
		m.finishHolder(holder, cancel, false)
		return ctx, recorded
	}
	ownerCtx, ownerCause := cancelled(func(context.CancelFunc) { w.marshalStop() })
	shutdownCtx, shutdownCause := cancelled(func(context.CancelFunc) {
		w.noteMarshalShutdown()
		w.marshalStopFor(marshalStopShutdown)
	})
	unknownCtx, unknownCause := cancelled(func(cancel context.CancelFunc) { cancel() })

	for _, c := range []struct {
		ctx      context.Context
		recorded string
		want     string
		text     string
	}{
		{ownerCtx, ownerCause, marshalStopByOwner, "owner"},
		{shutdownCtx, shutdownCause, marshalStopShutdown, "shutting down"},
		{unknownCtx, unknownCause, marshalStopUnknown, "unknown"},
	} {
		if got := marshalCancelSource(c.ctx); got != c.want {
			t.Fatalf("cancel source %q, want %q", got, c.want)
		}
		cause := context.Cause(c.ctx)
		if !errors.Is(cause, context.Canceled) || !strings.Contains(cause.Error(), c.text) {
			t.Fatalf("cause %v does not name %q or no longer reads as a cancellation", cause, c.text)
		}
		source := c.recorded
		if source == "" {
			source = marshalCancelSource(c.ctx)
		}
		note, alert := marshalExecuteOutcome(marshal.Run{State: marshal.Reviewing}, context.Canceled, true, source)
		reason, _, command, ok := parseKnownStopNote(note)
		if alert || !ok || command != "/marshal resume" || reason == "" {
			t.Fatalf("note %q -> %q %q %v alert=%v", note, reason, command, ok, alert)
		}
		if c.want == marshalStopUnknown && !strings.Contains(reason, "unknown") {
			t.Fatalf("an unrecorded cancellation must say its source is unknown: %q", reason)
		}
	}
	if ownerCause != marshalStopByOwner || shutdownCause != marshalStopShutdown || unknownCause != "" {
		t.Fatalf("recorded causes %q %q %q", ownerCause, shutdownCause, unknownCause)
	}
}

// An approved delivery proposal tells the Marshal what actually happened: a
// submission still pending its checks, the exact delivery of a closed run, or
// a named refusal when the run holds no close authority. "Applied" alone read
// as delivered work.
func TestDeliverProposalReportsPendingOrExactDelivery(t *testing.T) {
	pending := deliverProposalOutcome(marshal.Run{State: marshal.Reviewing, TargetRef: "refs/heads/main", CloseAuthorization: &marshal.CloseAuthorization{}})
	if !strings.Contains(pending, "pending verification") || !strings.Contains(pending, "nothing is delivered yet") || !strings.Contains(pending, "refs/heads/main") {
		t.Fatalf("pending outcome %q", pending)
	}
	delivered := deliverProposalOutcome(marshal.Run{State: marshal.Closed, TargetRef: "refs/heads/main", DeliveryKind: "delivered_checks_only"})
	if !strings.HasPrefix(delivered, "delivered:") || !strings.Contains(delivered, "refs/heads/main") || !strings.Contains(delivered, "delivered_checks_only") {
		t.Fatalf("delivered outcome %q", delivered)
	}
	if full := deliverProposalOutcome(marshal.Run{State: marshal.Closed, TargetRef: "refs/heads/main"}); !strings.Contains(full, "full canonical proof") {
		t.Fatalf("full delivery outcome %q", full)
	}
	refused := deliverProposalOutcome(marshal.Run{State: marshal.Reviewing, TargetRef: "refs/heads/main"})
	if !strings.HasPrefix(refused, "refused:") || !strings.Contains(refused, "close authority") {
		t.Fatalf("refused outcome %q", refused)
	}
	for _, text := range []string{pending, delivered, refused} {
		if strings.Contains(text, "applied") {
			t.Fatalf("delivery outcome still reads as applied: %q", text)
		}
	}

	// Without a readable run the notice says the outcome is unknown rather
	// than claiming delivery.
	w := NewWorkspace(nil, "owned-project", "owned-session")
	t.Cleanup(w.Close)
	if text := w.deliverOutcomeText("RUN-1"); !strings.Contains(text, "outcome unknown") {
		t.Fatalf("unreadable outcome %q", text)
	}
	refusal := proposalAuthRefusal(errDeliverNoCloseAuthority)
	if receipt, ok := marshal.RefusalOf(refusal); !ok || receipt.Code != marshal.RefusalCodeUnauthorized || receipt.Effects != marshal.EffectsNone {
		t.Fatalf("missing close authority is not a named refusal: %#v %v", receipt, ok)
	}
}
