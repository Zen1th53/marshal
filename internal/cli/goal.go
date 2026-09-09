package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// The goal command turns a request into a Goal candidate and shows what
// MARSHAL understood, what it assessed and what it would ask about.
//
// It stops short of persisting or approving. Forming a Goal is the point where
// a user should see MARSHAL's reading of their request before anything acts on
// it, and a command that formed and approved in one step would remove exactly
// that opportunity.
func (c *command) goal(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: describe what you want, or use: goal explain <request>", model.ErrInvalid)
	}

	request := strings.Join(args, " ")
	if args[0] == "explain" {
		request = strings.Join(args[1:], " ")
		if strings.TrimSpace(request) == "" {
			return fmt.Errorf("%w: goal explain needs a request to explain", model.ErrInvalid)
		}
	}

	root, err := c.projectRoot(ctx)
	if err != nil {
		return err
	}
	binding, found := projectid.LoadBinding(filepath.Join(root, projectid.StateDirName))
	if !found {
		// A Goal must belong to a project. Refusing here rather than inventing
		// an identity keeps the binding meaningful.
		return fmt.Errorf("%w: this project has no identity yet; set it up first", model.ErrInvalid)
	}

	qualification := projectid.QualifyGit(ctx, nil, root)
	scope, scopeErr := projectid.ResolveScope(root, "", qualification)

	intake, err := goalintake.Form(goalintake.FormationRequest{
		Request:   request,
		ProjectID: binding.ID,
		SessionID: "default-session",
		Version:   constitution.Current,
		Context: goalintake.RequestContext{
			Recoverable:   qualification.State.Recoverable(),
			DirtyWorktree: qualification.Dirty,
			ScopeKnown:    scopeErr == nil && scope.Root != "",
		},
	})
	if err != nil {
		return err
	}

	// ULTRA authorization is asked for here, at the one point where a Goal's
	// confirmation is decided. The Cloud is the authority; with it unconfigured
	// or unreachable this returns a nil gate, which answers Standard, so the
	// offline path needs no special case and cannot accidentally grant.
	authorization := cloud.Authorize(ctx, cloud.LoadConfig(),
		filepath.Join(root, projectid.StateDirName), constitution.Current.String())
	// Lease renewal, presence and telemetry run in the background for the life
	// of the command, and Stop flushes what is queued before returning.
	authorization.Start(ctx)
	defer authorization.Stop()

	decision := goalintake.Confirm(intake, authorization.Mode(), authorization.Policy())

	if c.json {
		return c.print(map[string]any{
			"original_request": intake.OriginalRequest,
			"interpretation":   intake.Interpretation,
			"project_id":       intake.ProjectID,
			"assessment":       intake.Assessment.Dimensions(),
			"elevated":         intake.Assessment.Elevated(),
			"unestablished":    intake.Assessment.Unestablished(),
			"constraints":      intake.Constraints,
			"ambiguities":      intake.Ambiguities,
			"confirmation":     intake.Confirmation,
			"requires_user":    decision.RequiresUser,
			"hard_approval":    decision.HardApprovalRequired,
			"reasons":          decision.Reasons,
		}, "")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "You asked:\n  %s\n\n", intake.OriginalRequest)
	fmt.Fprintf(&b, "MARSHAL understands this as:\n  %s\n", intake.Interpretation)

	if len(intake.Constraints) > 0 {
		b.WriteString("\nLimits you set:\n")
		for _, constraint := range intake.Constraints {
			fmt.Fprintf(&b, "  - %s\n", constraint.Text)
		}
	}

	// Only the dimensions that say something are shown. Listing ten values
	// where eight are NONE buries the two that matter.
	if elevated := intake.Assessment.Elevated(); len(elevated) > 0 {
		b.WriteString("\nWorth noting:\n")
		for _, dimension := range elevated {
			fmt.Fprintf(&b, "  - %s: %s\n", dimension, intake.Assessment.Dimensions()[dimension])
		}
	}
	if unknown := intake.Assessment.Unestablished(); len(unknown) > 0 {
		b.WriteString("\nCould not be established:\n")
		for _, dimension := range unknown {
			fmt.Fprintf(&b, "  - %s\n", dimension)
		}
	}
	// Complexity is reported separately from the safety notes, because it is
	// information about effort and reporting it alongside danger is what
	// conflates the two.
	if intake.Assessment.Complexity.AtLeast(goalintake.LevelMed) {
		fmt.Fprintf(&b, "\nEffort: %s (this is about size, not risk)\n", intake.Assessment.Complexity)
	}

	if len(intake.Ambiguities) > 0 {
		b.WriteString("\nMARSHAL needs to know:\n")
		for _, ambiguity := range intake.Ambiguities {
			fmt.Fprintf(&b, "  - %s\n    %s\n", ambiguity.Question, ambiguity.Impact)
		}
	}

	b.WriteString("\n")
	switch {
	case decision.HardApprovalRequired:
		b.WriteString("This needs your approval before it can run:\n")
		for _, reason := range decision.Reasons {
			fmt.Fprintf(&b, "  - %s\n", reason)
		}
	case decision.RequiresUser:
		b.WriteString("Confirm before this runs:\n")
		for _, reason := range decision.Reasons {
			fmt.Fprintf(&b, "  - %s\n", reason)
		}
	default:
		b.WriteString("This looks routine and can proceed once you accept it.\n")
	}
	fmt.Fprintf(&b, "\nStatus: %s\n", intake.Confirmation)

	fmt.Fprint(c.stdout, b.String())
	return nil
}

// projectRoot resolves the repository root for the current directory.
func (c *command) projectRoot(ctx context.Context) (string, error) {
	output, err := projectid.NewGitCollector().Run(ctx, c.root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%w: this directory is not part of a Git repository", model.ErrInvalid)
	}
	return strings.TrimSpace(string(output)), nil
}
