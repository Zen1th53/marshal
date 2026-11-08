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
	isolateRequiredFixtureProviders(t)
	for _, preapproved := range []bool{false, true} {
		t.Run(map[bool]string{false: "draft", true: "already-approved-queued"}[preapproved], func(t *testing.T) {
			// Planning only accepts workers whose CLI is installed; CI has none.
			fakeBin := t.TempDir()
			if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'codex-cli 0.159.2'; fi\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
			w, rt, goal := marshalRetryFixtureWorkspace(t)
			service := *rt.Marshal()
			marshalRetryConfigureService(&service)
			// This retry fixture exercises plan approval only. Standing delivery
			// consent is a distinct question covered by the service's mode matrix.
			settings, err := service.Store.GetMarshalSettings(t.Context(), service.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			settings.Value.AcceptanceMode = marshal.AcceptMarshalThenUser
			if _, err = service.Store.SetMarshalSettings(t.Context(), service.ProjectID, settings.Value, settings.Revision); err != nil {
				t.Fatal(err)
			}
			m := w.marshalSession()
			approvalEntered := make(chan struct{})
			approvalRelease := make(chan struct{})
			defer close(approvalRelease)
			service.ApprovalActor = func(ctx context.Context, runID, purpose string) (string, error) {
				if purpose != "plan" {
					return "", errors.New("fixture approves only the plan")
				}
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
			draft, err := service.DraftFromProposal(marshalRetryProposal(), "codex")
			if err != nil {
				t.Fatal(err)
			}
			draft, err = marshalRetryCanonicalDraft(t, rt, w, draft)
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.StartPlanningFromDraft(t.Context(), "RUN-retry", goal, draft, marshal.Budget{})
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
			if requiredLegacyFixtureRefusal && !preapproved {
				requests := w.permissions.queue.Take(false)
				if len(requests) != 1 || requests[0].Object != "/marshal approve" || requests[0].ApprovalBinding == "" {
					t.Fatalf("canonical plan popup missing: %+v, output=%q", requests, out)
				}
				if err := w.applyMarshalProposal(t.Context(), requests[0]); err != nil {
					t.Fatal(err)
				}
			}
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
			if requiredLegacyFixtureRefusal {
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					snapshot, readErr := service.Snapshot(t.Context(), "RUN-retry")
					if readErr == nil && snapshot.Process05Bound && snapshot.ApprovalScopeDigest != "" {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				snapshot, readErr := service.Snapshot(t.Context(), "RUN-retry")
				if readErr != nil || !snapshot.Process05Bound || snapshot.ApprovalScopeDigest == "" {
					t.Fatalf("canonical approval did not settle: %+v %v panel=%+v", snapshot, readErr, w.marshalPanel())
				}
				out, err = w.ExecuteCommand(t.Context(), "/marshal approve")
				if err != nil || !strings.Contains(strings.ToLower(out), "already approved") {
					t.Fatalf("approved retry: %q %v", out, err)
				}
				w.Close()
				select {
				case <-entered:
					t.Fatal("unreviewed custom driver dispatched")
				default:
				}
				events, err := rt.Store().MarshalDecisions(t.Context(), "RUN-retry")
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, event := range events {
					if event.Type == "marshal.plan.approved" {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("approval events=%d", count)
				}
				if _, err := m.approver(t.Context(), "RUN-retry", "plan"); err == nil {
					t.Fatal("retry left an unused grant")
				}
				return
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
