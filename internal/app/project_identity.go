package app

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// This file decides whether the project state found on disk belongs to the
// project actually present.
//
// The check it replaces compared the stored repository path against the
// current one and treated any difference as a conflict. That made a moved
// project permanently unopenable — and moving a project is ordinary, whereas
// the thing genuinely worth refusing is a *different* repository having
// arrived at a path a previous project used to occupy. Comparing paths gets
// both cases exactly backwards.

// ProjectAdmission is the decision about opening a project.
type ProjectAdmission struct {
	// Identity is the project's stable ID, when one was established.
	Identity projectid.ID
	// Verdict says how the observed repository relates to the recorded one.
	Verdict projectid.Verdict
	// Admitted reports whether the runtime may use the state found here.
	Admitted bool
	// Rebound reports that the project moved and its binding was updated.
	Rebound bool
	// Reason is user-safe.
	Reason string
}

// admitProject decides whether the state in a project directory may be used.
//
// It is deliberately permissive about location and strict about identity: a
// project that moved is admitted and rebound, while a directory holding a
// different repository's state is refused even though the path is unchanged.
func admitProject(ctx context.Context, layout project.Layout, stored model.Project) (ProjectAdmission, error) {
	resolution := projectid.Resolve(ctx, nil, layout.Root, layout.RuntimeDir)

	// A project predating identity binding has no binding to check. Its state
	// is admitted on the old path-equality rule, which is what it was written
	// under, and it acquires an identity on the way through so the next open
	// uses the stronger check. Refusing these outright would strand every
	// project created before this change.
	if !resolution.Bound {
		if stored.Repository != "" && stored.Repository != layout.Root {
			return ProjectAdmission{
				Verdict: projectid.VerdictUnverifiable,
				Reason:  "This project was set up at a different location and has no identity record to confirm it is the same one.",
			}, nil
		}
		binding, err := projectid.Adopt(ctx, nil, layout.Root, layout.RuntimeDir)
		if err != nil {
			// Failing to record an identity is not a reason to refuse a
			// project that the previous rule would have admitted. It only
			// means the stronger check is unavailable next time.
			return ProjectAdmission{
				Admitted: true,
				Verdict:  projectid.VerdictUnverifiable,
				Reason:   "This project is in use but its identity could not be recorded.",
			}, nil
		}
		return ProjectAdmission{
			Identity: binding.ID, Admitted: true, Verdict: projectid.VerdictSame,
			Reason: "This project is ready.",
		}, nil
	}

	admission := ProjectAdmission{
		Identity: resolution.ID,
		Verdict:  resolution.Comparison.Verdict,
		Admitted: resolution.AdoptExisting,
		Reason:   resolution.Reason,
	}
	if !admission.Admitted {
		return admission, nil
	}

	// A moved project is rebound so the recorded location catches up. The
	// identity is unchanged; only where it was last seen is updated.
	if resolution.NeedsRebind {
		if _, err := projectid.Adopt(ctx, nil, layout.Root, layout.RuntimeDir); err != nil {
			return admission, fmt.Errorf("record the project's new location: %w", err)
		}
		admission.Rebound = true
	}
	return admission, nil
}

// ProjectIdentity returns the runtime's canonical project identity.
//
// It falls back to the legacy constant for projects that predate identity
// binding, so existing callers keep working while new projects get a real
// identity. Memory and evidence scoping use whatever this returns, which is
// what makes the transition safe: a project's records stay internally
// consistent either way.
func (r *Runtime) ProjectIdentity() string {
	if r == nil {
		return localProjectID
	}
	if binding, found := projectid.LoadBinding(r.layout.RuntimeDir); found {
		return string(binding.ID)
	}
	return localProjectID
}
