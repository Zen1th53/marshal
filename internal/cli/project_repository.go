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
	"github.com/Zen1th53/marshal/internal/project"
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
	// Committed lists the files the first commit holds, relative to the
	// project folder.
	Committed []string
	// LeftOut lists files that look like secrets by name and were left
	// untracked, relative to the project folder. Each is also listed in the
	// project's .gitignore (see Listed), so a later `git add .` leaves it out.
	LeftOut []string
	// Listed reports that the left-out files were added to the .gitignore.
	Listed bool
	// WroteGitignore reports that the .gitignore is the default one Marshal
	// wrote; otherwise the left-out files were added to the person's own.
	WroteGitignore bool
}

// defaultGitignore is written to a folder that has no .gitignore before its
// first commit, so secrets and dependency or build directories stay out.
const defaultGitignore = project.GitignoreHeader + `
.env
.env.*
*.pem
*.key
*.p12
*.pfx
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
	for _, pattern := range []string{".env*", "*.pem", "*.key", "id_rsa*", "*.p12", "*.pfx", "credentials*.json"} {
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
// Those files are also listed in the .gitignore, the default one or the
// person's own, in the same first commit, so a later `git add .` does not
// commit them either.
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
	ignoreInfo, ignoreErr := os.Lstat(ignorePath)
	if ignoreErr != nil && !os.IsNotExist(ignoreErr) {
		return firstCommit{}, ignoreErr
	}
	wroteIgnore := false
	var originalIgnore []byte
	changedIgnore := false
	// Only a regular .gitignore is added to; one that is a link or anything
	// else is the person's to look at.
	ignoreWritable := ignoreErr == nil && ignoreInfo.Mode().IsRegular()
	if ignoreWritable {
		if originalIgnore, err = os.ReadFile(ignorePath); err != nil {
			return firstCommit{}, err
		}
	}
	if err := runGit(ctx, dir, "init", "-q"); err != nil {
		return firstCommit{}, fmt.Errorf("start a Git repository for this project: %w", err)
	}
	defer func() {
		// A half-made repository would leave the folder neither as it was nor
		// usable. Only what this call created is removed: the .git and a
		// default .gitignore, neither of which existed before; the person's
		// own .gitignore gets its earlier content back.
		if err != nil {
			_ = os.RemoveAll(filepath.Join(dir, ".git"))
			if wroteIgnore {
				_ = os.Remove(ignorePath)
			} else if changedIgnore {
				_ = os.WriteFile(ignorePath, originalIgnore, 0o644)
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
	if len(result.LeftOut) > 0 && (wroteIgnore || ignoreWritable) {
		data := originalIgnore
		if wroteIgnore {
			data = []byte(defaultGitignore)
		}
		changedIgnore = !wroteIgnore
		if err := os.WriteFile(ignorePath, appendLeftOut(data, result.LeftOut), 0o644); err != nil {
			return firstCommit{}, fmt.Errorf("list the left-out files in .gitignore: %w", err)
		}
		result.Listed = true
	}
	result.WroteGitignore = wroteIgnore
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
		result.Committed = append(result.Committed, batch...)
	}
	if err := runGit(ctx, dir, "commit", "--allow-empty", "-q", "-m", "Initial commit"); err != nil {
		return firstCommit{}, fmt.Errorf("make this project's first commit: %w", err)
	}
	result.Started = true
	return result, nil
}

// leftOutComment heads the lines Marshal adds to a .gitignore for the files it
// left out of the first commit.
const leftOutComment = "# Left out by Marshal: secret-looking files"

// appendLeftOut adds the left-out files to a .gitignore's content under
// leftOutComment, each as an exact path from the project root.
func appendLeftOut(data []byte, leftOut []string) []byte {
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, leftOutComment+"\n"...)
	for _, path := range leftOut {
		data = append(data, gitignoreExactPath(path)+"\n"...)
	}
	return data
}

// gitignoreExactPath is a .gitignore line that matches exactly one path from
// the project root: anchored with a leading slash, with every character Git
// would read as a pattern escaped. A line break cannot be written in a
// .gitignore, so it is matched by a single-character wildcard instead.
func gitignoreExactPath(path string) string {
	var b strings.Builder
	b.WriteByte('/')
	for _, r := range filepath.ToSlash(path) {
		switch r {
		case '\\', '*', '?', '[', ']', '!', '#':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n', '\r':
			b.WriteByte('?')
		default:
			b.WriteRune(r)
		}
	}
	line := b.String()
	// Git drops unescaped trailing spaces.
	trimmed := strings.TrimRight(line, " ")
	return trimmed + strings.Repeat("\\ ", len(line)-len(trimmed))
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
// started for the project. second lists the files Marshal then committed in
// its own, second commit; none when it made no second commit.
func startedRepositoryLine(dir string, commit firstCommit, second []string) string {
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
	first := "empty"
	switch {
	case len(commit.Committed) > 3:
		first = fmt.Sprintf("%d files", len(commit.Committed))
	case len(commit.Committed) > 0:
		first = joinNames(commit.Committed)
	case commit.Files > 0:
		first = fmt.Sprintf("%d files", commit.Files)
	}
	line := fmt.Sprintf("Started a new Git repository for this project in %s (first commit: %s).", shown, first)
	if len(commit.LeftOut) > 0 {
		names := commit.LeftOut
		if len(names) > 3 {
			names = append(append([]string(nil), names[:3]...), fmt.Sprintf("%d more", len(commit.LeftOut)-3))
		}
		looks, pronoun := "look like secrets", "them"
		if len(commit.LeftOut) == 1 {
			looks, pronoun = "looks like a secret", "it"
		}
		where := "your .gitignore"
		if commit.WroteGitignore {
			where = ".gitignore"
		}
		if commit.Listed {
			line += fmt.Sprintf(" Left out %s, which %s, and listed %s in %s.", joinNames(names), looks, pronoun, where)
		} else {
			line += fmt.Sprintf(" Left out %s, which %s.", joinNames(names), looks)
		}
	}
	if len(second) > 0 {
		line += fmt.Sprintf(" Marshal added %s in a second commit.", joinNames(second))
	}
	return line
}

// joinNames lists names as "a", "a and b" or "a, b and c".
func joinNames(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// prepareProjectRepository is the step init and the control centre share: the
// folder gets its own repository when it needs one. The caller says so with
// reportStartedRepository once Marshal's own commit is made, so the line can
// tell the whole story.
func (c *command) prepareProjectRepository(ctx context.Context) (firstCommit, error) {
	return prepareProjectRepository(ctx, c.root)
}

// reportStartedRepository prints the started line when a repository was
// started; second lists the files of Marshal's own, second commit.
func (c *command) reportStartedRepository(commit firstCommit, second []string) {
	if commit.Started {
		fmt.Fprintln(c.stderr, startedRepositoryLine(c.root, commit, second))
	}
}
