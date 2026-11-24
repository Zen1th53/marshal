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
