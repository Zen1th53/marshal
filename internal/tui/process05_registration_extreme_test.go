//go:build !governed

package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Exercise the real polling loop after independently answered, authenticated
// protocol fixture questions. This starts no model or worker and supplies no
// genuine native authority for release qualification.
func TestProcess05AnsweredBatchSettlesOnceWithoutRenewingTransportQuestions(t *testing.T) {
	w, original := process05QuestionFixture(t)
	cfg := bindWorkspaceMCP(t, w)
	fakeCodexDecisionPane(t, "pasted")
	for _, req := range original {
		if err := w.queueMarshalRunProposal(marshalProposal{action: "approve-task", id: strings.TrimPrefix(req.Object, "/marshal approve-task "), who: "MARSHAL runtime"}); err != nil {
			t.Fatal(err)
		}
	}
	questions := w.permissions.queue.Take(false)
	w.registerPrivateProcess05Questions(t.Context(), questions)
	for i, req := range questions {
		id := ""
		w.permissions.mu.Lock()
		for candidate, p := range w.permissions.build.pending {
			if p.request == req {
				id = candidate
			}
		}
		w.permissions.mu.Unlock()
		if id == "" {
			t.Fatal("original individual question missing")
		}
		submitMCP(t, cfg, fmt.Sprintf(`{"submission_id":"answered-poll-%d","proposal":{"action":"decide","id":%q,"allow":true,"reason":"model-free protocol fixture separate exact transport review"}}`, i, id))
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { w.runPermissionQueue(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(20 * time.Second)
	for {
		applied := 0
		for _, req := range questions {
			id := strings.TrimPrefix(req.Object, "/marshal approve-task ")
			if _, err := w.store.AppliedMCPTransportReview(t.Context(), w.projectID, id); err == nil {
				applied++
			}
		}
		if applied == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("applied individual fixture receipts=%d, want 4", applied)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A resume request is queued only after all four reviews have been applied.
	// Wait until the next poll durably registers it, so the transport assertions
	// cannot race the rest of the current batch under the race detector.
	for {
		events, err := w.store.ListEvents(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		registered := false
		for _, event := range events {
			if event.Type == "PERMISSION_REQUESTED" && event.Data["object"] == "/marshal resume" {
				registered = true
				break
			}
		}
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("next polling cycle did not register the current resume question")
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.permissions.mu.Lock()
	transportPending := 0
	for _, p := range w.permissions.build.pending {
		if privateProcess05QuestionIntent(p.request) {
			transportPending++
		}
	}
	w.permissions.mu.Unlock()
	if transportPending != 0 {
		t.Fatalf("answered polling batch recreated %d stale transport questions", transportPending)
	}
	events, err := w.store.ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	transportRequests := 0
	for _, event := range events {
		if event.Type == "PERMISSION_REQUESTED" && strings.HasPrefix(fmt.Sprint(event.Data["object"]), "/marshal approve-task ") {
			transportRequests++
		}
	}
	if transportRequests != 4 {
		t.Fatalf("transport request occurrences=%d, want original 4 without new deadlines", transportRequests)
	}
	if _, ready := w.privateProcess05ResumeSnapshot(t.Context()); !ready {
		t.Fatal("settled original questions left current continuation readiness blocked")
	}
}
