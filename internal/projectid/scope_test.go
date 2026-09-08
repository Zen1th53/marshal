package projectid_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectid"
)

func qualify(t *testing.T, root string) projectid.GitQualification {
	t.Helper()
	return projectid.QualifyGit(context.Background(), nil, root)
}

func scopeFor(t *testing.T, root, subpath string) projectid.Scope {
	t.Helper()
	scope, err := projectid.ResolveScope(root, subpath, qualify(t, root))
	if err != nil {
		t.Fatalf("resolve scope: %v", err)
	}
	return scope
}

func TestCleanRepositoryIsReadyAndSafeToMutate(t *testing.T) {
	repo := newRepo(t)
	qualification := qualify(t, repo)

	if qualification.State != projectid.GitReady {
		t.Fatalf("a clean repository qualified as %s: %s", qualification.State, qualification.Reason)
	}
	if !qualification.SafeToMutate() {
		t.Fatal("a clean repository was not safe to modify")
	}
	if qualification.Dirty {
		t.Fatal("a clean repository was reported dirty")
	}
}

// Uncommitted work is the one thing Git cannot recover, so it must be
// detected and named rather than silently overwritten.
func TestDirtyWorktreeIsDetectedAndNamed(t *testing.T) {
	repo := newRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "in-progress.txt"), []byte("unsaved\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	qualification := qualify(t, repo)
	if !qualification.Dirty {
		t.Fatal("uncommitted work was not detected")
	}
	if qualification.SafeToMutate() {
		t.Fatal("MARSHAL considered it safe to modify a tree with uncommitted work")
	}
	found := false
	for _, path := range qualification.DirtyPaths {
		if path == "in-progress.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the uncommitted file was not named: %v", qualification.DirtyPaths)
	}
	// The repository itself is still usable; only unattended mutation is not.
	if qualification.State != projectid.GitReady {
		t.Fatalf("a dirty but otherwise healthy repository qualified as %s", qualification.State)
	}
}

// A repository with no commits has no baseline to roll back to.
func TestRepositoryWithoutCommitsIsLimited(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", ".")

	qualification := qualify(t, dir)
	if qualification.State != projectid.GitLimited {
		t.Fatalf("an empty repository qualified as %s, want LIMITED", qualification.State)
	}
	if qualification.State.Recoverable() {
		t.Fatal("an empty repository was reported as recoverable")
	}
	if qualification.SafeToMutate() {
		t.Fatal("a repository with nothing to roll back to was safe to modify")
	}
	if qualification.HasCommits {
		t.Fatal("an empty repository reported commits")
	}
}

// A non-repository cannot make changes recoverable at all.
func TestNonRepositoryIsBlocked(t *testing.T) {
	qualification := qualify(t, t.TempDir())
	if qualification.State != projectid.GitBlocked {
		t.Fatalf("a plain directory qualified as %s, want BLOCKED", qualification.State)
	}
	if qualification.State.Recoverable() || qualification.SafeToMutate() {
		t.Fatal("a plain directory was treated as recoverable")
	}
}

// A detached HEAD still works but makes recorded work harder to find.
func TestDetachedHeadIsLimited(t *testing.T) {
	repo := newRepo(t)
	head := git(t, repo, "rev-parse", "HEAD")
	git(t, repo, "checkout", "-q", "--detach", head)

	qualification := qualify(t, repo)
	if !qualification.DetachedHEAD {
		t.Fatal("a detached HEAD was not detected")
	}
	if qualification.State != projectid.GitLimited {
		t.Fatalf("a detached HEAD qualified as %s, want LIMITED", qualification.State)
	}
}

// Working in a repository mid-operation risks entangling MARSHAL's changes
// with a state the user is partway through resolving.
func TestMidOperationRepositoryIsLimited(t *testing.T) {
	repo := newRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".git", "MERGE_HEAD"), []byte("deadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	qualification := qualify(t, repo)
	if qualification.MidOperation != "merge" {
		t.Fatalf("an in-progress merge was reported as %q", qualification.MidOperation)
	}
	if qualification.SafeToMutate() {
		t.Fatal("a repository mid-merge was safe to modify")
	}
}

// The scope boundary: paths inside are allowed, paths outside are not.
func TestScopeContainsOnlyProjectPaths(t *testing.T) {
	repo := newRepo(t)
	scope := scopeFor(t, repo, "")

	for _, inside := range []string{
		repo,
		filepath.Join(repo, "README.md"),
		filepath.Join(repo, "src", "deep", "file.go"), // not yet created
	} {
		if !scope.Contains(inside) {
			t.Fatalf("a path inside the project was reported outside scope: %s", inside)
		}
	}
	for _, outside := range []string{
		filepath.Dir(repo),
		filepath.Join(filepath.Dir(repo), "sibling"),
		"/etc/passwd",
		filepath.Join(repo, "..", "escape.txt"),
	} {
		if scope.Contains(outside) {
			t.Fatalf("a path outside the project was reported inside scope: %s", outside)
		}
	}
}

