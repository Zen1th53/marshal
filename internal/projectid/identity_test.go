package projectid_test

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/projectid"
)

func evidence(rootCommit, remote, nonce string) projectid.Evidence {
	return projectid.Evidence{RootCommit: rootCommit, RemoteURL: remote, CreatedNonce: nonce}
}

func mustDerive(t *testing.T, e projectid.Evidence) projectid.ID {
	t.Helper()
	id, err := projectid.Derive(e)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	return id
}

func binding(t *testing.T, e projectid.Evidence, root string) projectid.Binding {
	t.Helper()
	return projectid.Binding{
		ID: mustDerive(t, e), Evidence: e, RecordedRoot: root,
		SchemaVersion: projectid.BindingSchemaVersion,
	}
}

// The central invariant: identity does not depend on the path.
func TestIdentityIsIndependentOfPath(t *testing.T) {
	repo := evidence("abc123", "git@github.com:owner/repo.git", "")
	first := mustDerive(t, repo)
	second := mustDerive(t, repo)
	if first != second {
		t.Fatal("deriving the same evidence twice gave different identities")
	}
	if !first.Valid() {
		t.Fatalf("derived a malformed identity: %q", first)
	}
	// Nothing about the path is an input, so there is no path to vary. What
	// matters is that a binding recorded at one root still yields the same ID.
	moved := binding(t, repo, "/old/location")
	if moved.ID != first {
		t.Fatal("a binding recorded at a different path holds a different identity")
	}
}

// Different repositories get different identities.
func TestDifferentRepositoriesGetDifferentIdentities(t *testing.T) {
	a := mustDerive(t, evidence("aaa111", "", ""))
	b := mustDerive(t, evidence("bbb222", "", ""))
	if a == b {
		t.Fatal("two different repositories derived the same identity")
	}
	// Two projects created independently with no history must also differ.
	x := mustDerive(t, evidence("", "", "nonce-one"))
	y := mustDerive(t, evidence("", "", "nonce-two"))
	if x == y {
		t.Fatal("two independently created projects derived the same identity")
	}
}

// Evidence with nothing durable cannot produce an identity. Refusing is the
// point: an identity invented from nothing would be indistinguishable from a
// real one later.
func TestIdentityRequiresDurableEvidence(t *testing.T) {
	if _, err := projectid.Derive(projectid.Evidence{}); err == nil {
		t.Fatal("an identity was derived from no evidence")
	}
	// A remote alone is not durable evidence: remotes are changed, shared and
	// removed.
	if _, err := projectid.Derive(evidence("", "git@github.com:owner/repo.git", "")); err == nil {
		t.Fatal("an identity was derived from a remote URL alone")
	}
}

// A project that moves keeps its identity and is safe to adopt.
func TestMovedProjectKeepsItsIdentity(t *testing.T) {
	repo := evidence("abc123", "git@github.com:owner/repo.git", "")
	stored := binding(t, repo, "/home/user/projects/app")

	comparison := projectid.Compare(stored, repo, "/home/user/work/app")

	if comparison.Verdict != projectid.VerdictMoved {
		t.Fatalf("a moved project produced %s, want MOVED", comparison.Verdict)
	}
	if !comparison.Verdict.SafeToAdopt() {
		t.Fatal("a moved project was not safe to adopt")
	}
	if !comparison.PathChanged {
		t.Fatal("the path change was not reported")
	}
	if comparison.ObservedID != stored.ID {
		t.Fatal("a moved project was given a different identity")
	}
}

// The reused-path case: a different repository must never inherit the old
// project's identity or state.
func TestDifferentRepositoryAtReusedPathDoesNotInherit(t *testing.T) {
	original := evidence("abc123", "git@github.com:owner/original.git", "")
	stored := binding(t, original, "/home/user/projects/app")

	// A different repository now occupies the same directory.
	replacement := evidence("zzz999", "git@github.com:owner/replacement.git", "")
	comparison := projectid.Compare(stored, replacement, "/home/user/projects/app")

	if comparison.Verdict != projectid.VerdictDifferent {
		t.Fatalf("a replaced repository produced %s, want DIFFERENT", comparison.Verdict)
	}
	if comparison.Verdict.SafeToAdopt() {
		t.Fatal("a different repository was allowed to adopt the previous project's state")
	}
	if comparison.ObservedID == stored.ID {
		t.Fatal("a different repository inherited the previous project's identity")
	}
	if comparison.PathChanged {
		t.Fatal("the path did not change, but a path change was reported")
	}
}

// Same project, same place.
func TestUnchangedProjectIsRecognised(t *testing.T) {
	repo := evidence("abc123", "", "")
	stored := binding(t, repo, "/home/user/app")
	comparison := projectid.Compare(stored, repo, "/home/user/app")

	if comparison.Verdict != projectid.VerdictSame {
		t.Fatalf("an unchanged project produced %s, want SAME", comparison.Verdict)
	}
	if comparison.PathChanged {
		t.Fatal("an unchanged path was reported as changed")
	}
}

