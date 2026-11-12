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

// firstCommit describes the repository prepareProjectRepository started.
type firstCommit struct {
	// Started reports whether a repository was started at all.
	Started bool
	// Files is how many files the first commit holds.
	Files int
	// LeftOut lists files that look like secrets by name and were left
	// untracked, relative to the project folder.
	LeftOut []string
}

// defaultGitignore is written to a folder that has no .gitignore before its
// first commit, so secrets and dependency or build directories stay out.
const defaultGitignore = `# Written by Marshal when it started this repository. Edit freely.
.env
.env.*
*.pem
*.key
id_rsa*
.venv/
venv/
node_modules/
__pycache__/
*.pyc
dist/
build/
target/
.DS_Store
`

// secretLooking reports whether a file name looks like a secret. Such files
// are never staged by the first commit, even when nothing ignores them.
func secretLooking(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	for _, pattern := range []string{".env*", "*.pem", "*.key", "id_rsa*", "*.p12", "credentials*.json"} {
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
	}
	return false
}

// prepareProjectRepository gives a dedicated folder its own Git repository
// when it has none Marshal can use: it is in no repository at all, or the
// repository it is in is the home folder's or the whole computer's, which
// Marshal never uses as a project. The folder becomes the new repository's
// root and gets a first commit, so worktrees have a baseline.
//
// The first commit keeps secrets and dependencies out: a folder without a
// .gitignore gets a short default one (committed with it), an existing one is
// kept, and files that look like secrets by name are left untracked either way.
//
// The home folder and / themselves are refused, the enclosing repository is
// never touched and no file is moved. A folder inside an ordinary repository
// is left exactly as it is.
func prepareProjectRepository(ctx context.Context, dir string) (result firstCommit, err error) {
	dir, err = filepath.Abs(dir)
	if err != nil {
		return firstCommit{}, err
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if err := statepath.ValidateRoot(dir); err != nil {
		return firstCommit{}, err
	}
	if root, found := enclosingRepository(dir); found {
		if statepath.ValidateRoot(root) == nil {
			return firstCommit{}, nil
		}
	} else if _, statErr := os.Lstat(filepath.Join(dir, ".git")); !os.IsNotExist(statErr) {
		// Something named .git is here but is not a usable repository. That is
		// for the person to look at, not for Marshal to replace.
		return firstCommit{}, nil
	}
	// Without Git the usual checks explain what to install.
	if _, err := exec.LookPath("git"); err != nil {
		return firstCommit{}, nil
	}
	known, err := gitIdentityConfigured(ctx, dir)
	if err != nil {
		return firstCommit{}, err
	}
	if !known {
		return firstCommit{}, errGitIdentityMissing
	}
	ignorePath := filepath.Join(dir, ".gitignore")
	_, ignoreErr := os.Lstat(ignorePath)
	if ignoreErr != nil && !os.IsNotExist(ignoreErr) {
		return firstCommit{}, ignoreErr
	}
	wroteIgnore := false
	if err := runGit(ctx, dir, "init", "-q"); err != nil {
		return firstCommit{}, fmt.Errorf("start a Git repository for this project: %w", err)
	}
	defer func() {
		// A half-made repository would leave the folder neither as it was nor
		// usable. Only what this call created is removed: the .git and a
		// default .gitignore, neither of which existed before.
		if err != nil {
			_ = os.RemoveAll(filepath.Join(dir, ".git"))
			if wroteIgnore {
				_ = os.Remove(ignorePath)
			}
		}
	}()
	if err := excludeRuntimeState(ctx, dir); err != nil {
		return firstCommit{}, err
	}
	if os.IsNotExist(ignoreErr) {
		file, err := os.OpenFile(ignorePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return firstCommit{}, fmt.Errorf("write a default .gitignore: %w", err)
		}
		wroteIgnore = true
		_, writeErr := file.WriteString(defaultGitignore)
		if closeErr := file.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			return firstCommit{}, fmt.Errorf("write a default .gitignore: %w", writeErr)
		}
	}
	candidates, err := untrackedFiles(ctx, dir)
	if err != nil {
		return firstCommit{}, fmt.Errorf("list this project's files: %w", err)
	}
	var stage []string
	for _, path := range candidates {
		if secretLooking(path) {
			result.LeftOut = append(result.LeftOut, path)
			continue
		}
		stage = append(stage, path)
	}
	// Literal pathspecs, in bounded batches, so no name is read as a pattern
	// and a large folder does not exceed the argument limit.
	for len(stage) > 0 {
		batch := stage
		if len(batch) > 500 {
			batch = batch[:500]
		}
		stage = stage[len(batch):]
		args := []string{"--literal-pathspecs", "add", "--"}
		if err := runGit(ctx, dir, append(args, batch...)...); err != nil {
			return firstCommit{}, fmt.Errorf("record this project's files: %w", err)
		}
		result.Files += len(batch)
	}
	if err := runGit(ctx, dir, "commit", "--allow-empty", "-q", "-m", "Initial commit"); err != nil {
		return firstCommit{}, fmt.Errorf("make this project's first commit: %w", err)
	}
	result.Started = true
	return result, nil
}

// untrackedFiles lists the files the first commit could hold: everything not
// ignored by the project's .gitignore or Marshal's own exclusion.
func untrackedFiles(ctx context.Context, dir string) ([]string, error) {
	command, err := hostgit.Command(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	command.Stdin = nil
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, path := range strings.Split(string(output), "\x00") {
		if path != "" {
			files = append(files, path)
		}
	}
	return files, nil
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
func startedRepositoryLine(dir string, commit firstCommit) string {
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
	files := fmt.Sprintf("%d files", commit.Files)
	if commit.Files == 1 {
		files = "1 file"
	}
	line := fmt.Sprintf("Started a new Git repository for this project in %s and committed %s", shown, files)
	if len(commit.LeftOut) > 0 {
		shownLeft := commit.LeftOut
		more := ""
		if len(shownLeft) > 3 {
			more = fmt.Sprintf(" and %d more", len(shownLeft)-3)
			shownLeft = shownLeft[:3]
		}
		line += " (left out: " + strings.Join(shownLeft, ", ") + more + ")"
	}
	return line + "."
}

// prepareProjectRepository is the step init and the control centre share: the
// folder gets its own repository when it needs one, and the person is told.
func (c *command) prepareProjectRepository(ctx context.Context) (bool, error) {
	commit, err := prepareProjectRepository(ctx, c.root)
	if commit.Started {
		fmt.Fprintln(c.stderr, startedRepositoryLine(c.root, commit))
	}
	return commit.Started, err
}
