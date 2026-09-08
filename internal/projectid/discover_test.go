package projectid_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectid"
)

// newRepo creates a real Git repository with one commit.
func newRepo(t *testing.T) string {
	t.Helper()
	return newRepoWithContent(t, "project\n")
}

// newRepoWithContent creates a repository whose initial commit differs by
// content, so that two repositories built this way have different root
// commits. Two repositories with byte-identical first commits and identical
// commit metadata genuinely do share a root hash, and are then the same
// lineage as far as Git is concerned.
func newRepoWithContent(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", ".")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "chore: init")
	return dir
}

// sameRoot reports whether two repositories share a root commit.
func sameRoot(t *testing.T, a, b string) bool {
	t.Helper()
	return git(t, a, "rev-list", "--max-parents=0", "HEAD") ==
		git(t, b, "rev-list", "--max-parents=0", "HEAD")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("git unavailable in this environment: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func marshalDir(root string) string { return filepath.Join(root, ".marshal") }

func TestEvidenceComesFromTheRepositoryNotThePath(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	evidence := projectid.CollectEvidence(ctx, nil, repo)
	if evidence.RootCommit == "" {
		t.Fatal("no root commit was collected from a repository with a commit")
	}
	if !evidence.HasDurableEvidence() {
		t.Fatal("a repository with history reported no durable evidence")
	}

	// Cloning produces the same root commit, so a clone is recognised as the
	// same repository lineage.
	clone := filepath.Join(t.TempDir(), "clone")
	git(t, t.TempDir(), "clone", "-q", repo, clone)
	cloned := projectid.CollectEvidence(ctx, nil, clone)
	if cloned.RootCommit != evidence.RootCommit {
		t.Fatalf("a clone reported a different root commit: %q vs %q", cloned.RootCommit, evidence.RootCommit)
	}
}

// A repository with no commits yields no root commit, which is an ordinary
// state rather than an error.
func TestEmptyRepositoryYieldsNoRootCommit(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", ".")

	evidence := projectid.CollectEvidence(context.Background(), nil, dir)
	if evidence.RootCommit != "" {
		t.Fatalf("an empty repository reported a root commit: %q", evidence.RootCommit)
	}
	if evidence.HasDurableEvidence() {
		t.Fatal("an empty repository reported durable evidence")
	}
}

// Adoption establishes an identity and records it where it will travel with
// the project.
func TestAdoptEstablishesAndPersistsIdentity(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	binding, err := projectid.Adopt(ctx, nil, repo, marshalDir(repo))
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if !binding.ID.Valid() {
		t.Fatalf("adoption produced a malformed identity: %q", binding.ID)
	}

	loaded, found := projectid.LoadBinding(marshalDir(repo))
	if !found {
		t.Fatal("the binding was not persisted")
	}
	if loaded.ID != binding.ID {
		t.Fatal("the persisted binding holds a different identity")
	}

	// Adopting again is idempotent.
	again, err := projectid.Adopt(ctx, nil, repo, marshalDir(repo))
	if err != nil {
		t.Fatalf("re-adopt: %v", err)
	}
	if again.ID != binding.ID {
		t.Fatal("re-adopting the same project changed its identity")
	}
}

// A project with no repository history still gets a durable identity, via a
// nonce recorded in its binding.
func TestProjectWithoutHistoryStillGetsAnIdentity(t *testing.T) {
	dir := t.TempDir()
	binding, err := projectid.Adopt(context.Background(), nil, dir, marshalDir(dir))
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if !binding.ID.Valid() {
		t.Fatal("a project with no history got no usable identity")
	}
	if binding.Evidence.CreatedNonce == "" {
		t.Fatal("a project with no history got no nonce to identify it")
	}
}

// The end-to-end move case: identity and adoptability survive relocation.
func TestMovedProjectResolvesToTheSameIdentity(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	original, err := projectid.Adopt(ctx, nil, repo, marshalDir(repo))
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}

	moved := filepath.Join(filepath.Dir(repo), "moved-project")
	if err := os.Rename(repo, moved); err != nil {
		t.Skipf("cannot move the project here: %v", err)
	}

	resolution := projectid.Resolve(ctx, nil, moved, marshalDir(moved))
	if resolution.ID != original.ID {
		t.Fatalf("a moved project resolved to %q, want %q", resolution.ID, original.ID)
	}
	if !resolution.AdoptExisting {
		t.Fatalf("a moved project was not safe to adopt: %s", resolution.Reason)
	}
	if !resolution.NeedsRebind {
		t.Fatal("a moved project was not flagged for rebinding")
	}
	if resolution.Comparison.Verdict != projectid.VerdictMoved {
		t.Fatalf("verdict was %s, want MOVED", resolution.Comparison.Verdict)
	}

	// Adopting at the new path updates where it was last seen without
	// changing the identity.
	rebound, err := projectid.Adopt(ctx, nil, moved, marshalDir(moved))
	if err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if rebound.ID != original.ID {
		t.Fatal("rebinding changed the project identity")
	}
	if rebound.RecordedRoot != moved {
		t.Fatalf("rebinding did not record the new location: %q", rebound.RecordedRoot)
	}
}

// The reused-path attack: a different repository must not inherit the previous
// project's identity or be allowed to adopt its state.
func TestDifferentRepositoryAtReusedPathIsRefused(t *testing.T) {
	ctx := context.Background()
	first := newRepo(t)
	original, err := projectid.Adopt(ctx, nil, first, marshalDir(first))
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}

	// A genuinely different repository is created — different initial content,
	// so a different root commit — and the previous project's state directory
	// is copied into it. This is the shape a careless backup restore or a
	// copied project template takes.
	second := newRepoWithContent(t, "an unrelated project\n")
	if sameRoot(t, first, second) {
		t.Fatal("the two test repositories share a root commit; the scenario is not set up")
	}
	copyBinding(t, marshalDir(first), marshalDir(second))

	resolution := projectid.Resolve(ctx, nil, second, marshalDir(second))
	if resolution.AdoptExisting {
		t.Fatal("a different repository was allowed to adopt copied project state")
	}
	if resolution.ID == original.ID {
		t.Fatal("a different repository inherited the previous project's identity")
	}
	if resolution.Comparison.Verdict != projectid.VerdictDifferent {
		t.Fatalf("verdict was %s, want DIFFERENT", resolution.Comparison.Verdict)
	}

	// Adoption refuses outright rather than overwriting the foreign binding.
	if _, err := projectid.Adopt(ctx, nil, second, marshalDir(second)); err == nil {
		t.Fatal("adoption overwrote a binding belonging to a different project")
	}
}

