package startup

import (
	"context"
	"path/filepath"

	"github.com/Zen1th53/marshal/internal/projectid"
)

// This file adds the project-identity dimension to startup readiness.
//
// It closes a divergence Process 01 introduced: startup reported every project
// check READY for a directory whose state the runtime would refuse to open,
// because assessment knew nothing about identity binding. A control centre
// that says Ready for something the backend will reject is the UI-versus-
// backend divergence Process 00 forbids, so readiness has to consult the same
// evidence admission does.

// ProjectIdentityCheck observes whether the state in a project directory
// belongs to the repository actually present.
//
// It is read-only: it resolves, it never adopts. Assessment must not create a
// project identity as a side effect of looking at a directory.
func ProjectIdentityCheck(ctx context.Context, root string) Check {
	if root == "" {
		return Check{
			ID: "project.identity", Dimension: DimensionProject, Status: StatusOptional,
			Required: false, Reason: ReasonNoProject,
			Summary: "No project is open, so there is no identity to confirm.",
		}
	}

	resolution := projectid.Resolve(ctx, nil, root, filepath.Join(root, projectid.StateDirName))

	// An unbound directory is not a problem: either the project has not been
	// set up, or it predates identity binding and will acquire one when it is
	// next opened. Neither is a reason to block, and project.initialized
	// already reports the not-set-up case.
	if !resolution.Bound {
		return Check{
			ID: "project.identity", Dimension: DimensionProject, Status: StatusOptional,
			Required: false, Reason: ReasonOK,
			Summary: "This project does not record an identity yet.",
		}
	}

	switch resolution.Comparison.Verdict {
	case projectid.VerdictSame:
		return Check{
			ID: "project.identity", Dimension: DimensionProject, Status: StatusReady,
			Required: true, Reason: ReasonOK,
			Summary: "This project's identity is confirmed.",
		}
	case projectid.VerdictMoved:
		// A move is recoverable and is handled when the project opens, so it
		// is reported as a limitation rather than a block. Saying nothing
		// would be the divergence; blocking would overstate it.
		return Check{
			ID: "project.identity", Dimension: DimensionProject, Status: StatusLimited,
			Required: true, Reason: ReasonProjectMoved,
			Summary:      "This project has moved since it was last opened.",
			Impact:       "Its recorded location will be updated when it is opened.",
			Remedy:       Describe(ReasonProjectMoved).Remedy,
			Capabilities: []Capability{},
		}
	case projectid.VerdictDifferent:
		return Check{
			ID: "project.identity", Dimension: DimensionProject, Status: StatusBroken,
			Required: true, Reason: ReasonProjectIdentityBad,
			Summary:      "The project state here belongs to a different project.",
			Impact:       "Work is blocked so that another project's history is not used here.",
			Remedy:       Describe(ReasonProjectIdentityBad).Remedy,
			Capabilities: []Capability{CapProjectExecution},
		}
	default:
		// Unverifiable is not agreement. Work is blocked rather than run
		// against state whose provenance could not be established.
		return Check{
			ID: "project.identity", Dimension: DimensionProject, Status: StatusUnknown,
			Required: true, Reason: ReasonProjectIdentityBad,
			Summary:      "This project's identity could not be confirmed.",
			Impact:       "Work is blocked until it is clear which project this state belongs to.",
			Remedy:       Describe(ReasonProjectIdentityBad).Remedy,
			Capabilities: []Capability{CapProjectExecution},
		}
	}
}
