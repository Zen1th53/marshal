package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/tui"
)

// acceptSetupEnv set to 1 accepts the first-launch setup without asking, for
// launches with no terminal to answer on.
const acceptSetupEnv = "MARSHAL_ACCEPT_SETUP"

// initFiles are the project defaults init commits when HEAD lacks them.
var initFiles = []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"}

// setupPreview is what opening Marshal in an existing Git project would do
// there, shown before anything is written.
type setupPreview struct {
	Files       []string // init files not yet in HEAD: the ones it commits
	Branch      string   // "" on a detached HEAD
	Commit      string   // short HEAD commit workers start from
	Uncommitted int      // changed or untracked paths workers will not see
	HostWorkers bool     // workers run as the user, without a sandbox
	Telemetry   bool     // anonymous telemetry is on
}

// previewSetup reads, without changing anything, what setup would do in the
// repository containing root.
func previewSetup(ctx context.Context, root string) (setupPreview, error) {
	var p setupPreview
	git := func(args ...string) (string, error) {
		command, err := hostgit.Command(ctx, root, args...)
		if err != nil {
			return "", err
		}
		command.Stdin = nil
		output, err := command.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return string(output), nil
	}
	top, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return p, err
	}
	root = strings.TrimSpace(top)
	sha, err := git("rev-parse", "--short", "HEAD")
	if err != nil {
		return p, err
	}
	p.Commit = strings.TrimSpace(sha)
	for _, name := range initFiles {
		// Fails only when HEAD has no such path: HEAD itself resolved above.
		if _, err := git("rev-parse", "--verify", "--quiet", "HEAD:"+name); err != nil {
			p.Files = append(p.Files, name)
		}
	}
	if branch, err := git("symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		p.Branch = strings.TrimSpace(branch)
	}
	status, err := git("status", "--porcelain=v1", "-z")
	if err != nil {
		return p, err
	}
	p.Uncommitted = countUncommitted(status, p.Files)
	p.HostWorkers = tui.Edition() == "Marshal"
	return p, nil
}

// countUncommitted counts the paths of `git status --porcelain=v1 -z`,
// leaving out Marshal's own state and the files setup commits.
func countUncommitted(status string, committed []string) int {
	skip := map[string]bool{}
	for _, name := range committed {
		skip[name] = true
	}
	entries := strings.Split(status, "\x00")
	n := 0
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		code, path := entry[:2], entry[3:]
		if strings.ContainsAny(code, "RC") {
			i++ // the rename or copy source follows as its own entry
		}
		if strings.HasPrefix(path, statepath.DirName+"/") || strings.HasPrefix(path, statepath.OtherDirName+"/") || skip[path] {
			continue
		}
		n++
	}
	return n
}

// Render is the consent screen.
func (p setupPreview) Render() string {
	var b strings.Builder
	b.WriteString("Marshal is not set up in this project yet. Setting it up will:\n")
	branch := "branch " + p.Branch
	if p.Branch == "" {
		branch = "a detached HEAD (no branch)"
	}
	if len(p.Files) > 0 {
		fmt.Fprintf(&b, "  - commit %s on %s (commit %q)\n", joinNames(p.Files), branch, initCommitSubject)
	} else {
		fmt.Fprintf(&b, "  - make no commit (its files are already in %s)\n", branch)
	}
	b.WriteString("  - add .marshal/ to .git/info/exclude (your .gitignore is not changed)\n")
	switch p.Uncommitted {
	case 0:
		fmt.Fprintf(&b, "Workers start from your last commit (%s).\n", p.Commit)
	case 1:
		fmt.Fprintf(&b, "Workers start from your last commit (%s). 1 uncommitted file is not included: commit it first if workers should see it.\n", p.Commit)
	default:
		fmt.Fprintf(&b, "Workers start from your last commit (%s). %d uncommitted files are not included: commit them first if workers should see them.\n", p.Commit, p.Uncommitted)
	}
	if p.HostWorkers {
		b.WriteString("Workers run as you, with your file and network access and no sandbox.\n")
	}
	if p.Telemetry {
		b.WriteString("Anonymous telemetry is on (install ID, version, OS; never code or file names).\n")
	} else {
		b.WriteString("Anonymous telemetry is off.\n")
	}
	return b.String()
}

