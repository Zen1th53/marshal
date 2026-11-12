package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/project"
)

// initCommitSubject names the one commit init makes in the user's repository.
const initCommitSubject = "Initialize MARSHAL project"

// initializeProject is used by explicit init, accepted setup offers and
// opening Marshal in a Git project it was never set up in (controlCenter):
// opening is the user asking to work here, and setup only adds what is
// missing. Runtime bootstrap elsewhere must not commit project files. It
// returns the files it committed, so the caller can say so; none on a
// repeated init.
//
// The files stay at the repository root and are committed: CAPABILITIES.yaml
// is the project's capability policy, shared and reviewed with the code, and
// the version files are read from the committed tree as source evidence.
// Keeping them under the excluded .marshal/ directory would make the policy
// per clone and unreviewed.
func initializeProject(ctx context.Context, root string) (project.Layout, []string, error) {
	layout, err := app.Bootstrap(ctx, root)
	if err != nil {
		return project.Layout{}, nil, err
	}
	if err := excludeRuntimeState(ctx, layout.Root); err != nil {
		return layout, nil, err
	}

	// Share policy/version defaults, but leave already tracked files and unrelated
	// staged changes alone. Repeated init makes no further commit.
	var files []string
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		command, err := hostgit.Command(ctx, layout.Root, "ls-tree", "--name-only", "HEAD", "--", name)
		if err != nil {
			return layout, nil, err
		}
		output, err := command.Output()
		if err != nil {
			return layout, nil, fmt.Errorf("inspect init file %s: %w", name, err)
		}
		if len(output) == 0 {
			files = append(files, name)
		}
	}
	if len(files) > 0 {
		if err := runGit(ctx, layout.Root, append([]string{"add", "--"}, files...)...); err != nil {
			return layout, nil, err
		}
		if err := runGitCommit(ctx, layout.Root, append([]string{"--only", "-m", initCommitSubject, "--"}, files...)...); err != nil {
			return layout, nil, fmt.Errorf("record MARSHAL project defaults: %w", err)
		}
	}
	return layout, files, nil
}

// initCommitNotice is the one line telling the user what init committed to
// their repository, or "" when it committed nothing.
func initCommitNotice(files []string) string {
	switch len(files) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("Committed %s to your repository (commit %q).", files[0], initCommitSubject)
	}
	list := strings.Join(files[:len(files)-1], ", ") + " and " + files[len(files)-1]
	return fmt.Sprintf("Committed %s to your repository (commit %q).", list, initCommitSubject)
}

// setUpProjectLine is the one line opening Marshal prints after it set up a
// project that was not set up yet, naming what it committed.
func setUpProjectLine(files []string) string {
	line := "Set up Marshal in this project."
	if notice := initCommitNotice(files); notice != "" {
		line += " " + notice
	}
	return line
}

// Use Git's resolved path so this also works when .git is a worktree pointer.
// The project .gitignore belongs to the user and is not changed here; only a
// repository Marshal starts lists the files it left out there (see
// prepareProjectRepository).
func excludeRuntimeState(ctx context.Context, root string) error {
	command, err := hostgit.Command(ctx, root, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("locate project exclude: %w", err)
	}
	path := strings.TrimSpace(string(output))
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("project exclude is not a regular file: %s", path)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// Keep the rule last: an earlier match can be overridden by a negation.
	rules := "/.marshal/\n/.marshal-extreme/\n"
	if strings.HasSuffix("\n"+string(data), "\n"+rules) {
		return nil
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, []byte(rules)...)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("exclude MARSHAL runtime state: %w", err)
	}
	return nil
}
