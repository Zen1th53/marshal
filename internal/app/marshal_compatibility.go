package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
)

const marshalRuntimeProtocol = 1

// Hash the running image once. The Linux proc handle continues to name the
// loaded image even after the executable on disk has been replaced.
var marshalRuntimeIdentity = sync.OnceValue(func() string {
	path, err := os.Executable()
	if runtime.GOOS == "linux" {
		path, err = "/proc/self/exe", nil
	}
	if err != nil {
		return "unknown"
	}
	f, err := os.Open(path)
	if err != nil {
		return "unknown"
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "unknown"
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
})

func newMarshalCompatibility() *marshal.RuntimeCompatibility {
	image := marshalRuntimeIdentity()
	return &marshal.RuntimeCompatibility{ProtocolVersion: marshalRuntimeProtocol, CreatedRuntime: image, ObservedRuntime: image, Disposition: "current"}
}

// observeMarshalCompatibility records classification without manufacturing
// historical execution proof. Legacy runs still traverse current task admission,
// source validation and fresh verification. Missing proof requires a new plan.
func (s *MarshalService) observeMarshalCompatibility(ctx context.Context, runID string, run marshal.Run, rev int64) (marshal.Run, int64, error) {
	if run.State == marshal.Closed {
		return run, rev, nil
	}
	image := marshalRuntimeIdentity()
	previous := run.Compatibility
	compatibility := marshal.RuntimeCompatibility{}
	if previous != nil {
		compatibility = *previous
	}
	disposition := "resumable"
	reason := "runtime image changed; protocol is compatible; current admission and fresh verification remain required"
	if compatibility.ProtocolVersion == 0 {
		disposition = "legacy-review-required"
		reason = "unversioned historical runtime; retain artifacts and validate with current admission and fresh verification; missing historical Process05 proof requires a newly approved plan"
	} else if compatibility.ProtocolVersion != marshalRuntimeProtocol {
		disposition = "incompatible"
		reason = fmt.Sprintf("run runtime protocol %d is incompatible with supported protocol %d", compatibility.ProtocolVersion, marshalRuntimeProtocol)
	}
	if compatibility.ProtocolVersion == 0 && s.requireOwner != nil && s.requiresCanonicalGovernedApproval() && !run.Process05Bound && run.State != marshal.Drafting {
		for _, task := range run.Tasks {
			if task.Mode == marshal.Governed {
				disposition = "replan-required"
				reason = "historical governed run has no canonical Process05 binding; retained artifacts cannot substitute for execution proof"
				break
			}
		}
	}
	if disposition == "incompatible" || disposition == "replan-required" {
		compatibility.ObservedRuntime, compatibility.Disposition = image, disposition
		run.Compatibility = &compatibility
		pauseMarshal(&run, reason, "retain the current artifacts; use a runtime supporting the recorded protocol, or confirm a new Goal, create and approve a new plan and /marshal use-plan", "")
	} else {
		if previous != nil && previous.ObservedRuntime == image {
			return run, rev, nil
		}
		compatibility.ObservedRuntime, compatibility.Disposition = image, disposition
		run.Compatibility = &compatibility
	}
	kind := events.EventTypeMarshalRecommended
	if disposition == "incompatible" || disposition == "replan-required" {
		kind = events.EventTypeMarshalEscalated
	}
	event, err := s.decisionEvent(runID, "", kind, map[string]any{"role": "runtime-compatibility", "reason": reason, "disposition": disposition, "protocol_version": compatibility.ProtocolVersion, "observed_runtime": image})
	if err != nil {
		return run, rev, err
	}
	// Classification and its explanation commit together in the run payload.
	if err := s.Store.SaveMarshalState(ctx, s.ProjectID, runID, run, rev, &event, nil, 0); err != nil {
		return run, rev, err
	}
	rev++
	if disposition == "incompatible" || disposition == "replan-required" {
		return run, rev, marshalPauseError(run)
	}
	return run, rev, nil
}