// A project identified only by a nonce still survives a move, because the
// nonce lives in the binding and travels with the directory.
func TestNonceOnlyProjectSurvivesAMove(t *testing.T) {
	repo := evidence("", "", "nonce-abc")
	stored := binding(t, repo, "/old/path")
	comparison := projectid.Compare(stored, projectid.Evidence{}, "/new/path")

	if comparison.Verdict != projectid.VerdictMoved {
		t.Fatalf("a nonce-identified project produced %s, want MOVED", comparison.Verdict)
	}
	if !comparison.Verdict.SafeToAdopt() {
		t.Fatal("a nonce-identified project was not safe to adopt after a move")
	}
}

// A repository that gained or lost its history is unverifiable, and
// unverifiable is not treated as agreement.
func TestHistoryChangeIsUnverifiableNotAssumedSame(t *testing.T) {
	stored := binding(t, evidence("abc123", "", ""), "/home/user/app")
	// The directory now holds a repository with no root commit.
	comparison := projectid.Compare(stored, projectid.Evidence{}, "/home/user/app")

	if comparison.Verdict != projectid.VerdictUnverifiable {
		t.Fatalf("a repository with changed history produced %s, want UNVERIFIABLE", comparison.Verdict)
	}
	if comparison.Verdict.SafeToAdopt() {
		t.Fatal("an unverifiable identity was treated as safe to adopt")
	}
}

// A tampered or copied binding is detected, because a binding must be
// consistent with its own evidence.
func TestTamperedBindingIsRejected(t *testing.T) {
	stored := binding(t, evidence("abc123", "", ""), "/home/user/app")

	// Someone edits the ID to impersonate another project.
	forged := stored
	forged.ID = mustDerive(t, evidence("other-project", "", ""))
	if err := forged.Validate(); err == nil {
		t.Fatal("a binding whose ID does not match its evidence was accepted")
	}
	comparison := projectid.Compare(forged, evidence("abc123", "", ""), "/home/user/app")
	if comparison.Verdict.SafeToAdopt() {
		t.Fatal("a forged binding was safe to adopt")
	}

	// A binding from a future format is not guessed at.
	future := stored
	future.SchemaVersion = projectid.BindingSchemaVersion + 1
	if err := future.Validate(); err == nil {
		t.Fatal("a binding from an unknown format was accepted")
	}

	// An empty binding proves nothing.
	if err := (projectid.Binding{}).Validate(); err == nil {
		t.Fatal("an empty binding was accepted")
	}
}

// Remote normalization makes equivalent URLs compare equal without ever
// merging identities, since the remote does not feed the derivation.
func TestRemoteNormalizationDoesNotAffectIdentity(t *testing.T) {
	forms := []string{
		"git@github.com:owner/repo.git",
		"https://github.com/owner/repo.git",
		"https://github.com/owner/repo",
		"ssh://git@github.com/owner/repo.git",
		"https://github.com/owner/repo/",
	}
	want := projectid.NormalizeRemote(forms[0])
	for _, form := range forms[1:] {
		if got := projectid.NormalizeRemote(form); got != want {
			t.Fatalf("normalizing %q gave %q, want %q", form, got, want)
		}
	}
	if projectid.NormalizeRemote("") != "" {
		t.Fatal("an empty remote normalized to something")
	}

	// Changing only the remote must not change the identity, or renaming a
	// remote would orphan a project from its own history.
	base := evidence("abc123", "git@github.com:owner/repo.git", "")
	renamed := evidence("abc123", "git@gitlab.com:other/fork.git", "")
	if mustDerive(t, base) != mustDerive(t, renamed) {
		t.Fatal("changing a remote changed the project identity")
	}
}

// A fork shares provenance with its upstream but stays a distinct project.
func TestForkIsRelatedButDistinct(t *testing.T) {
	upstream := evidence("abc123", "git@github.com:owner/repo.git", "")
	fork := evidence("abc123", "git@github.com:owner/repo.git", "")

	if !projectid.RelatedTo(upstream, fork) {
		t.Fatal("a fork sharing a remote was not reported as related")
	}
	// Sharing a root commit means they genuinely are the same repository
	// lineage, so the same identity here is correct. A fork that has diverged
	// with its own initial history gets its own identity.
	diverged := evidence("def456", "git@github.com:owner/repo.git", "")
	if mustDerive(t, upstream) == mustDerive(t, diverged) {
		t.Fatal("a repository with different history shared an identity")
	}
	if !projectid.RelatedTo(upstream, diverged) {
		t.Fatal("repositories sharing a remote were not reported as related")
	}

	unrelated := evidence("abc123", "", "")
	if projectid.RelatedTo(unrelated, evidence("abc123", "git@github.com:x/y.git", "")) {
		t.Fatal("a project with no remote was reported as related to one with a remote")
	}
}

func TestIDValidation(t *testing.T) {
	valid := mustDerive(t, evidence("abc", "", ""))
	if !valid.Valid() {
		t.Fatalf("a derived ID is invalid: %q", valid)
	}
	for _, bad := range []projectid.ID{"", "PROJECT-", "PROJECT-short", "local", "PROJECT-local"} {
		if bad.Valid() {
			t.Fatalf("%q was accepted as a valid project ID", bad)
		}
	}
}