// State with no binding is not adopted on the strength of merely existing.
func TestUnboundStateIsNotAdopted(t *testing.T) {
	repo := newRepo(t)
	if err := os.MkdirAll(marshalDir(repo), 0o700); err != nil {
		t.Fatal(err)
	}
	// A state directory exists but says nothing about which project it is.
	resolution := projectid.Resolve(context.Background(), nil, repo, marshalDir(repo))

	if resolution.AdoptExisting {
		t.Fatal("project state with no identity was adopted")
	}
	if resolution.Bound {
		t.Fatal("unbound state was reported as bound")
	}
	if !strings.Contains(strings.ToLower(resolution.Reason), "does not say which project") {
		t.Fatalf("the reason does not explain the problem: %q", resolution.Reason)
	}
}

// A corrupt or truncated binding is not guessed at.
func TestCorruptBindingIsNotTrusted(t *testing.T) {
	repo := newRepo(t)
	if err := os.MkdirAll(marshalDir(repo), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(marshalDir(repo), projectid.BindingFileName)
	for _, content := range []string{"", "{", `{"project_id":"PROJECT-nope"}`, "not json at all"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, found := projectid.LoadBinding(marshalDir(repo)); found {
			t.Fatalf("a corrupt binding was loaded as valid: %q", content)
		}
		resolution := projectid.Resolve(context.Background(), nil, repo, marshalDir(repo))
		if resolution.AdoptExisting {
			t.Fatalf("a corrupt binding permitted adoption: %q", content)
		}
	}
}

// Resolving never writes. Looking at a project must not adopt it.
func TestResolveNeverWrites(t *testing.T) {
	repo := newRepo(t)
	before := dirEntries(t, repo)

	resolution := projectid.Resolve(context.Background(), nil, repo, marshalDir(repo))
	if resolution.Bound {
		t.Fatal("an unadopted project reported a binding")
	}
	if _, err := os.Stat(marshalDir(repo)); err == nil {
		t.Fatal("resolving created the project state directory")
	}
	if after := dirEntries(t, repo); len(after) != len(before) {
		t.Fatalf("resolving changed the directory: %v then %v", before, after)
	}
}

// Saving refuses an invalid binding, so a malformed identity cannot be
// persisted and later read back as authoritative.
func TestSaveRefusesInvalidBindings(t *testing.T) {
	dir := t.TempDir()
	if err := projectid.SaveBinding(dir, projectid.Binding{}); err == nil {
		t.Fatal("an empty binding was saved")
	}
	if err := projectid.SaveBinding(dir, projectid.Binding{
		ID: "PROJECT-bogus", SchemaVersion: projectid.BindingSchemaVersion,
	}); err == nil {
		t.Fatal("a binding with a malformed ID was saved")
	}
	if _, found := projectid.LoadBinding(dir); found {
		t.Fatal("a rejected binding was nonetheless persisted")
	}
}

func copyBinding(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(from, projectid.BindingFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(to, projectid.BindingFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
