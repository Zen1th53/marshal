//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

// The subprocess bounds a real authority-mutex deadlock without leaving a
// wedged fixture cleanup or changing any production lock/deadline semantics.
func TestProcess05AuthenticatedResumeCompletesHeldAuthorityApplication(t *testing.T) {
	mode := os.Getenv("MARSHAL_TEST_RESUME_AUTHORITY_CHILD")
	if mode == "" {
		for _, child := range []string{"busy", "changed-child", "lease-barrier"} {
			t.Run(child, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProcess05AuthenticatedResumeCompletesHeldAuthorityApplication$")
				cmd.Env = append(os.Environ(), "MARSHAL_TEST_RESUME_AUTHORITY_CHILD="+child)
				out, err := cmd.CombinedOutput()
				if ctx.Err() != nil {
					t.Fatalf("authenticated Resume application exceeded the bounded child deadline: %v\n%s", ctx.Err(), out)
				}
				if err != nil {
					t.Fatalf("authenticated Resume fixture: %v\n%s", err, out)
				}
			})
		}
		return
	}
	w, resume, cfg := authenticatedResumeFixture(t, false, false)
	ctx := context.WithValue(t.Context(), privateDecisionRegistrationKey{}, true)
	w.routeBuildPermission(ctx, resume)
	id, _ := pendingCodexDecision(t, w)
	receipt := submitMCP(t, cfg, fmt.Sprintf(`{"submission_id":"authenticated-resume","proposal":{"action":"decide","id":%q,"allow":true,"reason":"model-free protected protocol Resume fixture"}}`, id))
	if receipt.State != "answer_accepted" {
		t.Fatalf("native answer not accepted: %+v", receipt)
	}
	if mode == "busy" || mode == "lease-barrier" {
		m := w.marshalSession()
		_, cancel, err := m.reserve()
		if err != nil {
			t.Fatal(err)
		}
		defer m.finish(cancel)
	} else if mode == "changed-child" {
		var snapshot process05ResumeSnapshot
		if err := json.Unmarshal([]byte(resume.ApprovalBinding), &snapshot); err != nil {
			t.Fatal(err)
		}
		e := w.runtime.Execution().Engine()
		child, err := e.GetRun(t.Context(), snapshot.Lanes[0].Run)
		if err != nil {
			t.Fatal(err)
		}
		child.UpdatedAt = time.Now().UTC()
		if err := e.RunStore().UpdateRun(t.Context(), child); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal("unknown fixture mode")
	}
	// Runs beginProposalMCPApply, real CommandPermission and snapshot validation
	// through the ordinary authenticated callback; no adapter/provider is run.
	if mode == "lease-barrier" {
		// Pause canonical application on the real session mutex after the guard
		// marks APPLYING. Concurrent lease revocation must wait until that exact
		// application and protected receipt finish; the guard is never weakened.
		m := w.marshalSession()
		lease := w.proposalMCP.active
		m.mu.Lock()
		done := make(chan bool, 1)
		go func() { done <- w.routeBuildPermission(ctx, resume) }()
		deadline := time.Now().Add(3 * time.Second)
		applying := false
		for time.Now().Before(deadline) {
			saved, err := w.store.MCPProposal(t.Context(), w.projectID, receipt.OccurrenceID)
			if err == nil && saved.State == "applying" {
				applying = true
				break
			}
			time.Sleep(time.Millisecond)
		}
		if !applying {
			m.mu.Unlock()
			t.Fatal("canonical application did not reach held authority barrier")
		}
		revoked := make(chan struct{})
		go func() { w.revokeProposalLease(lease); close(revoked) }()
		select {
		case <-revoked:
			m.mu.Unlock()
			t.Fatal("lease revoked while authenticated canonical application held authority")
		case <-time.After(20 * time.Millisecond):
		}
		m.mu.Unlock()
		if !<-done {
			t.Fatal("authenticated callback not routed")
		}
		<-revoked
	} else if !w.routeBuildPermission(ctx, resume) {
		t.Fatal("authenticated callback not routed")
	}
	saved, err := w.store.MCPProposal(t.Context(), w.projectID, receipt.OccurrenceID)
	if err != nil || saved.State != "rejected" {
		t.Fatalf("application did not complete bounded refusal: %+v %v", saved, err)
	}
	expected := "already running"
	if mode == "changed-child" {
		expected = "readiness changed"
	}
	if !strings.Contains(saved.Reason, expected) {
		t.Fatalf("wrong refusal gate: %q", saved.Reason)
	}
	w.proposalMCP.mu.Lock()
	valid := w.validateProposalLease(w.proposalMCP.active) == nil
	w.proposalMCP.mu.Unlock()
	if valid != (mode != "lease-barrier") {
		t.Fatal("canonical application/revocation lost exact lease state")
	}
}

