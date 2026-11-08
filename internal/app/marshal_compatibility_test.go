package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/verification"
)

func TestResumeClassifiesRuntimeChangeWithoutChangingOrigin(t *testing.T) {
	s := mergedRecoveryFixture(t)
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Compatibility == nil || run.Compatibility.ProtocolVersion != marshalRuntimeProtocol || run.Compatibility.CreatedRuntime == "" {
		t.Fatal("new run has no runtime/protocol identity")
	}
	previous := "sha256:" + strings.Repeat("a", 64)
	run.Compatibility.CreatedRuntime = previous
	run.Compatibility.ObservedRuntime = previous
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	run, err = s.Resume(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Compatibility.CreatedRuntime != previous || run.Compatibility.ObservedRuntime != marshalRuntimeIdentity() || run.Compatibility.Disposition != "resumable" {
		t.Fatalf("upgrade classification: %+v", run.Compatibility)
	}
	if _, err := s.VerifyMerged(t.Context(), "run", knownCharge()); err != nil {
		t.Fatal(err)
	}
	run, err = s.Snapshot(t.Context(), "run")
	if err != nil || run.Compatibility.Disposition != "reviewed-current-runtime" {
		t.Fatalf("fresh review not recorded: %+v %v", run.Compatibility, err)
	}
}

func TestLegacyResumeRetainsUnknownOriginAndMissingProofReason(t *testing.T) {
	s := mergedRecoveryFixture(t)
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Compatibility = nil // exact pre-versioning persisted payload shape
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	run, err = s.Resume(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Compatibility.ProtocolVersion != 0 || run.Compatibility.CreatedRuntime != "" || run.Compatibility.Disposition != "legacy-review-required" {
		t.Fatalf("invented legacy provenance: %+v", run.Compatibility)
	}
	cause := errors.New("canonical Process05 root for the approved Goal and plan is missing")
	s.requiredIntegrationSource = func(context.Context, string, marshal.Run, string) error { return cause }
	if _, err := s.VerifyMerged(t.Context(), "run", knownCharge()); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	run, err = s.Snapshot(t.Context(), "run")
	if err != nil || run.Pause == nil || !strings.Contains(run.Pause.Reason, cause.Error()) || !strings.Contains(strings.Join(run.Pause.Resolutions, " "), "new plan") {
		t.Fatalf("legacy recovery not durable: %+v %v", run, err)
	}
}

func TestUnsupportedRuntimeProtocolStopsAllExecutionBoundaries(t *testing.T) {
	for _, boundary := range []string{"resume", "dispatch", "verification", "close"} {
		t.Run(boundary, func(t *testing.T) {
			s := mergedRecoveryFixture(t)
			run, rev, err := s.load(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			run.Compatibility.ProtocolVersion = marshalRuntimeProtocol + 1
			if err := s.save(t.Context(), "run", run, rev); err != nil {
				t.Fatal(err)
			}
			switch boundary {
			case "resume":
				_, err = s.Resume(t.Context(), "run")
			case "dispatch":
				_, err = s.Dispatch(t.Context(), "run", "a", "brief")
			case "verification":
				var decision verification.Decision
				decision, err = s.VerifyMerged(t.Context(), "run", knownCharge())
				if decision != verification.Blocked {
					t.Fatal(decision)
				}
			case "close":
				err = s.Close(t.Context(), "run")
			}
			if err == nil || !strings.Contains(err.Error(), "incompatible") {
				t.Fatalf("unsupported protocol continued: %v", err)
			}
			run, err = s.Snapshot(t.Context(), "run")
			if err != nil || run.State != marshal.AwaitingUser || run.Pause == nil || run.Pause.ResumeState != "" || run.Compatibility.Disposition != "incompatible" {
				t.Fatalf("lost compatibility stop: %+v %v", run, err)
			}
		})
	}
}

func TestUnsupportedProtocolStopsHandInCollection(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	dispatch, err := s.Dispatch(t.Context(), "run", "a", "brief")
	if err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Compatibility.ProtocolVersion++
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(t.Context(), "run", dispatch); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("unsupported collection: %v", err)
	}
	run, err = s.Snapshot(t.Context(), "run")
	if err != nil || run.State != marshal.AwaitingUser || run.Operation != nil || run.Pause == nil {
		t.Fatalf("collection stop lost: %+v %v", run, err)
	}
}
