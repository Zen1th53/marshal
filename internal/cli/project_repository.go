package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// errGitIdentityMissing stops repository preparation before anything is
// created: a first commit needs the user's own name and email, and Marshal
// never makes one up for it.
var errGitIdentityMissing = errors.New("Git does not know who you are yet, so Marshal cannot start a repository for this project. Set your name and email, then run marshal again:\n" +
	"    git config --global user.name \"Your Name\"\n" +
	"    git config --global user.email \"you@example.com\"")

// prepareProjectRepository gives a dedicated folder its own Git repository
// when it has none Marshal can use: it is in no repository at all, or the
// repository it is in is the home folder's or the whole computer's, which
// Marshal never uses as a project. The folder becomes the new repository's
// root and gets a first commit of what it holds, so worktrees have a baseline.
//
// The home folder and / themselves are refused, the enclosing repository is
// never touched and no file is moved. A folder inside an ordinary repository
// is left exactly as it is. It reports whether a repository was started.
func prepareProjectRepository(ctx context.Context, dir string) (started bool, err error) {
	dir, err = filepath.Abs(dir)
	if err != nil {
		return false, err
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if err := statepath.ValidateRoot(dir); err != nil {
		return false, err
	}
	if root, found := enclosingRepository(dir); found {
		if statepath.ValidateRoot(root) == nil {
			return false, nil
		}
	} else if _, statErr := os.Lstat(filepath.Join(dir, ".git")); !os.IsNotExist(statErr) {
		// Something named .git is here but is not a usable repository. That is
		// for the person to look at, not for Marshal to replace.
		return false, nil
	}
	// Without Git the usual checks explain what to install.
	if _, err := exec.LookPath("git"); err != nil {
		return false, nil
	}
	known, err := gitIdentityConfigured(ctx, dir)
	if err != nil {
		return false, err
	}
	if !known {
		return false, errGitIdentityMissing
	}
	if err := runGit(ctx, dir, "init", "-q"); err != nil {
		return false, fmt.Errorf("start a Git repository for this project: %w", err)
	}
	defer func() {
		// A half-made repository would leave the folder neither as it was nor
		// usable. Only the .git this call created is removed; it did not exist
		// before, and the project's files are never touched.
		if err != nil {
			_ = os.RemoveAll(filepath.Join(dir, ".git"))
		}
	}()
	if err := excludeRuntimeState(ctx, dir); err != nil {
		return false, err
	}
	if err := runGit(ctx, dir, "add", "-A"); err != nil {
		return false, fmt.Errorf("record this project's files: %w", err)
	}
	if err := runGit(ctx, dir, "commit", "--allow-empty", "-q", "-m", "Initial commit"); err != nil {
		return false, fmt.Errorf("make this project's first commit: %w", err)
	}
	return true, nil
}

// enclosingRepository finds the repository dir belongs to. A .git directory
// with no HEAD in it is not a repository Git would use, so it does not count.
func enclosingRepository(dir string) (string, bool) {
	root, err := hostgit.Root(dir)
	if err != nil {
		return "", false
	}
	metadata := filepath.Join(root, ".git")
	if info, err := os.Lstat(metadata); err == nil && info.IsDir() {
		if _, err := os.Stat(filepath.Join(metadata, "HEAD")); err != nil {
			return "", false
		}
	}
	return root, true
}

// gitIdentityConfigured reports whether Git has the user's name and email,
// from the environment or from the user's own Git configuration. Only the
// user's global configuration is read; an enclosing repository's settings do
// not belong to the new project.
func gitIdentityConfigured(ctx context.Context, dir string) (bool, error) {
	for _, key := range []string{"name", "email"} {
		upper := strings.ToUpper(key)
		if os.Getenv("GIT_AUTHOR_"+upper) != "" && os.Getenv("GIT_COMMITTER_"+upper) != "" {
			continue
		}
		if key == "email" && os.Getenv("EMAIL") != "" {
			continue
		}
		command, err := hostgit.Command(ctx, dir, "config", "--global", "--get", "user."+key)
		if err != nil {
			return false, err
		}
		command.Stdin = nil
		output, err := command.Output()
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("read your Git identity: %w", err)
		}
		if strings.TrimSpace(string(output)) == "" {
			return false, nil
		}
	}
	return true, nil
}

// startedRepositoryLine is the one line printed after a repository was
// started for the project.
func startedRepositoryLine(dir string) string {
	shown := dir
	if abs, err := filepath.Abs(dir); err == nil {
		shown = abs
	}
	if resolved, err := filepath.EvalSymlinks(shown); err == nil {
		shown = resolved
	}
	if home, err := os.UserHomeDir(); err == nil {
		if resolved, err := filepath.EvalSymlinks(home); err == nil {
			home = resolved
		}
		if rel, err := filepath.Rel(home, shown); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			shown = filepath.Join("~", rel)
		}
	}
	return fmt.Sprintf("Started a new Git repository for this project in %s.", shown)
}

// prepareProjectRepository is the step init and the control centre share: the
// folder gets its own repository when it needs one, and the person is told.
func (c *command) prepareProjectRepository(ctx context.Context) (bool, error) {
	started, err := prepareProjectRepository(ctx, c.root)
	if started {
		fmt.Fprintln(c.stderr, startedRepositoryLine(c.root))
	}
	return started, err
}