func authenticatedResumeFixture(t *testing.T, missing, blocked bool) (*Workspace, permission.Request, proposalmcp.Config) {
	t.Helper()
	w, original := process05QuestionFrontierFixture(t, missing, blocked)
	cfg := bindWorkspaceMCP(t, w)
	fakeCodexDecisionPane(t, "pasted")
	for _, req := range original {
		if err := w.queueMarshalRunProposal(marshalProposal{action: "approve-task", id: strings.TrimPrefix(req.Object, "/marshal approve-task "), who: "MARSHAL runtime"}); err != nil {
			t.Fatal(err)
		}
	}
	questions := w.permissions.queue.Take(false)
	ctx := context.WithValue(t.Context(), privateDecisionRegistrationKey{}, true)
	for _, req := range questions {
		w.routeBuildPermission(ctx, req)
	}
	var resume permission.Request
	for i, req := range questions {
		id := ""
		w.permissions.mu.Lock()
		for candidate, p := range w.permissions.build.pending {
			if p.request.Key() == req.Key() {
				id = candidate
			}
		}
		w.permissions.mu.Unlock()
		if id == "" {
			t.Fatal("individual question missing")
		}
		submitMCP(t, cfg, fmt.Sprintf(`{"submission_id":"review-%d","proposal":{"action":"decide","id":%q,"allow":true,"reason":"model-free protocol fixture exact independent review"}}`, i, id))
		w.routeBuildPermission(ctx, req)
		approvalID := strings.TrimPrefix(req.Object, "/marshal approve-task ")
		if _, err := w.store.AppliedMCPTransportReview(t.Context(), w.projectID, approvalID); err != nil {
			t.Fatalf("actual protected application missing: %v", err)
		}
		batch := w.permissions.queue.Take(false)
		for _, q := range batch {
			if q.Object == "/marshal resume" {
				if i != len(questions)-1 {
					t.Fatal("partial review queued resume")
				}
				if resume.Object != "" {
					t.Fatal("duplicate automatic resume question")
				}
				resume = q
			}
		}
	}

	return w, resume, cfg
}

func TestProcess05HeldLeaseProofExpiresAndCannotCrossWorkspace(t *testing.T) {
	w, lease, cfg := liveMCPFixture(t)
	submitMCP(t, cfg, `{"submission_id":"held-scope","proposal":{"action":"setting","key":"acceptance-mode","value":"marshal"}}`)
	requests := w.permissions.queue.Take(false)
	if len(requests) != 1 {
		t.Fatal("missing protected canonical request")
	}
	finish, err := w.beginProposalMCPApply(requests[0])
	if err != nil {
		t.Fatal(err)
	}
	ctx, expire := w.privateProposalMCPApplyContext(t.Context(), requests[0])
	defer finish(false, nil)
	defer expire()
	launch, owner, provider, live := w.privateProcess05ResumeLease(ctx)
	if !live || launch != lease.id || owner != lease.owner || provider != lease.provider {
		t.Fatal("held proof changed native launch authority")
	}
	foreign := &Workspace{}
	if _, _, _, live := foreign.privateProcess05ResumeLease(ctx); live {
		t.Fatal("held proof crossed workspace authority")
	}
	lease.revoked = true
	if _, _, _, live := w.privateProcess05ResumeLease(ctx); live {
		t.Fatal("held proof admitted stale native lease")
	}
	lease.revoked = false
	expire()
	if _, _, _, live := w.privateProcess05ResumeLease(ctx); live {
		t.Fatal("completed context retained held authority")
	}
	unbound, release := w.privateProposalMCPApplyContext(t.Context(), permission.Request{})
	release()
	if unbound != t.Context() {
		t.Fatal("unbound application minted held authority")
	}
}

func TestProcess05HeldLeaseBorrowFencesCompletionAndRevocation(t *testing.T) {
	w, lease, cfg := liveMCPFixture(t)
	submitMCP(t, cfg, `{"submission_id":"borrow-fence","proposal":{"action":"setting","key":"acceptance-mode","value":"marshal"}}`)
	requests := w.permissions.queue.Take(false)
	if len(requests) != 1 {
		t.Fatal("missing protected canonical request")
	}
	finish, err := w.beginProposalMCPApply(requests[0])
	if err != nil {
		t.Fatal(err)
	}
	ctx, expire := w.privateProposalMCPApplyContext(t.Context(), requests[0])
	proof := ctx.Value(privateHeldProposalLeaseKey{}).(*privateHeldProposalLease)
	// Stop the borrower inside real lease validation, after it owns the proof.
	w.tmuxMu.Lock()
	borrowed := make(chan bool, 1)
	go func() { _, _, _, live := w.privateProcess05ResumeLease(ctx); borrowed <- live }()
	deadline := time.Now().Add(3 * time.Second)
	acquired := false
	for time.Now().Before(deadline) {
		if proof.mu.TryLock() {
			proof.mu.Unlock()
			time.Sleep(time.Millisecond)
		} else {
			acquired = true
			break
		}
	}
	if !acquired {
		w.tmuxMu.Unlock()
		expire()
		_ = finish(false, nil)
		t.Fatal("borrow did not reach lease validation barrier")
	}
	finished := make(chan error, 1)
	go func() { expire(); finished <- finish(false, nil) }()
	revoked := make(chan struct{})
	go func() { w.revokeProposalLease(lease); close(revoked) }()
	select {
	case <-finished:
		w.tmuxMu.Unlock()
		t.Fatal("application unlocked authority before retained-context borrow finished")
	case <-revoked:
		w.tmuxMu.Unlock()
		t.Fatal("revocation raced a held lease borrow")
	case <-time.After(20 * time.Millisecond):
	}
	w.tmuxMu.Unlock()
	if !<-borrowed {
		t.Fatal("live guarded borrow lost its exact lease")
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	<-revoked
	if _, _, _, live := w.privateProcess05ResumeLease(ctx); live {
		t.Fatal("retained completed context regained native authority")
	}
}
