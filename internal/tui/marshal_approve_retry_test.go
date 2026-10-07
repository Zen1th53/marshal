package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func TestMarshalApproveRetryDispatchesGovernedTaskOnce(t *testing.T) {
	for _, preapproved := range []bool{false, true} {
		t.Run(map[bool]string{false: "draft", true: "already-approved-queued"}[preapproved], func(t *testing.T) {
			// Planning only accepts workers whose CLI is installed; CI has none.
			fakeBin := t.TempDir()
			if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
			w, rt := realControlWorkspace(t, "SESSION-approve-retry")
			service := *rt.Marshal()
			m := w.marshalSession()
			approvalEntered := make(chan struct{})
			approvalRelease := make(chan struct{})
			defer close(approvalRelease)
			service.ApprovalActor = func(ctx context.Context, runID, purpose string) (string, error) {
				if !preapproved {
					close(approvalEntered)
					select {
					case <-approvalRelease:
					case <-ctx.Done():
						return "", ctx.Err()
					}
				}
				return m.approver(ctx, runID, purpose)
			}
			service.ProbeWorker = func(context.Context, string) error { return nil }
			service.GateState = func(context.Context, string, string) (constitution.RuntimeState, error) {
				return constitution.RuntimeState{SandboxAvailable: true, NetworkEnforced: true, AuthorizedActor: true, EvidencePresent: true, EvidenceFresh: true, HarnessGovernance: constitution.GovernanceVerified}, nil
			}
			entered := make(chan struct{}, 2)
			release := make(chan struct{})
			defer close(release)
			service.GovernedDrivers = map[string]driver.Driver{"codex": driver.Governed{Provider: "codex", Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
				entered <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
				}
				return nil, errors.New("fixture stops after proving dispatch")
			}}}
			draft, err := service.DraftFromProposal([]byte(`{"tasks":[{"id":"T1","title":"Review fixture","criteria":["fixture checked"],"paths":["README.md"],"depends_on":[],"worker":"codex","mode":"governed","checks":[{"command":"true","criteria":["fixture checked"]}]}]}`), "codex")
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.StartPlanningFromDraft(t.Context(), "RUN-retry", "fixture", draft, marshal.Budget{})
			if err != nil {
				t.Fatal(err)
			}
			m.service, m.runID, m.provider = &service, "RUN-retry", "codex"
			if preapproved {
				m.grant("RUN-retry", "plan")
				run, err = service.Approve(t.Context(), "RUN-retry")
				if err != nil {
					t.Fatal(err)
				}
			}
			w.setMarshalPanel(newMarshalPanel("RUN-retry", "codex", run, "review"))
			// Exercise the interactive background command lane, including its UI result.
			w.uiEvents = make(chan func(), 64)
			w.runCommand(t.Context(), "/marshal approve")
			commandDeadline := time.After(5 * time.Second)
			for w.commandBusy.Load() {
				select {
				case apply := <-w.uiEvents:
					apply()
				case <-commandDeadline:
					t.Fatal("command lane did not return")
				}
			}
			w.mu.RLock()
			out := w.state.LastOutput
			w.mu.RUnlock()
			if !preapproved {
				select {
				case <-approvalEntered:
				case <-time.After(5 * time.Second):
					t.Fatal("approval actor not called")
				}
				retry, retryErr := w.ExecuteCommand(t.Context(), "/marshal approve")
				if retryErr != nil || !strings.Contains(retry, "already in progress") {
					t.Fatalf("pending retry: %q %v", retry, retryErr)
				}
				approvalRelease <- struct{}{}
			}
			if preapproved && !strings.Contains(strings.ToLower(out), "already approved") {
				t.Fatalf("retry response: %s", out)
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatalf("queued governed task not dispatched: %+v", w.marshalPanel())
			}
			out, err = w.ExecuteCommand(t.Context(), "/marshal approve")
			if err != nil || !strings.Contains(strings.ToLower(out), "already approved") {
				t.Fatalf("in-flight retry: %q %v", out, err)
			}
			// Join detached work before inspecting durable decisions.
			w.Close()
			out, err = w.ExecuteCommand(t.Context(), "/marshal approve")
			if err != nil || !strings.Contains(strings.ToLower(out), "already approved") {
				t.Fatalf("completed retry: %q %v", out, err)
			}
			select {
			case <-entered:
				t.Fatal("duplicate dispatch")
			default:
			}
			events, err := rt.Store().MarshalDecisions(t.Context(), "RUN-retry")
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, e := range events {
				if e.Type == "marshal.plan.approved" {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("approval events=%d", count)
			}
			if p := w.marshalPanel(); strings.Contains(p.Note, "approval failed") {
				t.Fatalf("stale failure: %+v", p)
			}
			if _, err := m.approver(t.Context(), "RUN-retry", "plan"); err == nil {
				t.Fatal("retry left an unused grant")
			}
		})
	}
}