// setupConsentInteractive reports whether someone at a terminal can answer
// the consent screen; tests replace it.
var setupConsentInteractive = func(c *command) bool { return c.isInteractiveTerminal() }

// setupDeclinedLine is printed when the owner says no.
const setupDeclinedLine = "Nothing was changed. To set up Marshal here later, run `marshal` again or `marshal init`."

// consentToSetup shows what first-launch setup will do and asks before it
// does it. It returns true to go ahead; false when the owner declined (the
// caller exits cleanly). With no terminal and no MARSHAL_ACCEPT_SETUP=1 it
// returns an error saying how to continue.
func (c *command) consentToSetup(ctx context.Context) (bool, error) {
	preview, err := previewSetup(ctx, c.root)
	if err != nil {
		return false, err
	}
	preview.Telemetry = c.telemetryOn(ctx)
	if os.Getenv(acceptSetupEnv) == "1" {
		return true, nil
	}
	if !setupConsentInteractive(c) {
		return false, fmt.Errorf("%w: Marshal is not set up in this project yet, and there is no terminal to ask before it commits %s. Run `marshal init` here, or set %s=1 to accept", model.ErrApprovalRequired, joinNamesOr(preview.Files, "its files"), acceptSetupEnv)
	}
	fmt.Fprint(c.stderr, "\n"+preview.Render())
	reader := bufio.NewReader(c.stdin)
	if !askYesNo(c.stderr, reader, "Set up Marshal here? [y/N] ", false) {
		fmt.Fprintln(c.stderr, setupDeclinedLine)
		return false, nil
	}
	if preview.Telemetry {
		keep := askYesNo(c.stderr, reader, "Keep anonymous telemetry on? [Y/n] ", true)
		c.chooseTelemetryAtSetup(ctx, keep)
	}
	return true, nil
}

// askYesNo reads one answer; an empty answer is def, a closed input is no.
func askYesNo(out io.Writer, reader *bufio.Reader, question string, def bool) bool {
	fmt.Fprint(out, question)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		fmt.Fprintln(out)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "yes":
		return true
	}
	return false
}

// telemetryOn reports whether anonymous telemetry is on for this machine.
func (c *command) telemetryOn(ctx context.Context) bool {
	if !cloud.LoadConfig().Enabled() {
		return false
	}
	dir, err := c.machineCloudDir()
	if err != nil {
		return false
	}
	st, err := cloud.NewStore(dir).TelemetryPreference(ctx)
	return err == nil && !st.TelemetryDisabled
}

// chooseTelemetryAtSetup saves the consent screen's telemetry answer, as the
// Board's Usage counts card does, so the Board does not ask again.
func (c *command) chooseTelemetryAtSetup(ctx context.Context, keep bool) {
	dir, err := c.machineCloudDir()
	if err != nil {
		return
	}
	if keep {
		cloud.NewStore(dir).TakeTelemetryNotice(ctx)
		return
	}
	if _, _, err := cloud.SetTelemetry(ctx, dir, false, Version); err != nil {
		fmt.Fprintf(c.stderr, "Could not turn telemetry off: %v. Run `marshal telemetry disable`.\n", err)
		return
	}
	if c.stopTelemetry != nil {
		c.stopTelemetry()
	}
	fmt.Fprintln(c.stderr, "Telemetry is off. `marshal telemetry enable` turns it back on.")
}

func joinNamesOr(names []string, none string) string {
	if len(names) == 0 {
		return none
	}
	return joinNames(names)
}
