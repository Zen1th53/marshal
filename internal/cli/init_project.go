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

// initializeProject is used only by explicit init and accepted setup offers.
// Automatic runtime bootstrap must not commit project files.
func initializeProject(ctx context.Context, root string) (project.Layout, error) {
	layout, err := app.Bootstrap(ctx, root)
	if err != nil {
		return project.Layout{}, err
	}
	if err := excludeRuntimeState(ctx, layout.Root); err != nil {
		return layout, err
	}

	// Share policy/version defaults, but leave already tracked files and unrelated
	// staged changes alone. Repeated init makes no further commit.
	var files []string
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		command, err := hostgit.Command(ctx, layout.Root, "ls-tree", "--name-only", "HEAD", "--", name)
		if err != nil {
			return layout, err
		}
		output, err := command.Output()
		if err != nil {
			return layout, fmt.Errorf("inspect init file %s: %w", name, err)
		}
		if len(output) == 0 {
			files = append(files, name)
		}
	}
	if len(files) > 0 {
		if err := runGit(ctx, layout.Root, append([]string{"add", "--"}, files...)...); err != nil {
			return layout, err
		}
		if err := runGit(ctx, layout.Root, append([]string{"commit", "--only", "-m", "Initialize MARSHAL project", "--"}, files...)...); err != nil {
			return layout, fmt.Errorf("record MARSHAL project defaults: %w", err)
		}
	}
	return layout, nil
}

// Use Git's resolved path so this also works when .git is a worktree pointer.
// The project .gitignore belongs to the user and is never changed by init.
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
	if strings.HasSuffix("\n"+string(data), "\n/.marshal/\n") {
		return nil
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, []byte("/.marshal/\n")...)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("exclude MARSHAL runtime state: %w", err)
	}
	return nil
}