// A sibling directory whose name merely starts with the project's name is not
// inside it. This is the string-prefix trap.
func TestSimilarlyNamedSiblingIsOutsideScope(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "project")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(parent, "project-evil")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	scope := projectid.Scope{Root: repo}
	if scope.Contains(filepath.Join(sibling, "file.txt")) {
		t.Fatal("a similarly named sibling directory was treated as inside the project")
	}
}

// A symlink pointing out of the project does not smuggle a path past the
// boundary.
func TestSymlinkCannotEscapeScope(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	for _, dir := range []string{repo, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repo, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}

	scope := projectid.Scope{Root: repo}
	if scope.Contains(link) {
		t.Fatal("a symlink to outside the project was inside scope")
	}
	if scope.Contains(filepath.Join(link, "secret.txt")) {
		t.Fatal("a file reached through an escaping symlink was inside scope")
	}
	violations := scope.Violations([]string{
		filepath.Join(repo, "ok.txt"),
		filepath.Join(link, "secret.txt"),
	})
	if len(violations) != 1 {
		t.Fatalf("expected exactly the escaping path to be reported: %v", violations)
	}
}

// A nested repository's history belongs to another repository, so changes
// there could not be rolled back with the outer one.
func TestNestedRepositoryIsExcludedFromScope(t *testing.T) {
	repo := newRepo(t)
	nested := filepath.Join(repo, "vendor", "library")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, nested, "init", "-q", ".")

	qualification := qualify(t, repo)
	if len(qualification.NestedRepositories) == 0 {
		t.Fatal("a nested repository was not detected")
	}

	scope := scopeFor(t, repo, "")
	if scope.Contains(filepath.Join(nested, "file.go")) {
		t.Fatal("a file inside a nested repository was in scope")
	}
	if !scope.Contains(filepath.Join(repo, "src", "file.go")) {
		t.Fatal("excluding a nested repository also excluded the rest of the project")
	}
}

// A monorepo can be scoped to one package, and that narrowing is real.
func TestMonorepoScopeNarrowsToASubpath(t *testing.T) {
	repo := newRepo(t)
	pkg := filepath.Join(repo, "packages", "api")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(repo, "packages", "web")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}

	scope := scopeFor(t, repo, filepath.Join("packages", "api"))
	if scope.Subpath != filepath.Join("packages", "api") {
		t.Fatalf("scope subpath was %q", scope.Subpath)
	}
	if !scope.Contains(filepath.Join(pkg, "main.go")) {
		t.Fatal("a file in the scoped package was outside scope")
	}
	if scope.Contains(filepath.Join(other, "main.go")) {
		t.Fatal("a file in a different package was inside the narrowed scope")
	}
	if scope.Contains(filepath.Join(repo, "README.md")) {
		t.Fatal("a file outside the narrowed scope was included")
	}
}

// A requested subpath that resolves outside the project is refused rather than
// clamped, so the disagreement about the boundary is visible.
func TestScopeEscapeIsRefused(t *testing.T) {
	repo := newRepo(t)
	for _, escape := range []string{"..", filepath.Join("..", ".."), "/etc"} {
		_, err := projectid.ResolveScope(repo, escape, qualify(t, repo))
		if err == nil {
			t.Fatalf("a scope escaping to %q was accepted", escape)
		}
		if !errors.Is(err, projectid.ErrScopeEscape) {
			t.Fatalf("escaping to %q failed for an unexpected reason: %v", escape, err)
		}
	}
}

// A scope with an unresolvable base admits nothing, rather than defaulting
// open.
func TestUnresolvableScopeAdmitsNothing(t *testing.T) {
	scope := projectid.Scope{Root: string([]byte{0})}
	if scope.Contains("/anything") {
		t.Fatal("a scope with an unusable root admitted a path")
	}
}

// MARSHAL's own setup files are not the user's uncommitted work. Counting them
// made a freshly initialized project report work at risk, which would attach a
// warning to every routine request in a new project.
func TestMarshalOwnFilesAreNotUserWork(t *testing.T) {
	repo := newRepo(t)
	// Recreate what marshal init leaves behind: its state directory and the
	// version files, none of them committed.
	if err := os.MkdirAll(filepath.Join(repo, ".marshal", "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte("version: 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	qualification := qualify(t, repo)
	if qualification.Dirty {
		t.Fatalf("MARSHAL's own setup files were reported as the user's uncommitted work: %v",
			qualification.DirtyPaths)
	}
	if !qualification.SafeToMutate() {
		t.Fatal("a freshly initialized project was reported unsafe to work in")
	}

	// A real uncommitted file is still detected.
	if err := os.WriteFile(filepath.Join(repo, "user-work.txt"), []byte("in progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withWork := qualify(t, repo)
	if !withWork.Dirty {
		t.Fatal("genuine uncommitted work was not detected")
	}
	for _, path := range withWork.DirtyPaths {
		if strings.HasPrefix(path, ".marshal") || strings.HasSuffix(path, "-VERSION.yaml") {
			t.Fatalf("a MARSHAL-owned file was listed as work at risk: %q", path)
		}
	}
	if len(withWork.DirtyPaths) != 1 || withWork.DirtyPaths[0] != "user-work.txt" {
		t.Fatalf("expected only the user's file to be at risk, got %v", withWork.DirtyPaths)
	}
}
