package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/startup"

	"golang.org/x/term"
)

// Setup assesses, and then offers to carry out the steps it just said were
// missing. Offering is the whole of it: each step is described, asked for by
// itself, and carried out only on a yes. Nothing here runs without an answer,
// so a user who wanted a report still gets one and nothing else.
//
// This is not startup's automatic repair, and it deliberately does not widen
// it. That layer fixes one condition without being asked each time, and
// creating a repository is not on its list because it is the user's decision.
// Asking the user and acting on their answer is that decision being made.

// setupFix is one offered step.
type setupFix struct {
	// checkID names the check this step is meant to resolve.
	checkID string
	// reason is the condition being resolved.
	reason startup.ReasonCode
	// question is what the user is asked, without the prompt suffix.
	question string
	// apply performs the step.
	apply func(ctx context.Context, root string) error
}

// setupFixes lists the steps on offer for an assessment, in the order they
// have to happen: a project cannot be set up before there is a repository to
// set it up in.
//
// Only conditions with one obvious action are listed. Installing Git, writing
// a capability policy or granting an entitlement stay the user's to do, so
// they are explained and never offered as a step MARSHAL would take.
func setupFixes(assessment startup.Assessment) []setupFix {
	// Dependency order, not the order the checks happen to be reported in: a
	// repository has to exist before it can hold a commit, and a project needs
	// that commit as its baseline.
	order := []startup.ReasonCode{
		startup.ReasonNotAGitRepository,
		startup.ReasonRepositoryEmpty,
		startup.ReasonProjectNotInit,
	}
	blocking := make(map[startup.ReasonCode]startup.Check, len(order))
	for _, check := range assessment.Blocking() {
		blocking[check.Reason] = check
	}

	var fixes []setupFix
	for _, reason := range order {
		check, found := blocking[reason]
		if !found {
			continue
		}
		switch check.Reason {
		case startup.ReasonNotAGitRepository:
			fixes = append(fixes, setupFix{
				checkID: check.ID, reason: check.Reason,
				question: "Initialize a Git repository here",
				apply: func(ctx context.Context, root string) error {
					return runGit(ctx, root, "init")
				},
			})
		case startup.ReasonRepositoryEmpty:
			// An empty commit, not the working tree: adding whatever happens
			// to be lying in the directory is the user's decision, and a
			// baseline is all the project needs to exist.
			fixes = append(fixes, setupFix{
				checkID: check.ID, reason: check.Reason,
				question: "Make an empty first commit as a baseline",
				apply: func(ctx context.Context, root string) error {
					err := runGit(ctx, root, "commit", "--allow-empty", "-m", "Initial commit")
					if err != nil && strings.Contains(err.Error(), "Author identity unknown") {
						// Git's own answer to this runs to a dozen lines. Who
						// the user is, is theirs to say, so the instruction is
						// all that is useful here.
						return fmt.Errorf("Git does not know who you are yet. Set an identity and run setup again:\n" +
							"    git config --global user.name \"Your Name\"\n" +
							"    git config --global user.email \"you@example.com\"")
					}
					return err
				},
			})
		case startup.ReasonProjectNotInit:
			fixes = append(fixes, setupFix{
				checkID: check.ID, reason: check.Reason,
				question: "Set up MARSHAL for this project",
				apply: func(ctx context.Context, root string) error {
					_, err := app.Bootstrap(ctx, root)
					if err != nil && strings.Contains(err.Error(), "ambiguous argument 'HEAD'") {
						// The repository has no commit to anchor the project to.
						// Git's wording is about revision parsing and says
						// nothing about what the user should do.
						return fmt.Errorf("this repository has no commits yet, so there is no baseline to set the project up against")
					}
					return err
				},
			})
		}
	}
	return fixes
}

func runGit(ctx context.Context, root string, args ...string) error {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	// Git never inherits stdin here: a prompt the user cannot see would hang
	// setup waiting for input.
	command.Stdin = nil
	if output, err := command.CombinedOutput(); err != nil {
		if len(output) > 2048 {
			output = output[:2048]
		}
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// offerSetupFixes asks about each blocking step and carries out the ones the
// user accepts, re-assessing after each so what is reported is what the next
// check found rather than that a command exited zero.
//
// The assessment is recomputed every round because one step changes what the
// next one is: a directory that has just become a repository can hold a
// project, which was not on offer a moment earlier.
func (c command) offerSetupFixes(ctx context.Context, assessment startup.Assessment) startup.Assessment {
	confirm := c.confirmer()
	if confirm == nil {
		if len(setupFixes(assessment)) > 0 {
			fmt.Fprint(c.stdout, "\nRun setup from a terminal to be asked about these steps.\n")
		}
		return assessment
	}

	asked := make(map[startup.ReasonCode]bool)
	for {
		fixes := setupFixes(assessment)
		var fix *setupFix
		for i := range fixes {
			if !asked[fixes[i].reason] {
				fix = &fixes[i]
				break
			}
		}
		if fix == nil {
			return assessment
		}
		asked[fix.reason] = true

		answer, err := confirm(fix.question)
		if err != nil || !answer {
			fmt.Fprintf(c.stdout, "  Skipped. %s is still yours to do.\n", fix.question)
			continue
		}
		if err := fix.apply(ctx, c.root); err != nil {
			fmt.Fprintf(c.stdout, "  That step could not be completed: %v\n", err)
			continue
		}

		// The re-assessment decides what is reported. A step that ran is not
		// the same as a problem that is gone.
		assessment = startup.Assess(ctx, startup.NewSystemProber(), c.startupEnvironment())
		check, found := assessment.Check(fix.checkID)
		switch {
		case !found:
			fmt.Fprint(c.stdout, "  Done, but the result could not be confirmed.\n")
		case check.Status.Healthy():
			fmt.Fprintf(c.stdout, "  Done. %s\n", check.Summary)
		default:
			fmt.Fprintf(c.stdout, "  That step ran but the problem remains. %s\n", check.Summary)
		}
	}
}

// confirmer returns a yes/no prompt, or nil when there is no one at the
// terminal to answer it. A setup whose output is piped somewhere is a report,
// and reading from a pipe would consume input meant for something else.
func (c command) confirmer() func(question string) (bool, error) {
	file, ok := c.stdin.(*os.File)
	if !ok || file == nil || !term.IsTerminal(int(file.Fd())) {
		return nil
	}
	reader := bufio.NewReader(file)
	return func(question string) (bool, error) {
		fmt.Fprintf(c.stdout, "\n%s? [y/N] ", question)
		line, err := reader.ReadString('\n')
		if err != nil && (err != io.EOF || strings.TrimSpace(line) == "") {
			fmt.Fprintln(c.stdout)
			return false, err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		return answer == "y" || answer == "yes", nil
	}
}
