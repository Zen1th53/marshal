package projectknowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
)

func fixture(t *testing.T) (string, *Index) {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-b", "main")
	git(t, root, "config", "user.name", "Fixture")
	git(t, root, "config", "user.email", "fixture@example.invalid")
	write(t, root, "go.mod", "module example.invalid/project\n\ngo 1.25\n")
	write(t, root, "pkg/a.go", "package pkg\nimport \"fmt\"\ntype Box[T any] struct{}\nfunc (b *Box[T]) Run() { fmt.Println(Helper()) }\nfunc Helper() int { return 1 }\n")
	write(t, root, "pkg/a_test.go", "package pkg\nimport \"testing\"\nfunc TestHelper(t *testing.T) { Helper() }\n")
	write(t, root, "other/a.go", "package pkg\nfunc Elsewhere(){}\n")
	write(t, root, "README.md", "# Fixture project\n## Build and tests\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "fixture")
	idx, err := Open(root, filepath.Join(root, statepath.DirName))
	if err != nil {
		t.Fatal(err)
	}
	return root, idx
}
func git(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("/usr/bin/git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture Git failed: %v %s", err, out)
	}
}
func write(t *testing.T, root, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func query(t *testing.T, idx *Index, mode, text, path string) Result {
	t.Helper()
	r, err := idx.Query(t.Context(), Query{Mode: mode, Text: text, Path: path, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestKnowledgeASTIncrementalRestartAndDeletion(t *testing.T) {
	root, idx := fixture(t)
	cold, err := idx.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cold.Stats.Changed != 5 || cold.Stats.GoFiles != 3 || cold.Stats.Definitions != 5 || cold.Stats.ParseErrors != 0 {
		t.Fatalf("coverage unexpected: %+v", cold.Stats)
	}
	r := query(t, idx, "symbol", "Run", "")
	if len(r.Items) != 1 || r.Items[0].Receiver != "Box" || r.Items[0].Line != 4 || r.Items[0].FileHash == "" {
		t.Fatal("generic method location/provenance missing")
	}
	refs := query(t, idx, "references", "Helper", "")
	if len(refs.Items) != 2 {
		t.Fatal("syntactic references did not preserve both call sites")
	}
	related := query(t, idx, "related", "", "pkg/a.go")
	for _, entry := range related.Items {
		if strings.HasPrefix(entry.Path, "other/") {
			t.Fatal("same package name crossed directory")
		}
	}
	facts := query(t, idx, "overview", "", "")
	found := false
	for _, e := range facts.Items {
		if e.Kind == "project_fact" && strings.HasPrefix(e.Name, "module ") {
			found = true
		}
	}
	if !found {
		t.Fatal("module project fact absent")
	}
	warm, err := idx.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if warm.Stats.Changed != 0 || warm.Stats.Reused != 5 {
		t.Fatal("unchanged files reparsed")
	}
	reopened, err := Open(root, filepath.Join(root, statepath.DirName))
	if err != nil {
		t.Fatal(err)
	}
	if len(query(t, reopened, "symbol", "Helper", "").Items) != 1 {
		t.Fatal("persistent restart lost definition")
	}
	write(t, root, "pkg/a.go", "package pkg\nfunc Replacement(){}\n")
	if _, err = idx.Query(t.Context(), Query{Mode: "symbol", Text: "Helper", Limit: 10}); !errors.Is(err, ErrStale) {
		t.Fatal("dirty tracked bytes silently reused")
	}
	updated, err := idx.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if updated.Stats.Changed != 1 || updated.Stats.Reused != 4 {
		t.Fatal("incremental update did not isolate changed file")
	}
	if len(query(t, idx, "symbol", "Helper", "").Items) != 0 {
		t.Fatal("changed old definition remained")
	}
	if err = os.Remove(filepath.Join(root, "other/a.go")); err != nil {
		t.Fatal(err)
	}
	if _, err = idx.Query(t.Context(), Query{Mode: "symbol", Text: "Elsewhere", Limit: 10}); !errors.Is(err, ErrStale) {
		t.Fatal("deleted tracked source served")
	}
	deleted, err := idx.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Stats.Deleted != 1 || len(query(t, idx, "symbol", "Elsewhere", "").Items) != 0 {
		t.Fatal("deleted entries retained")
	}
	git(t, root, "add", "-u")
	git(t, root, "commit", "-m", "changed")
	if _, err = idx.Query(t.Context(), Query{Mode: "overview", Limit: 10}); !errors.Is(err, ErrStale) {
		t.Fatal("new HEAD silently accepted")
	}
}

func TestKnowledgeSensitiveSymlinkIgnoredAndUntrackedExclusions(t *testing.T) {
	root, idx := fixture(t)
	write(t, root, "private.go", "package pkg\nvar PRIVATE_SECRET_SYMBOL = \""+"ghp_"+strings.Repeat("a", 36)+"\"\n")
	write(t, root, "credentials.go", "package pkg\nfunc CREDENTIAL_PATH_SYMBOL(){}\n")
	write(t, root, "ghp_"+strings.Repeat("b", 36)+".go", "package pkg\nfunc SECRET_FILENAME_SYMBOL(){}\n")
	write(t, root, ".marshal-extreme/private.go", "package pkg\nfunc EXTREME_STATE_SYMBOL(){}\n")
	write(t, root, ".gitignore", "ignored.go\n")
	write(t, root, "ignored.go", "package pkg\nfunc IGNORED_SYMBOL(){}\n")
	protocol, _ := constitution.MarshalProtocol()
	write(t, root, "protocol-note.md", protocol)
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package pkg\nfunc OUTSIDE_SYMBOL(){}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "add", "-f", "ignored.go")
	git(t, root, "commit", "-m", "exclude fixtures")
	write(t, root, "untracked.go", "package pkg\nfunc UNTRACKED_SYMBOL(){}\n")
	st, err := idx.Refresh(t.Context())
	if err != nil {
		t.Fatal("sensitive fixture refresh failed")
	}
	if st.Stats.Skipped < 5 {
		t.Fatal("exclusion coverage absent")
	}
	for _, name := range []string{"PRIVATE_SECRET_SYMBOL", "CREDENTIAL_PATH_SYMBOL", "SECRET_FILENAME_SYMBOL", "EXTREME_STATE_SYMBOL", "IGNORED_SYMBOL", "OUTSIDE_SYMBOL", "UNTRACKED_SYMBOL"} {
		if len(query(t, idx, "symbol", name, "").Items) != 0 {
			t.Fatal("excluded source metadata escaped")
		}
	}
	r := query(t, idx, "search", "protocol", "")
	if len(r.Items) != 0 {
		t.Fatal("raw protocol headings indexed")
	}
	bytes, err := os.ReadFile(idx.database)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), "PRIVATE_SECRET_SYMBOL") || strings.Contains(string(bytes), "ghp_") {
		t.Fatal("derived cache retained sensitive material")
	}
	if _, err = Open(root, filepath.Join(root, "elsewhere")); !errors.Is(err, ErrBinding) {
		t.Fatal("caller-selected cache scope accepted")
	}
	if _, err = idx.Query(t.Context(), Query{Mode: "related", Path: "../outside.go", Limit: 10}); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestKnowledgeBindingCacheSymlinkAndCancellation(t *testing.T) {
	root, idx := fixture(t)
	if _, err := idx.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	root2, idx2 := fixture(t)
	if err := os.MkdirAll(filepath.Dir(idx2.database), 0700); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(idx.database)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(idx2.database, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = idx2.Query(t.Context(), Query{Mode: "overview", Limit: 10}); !errors.Is(err, ErrBinding) {
		t.Fatal("foreign root cache accepted")
	}
	if err = os.RemoveAll(filepath.Join(root2, statepath.DirName)); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(root, statepath.DirName), filepath.Join(root2, statepath.DirName)); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(root2, filepath.Join(root2, statepath.DirName)); !errors.Is(err, ErrBinding) {
		t.Fatal("symlink cache root accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = idx.Refresh(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
}

func TestKnowledgeConcurrentReadRefreshAndParseFailure(t *testing.T) {
	root, idx := fixture(t)
	if _, err := idx.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if n%2 == 0 {
				_, err := idx.Refresh(t.Context())
				errs <- err
			} else {
				_, err := idx.Query(t.Context(), Query{Mode: "symbol", Text: "Helper", Limit: 10})
				errs <- err
			}
		}(n)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	write(t, root, "pkg/a.go", "package pkg\nfunc Helper( broken\n")
	st, err := idx.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.Stats.ParseErrors != 1 {
		t.Fatal("parse failure absent from coverage")
	}
	if len(query(t, idx, "symbol", "Helper", "").Items) != 0 {
		t.Fatal("parse failure reused old definitions")
	}
}

func TestKnowledgeRechecksCachedSourceMetadataBeforeExport(t *testing.T) {
	_, idx := fixture(t)
	if _, err := idx.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	db, err := idx.connection(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	// Derived-cache corruption is not an authority to resurrect excluded paths.
	if _, err = db.ExecContext(t.Context(), "INSERT INTO entries VALUES(?,?,?,?,?,?)", -1, 1, "Helper", "function", "", ""); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	r := query(t, idx, "symbol", "Helper", "")
	if len(r.Items) != 1 || r.Total != 1 || r.Items[0].Path != "pkg/a.go" {
		t.Fatal("excluded cache row affected rank/count/output")
	}
	db, err = idx.connection(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), "UPDATE entries SET line=999 WHERE name='Helper' AND kind='function'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err = idx.Query(t.Context(), Query{Mode: "symbol", Text: "Helper", Limit: 10}); !errors.Is(err, ErrStale) {
		t.Fatal("wrong canonical source location exported")
	}
}

func TestKnowledgePrivacyCacheBoundPolicyRootAndChangedBytes(t *testing.T) {
	data := []byte("package plain\nfunc SafeKnowledgeSymbol(){}\n")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	root := t.TempDir()
	safe, _ := contentVerdict(root, hash, data)
	if !safe {
		t.Fatal("ordinary source excluded")
	}
	safe, reused := contentVerdict(root, hash, data)
	if !safe || !reused {
		t.Fatal("unchanged-content verdict not reused")
	}
	_, reused = contentVerdict(t.TempDir(), hash, data)
	if reused {
		t.Fatal("privacy cache reused cross-root verdict")
	}
	// A linked worktree of the same repository shares the verdicts.
	repo, lane := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git", "worktrees", "lane"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, ".git"), []byte("gitdir: "+filepath.Join(repo, ".git", "worktrees", "lane")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	contentVerdict(repo, hash, data)
	if _, reused = contentVerdict(lane, hash, data); !reused {
		t.Fatal("a linked worktree did not share its repository's verdict")
	}
	secret := []byte("package plain\nvar SafeKnowledgeSymbol = \"" + "ghp_" + strings.Repeat("c", 36) + "\"\n")
	sum = sha256.Sum256(secret)
	safe, reused = contentVerdict(root, hex.EncodeToString(sum[:]), secret)
	if safe || reused {
		t.Fatal("changed secret bytes reused clean verdict")
	}
	privacyCache.Lock()
	defer privacyCache.Unlock()
	if len(privacyCache.verdicts) > 8192 || len(privacyCache.order) > 8192 {
		t.Fatal("privacy cache entry bound exceeded")
	}
	for key := range privacyCache.verdicts {
		if len(key) != 64 {
			t.Fatal("unbounded/raw privacy cache key")
		}
	}
}

func TestKnowledgePolicyMismatchAndPoisonedSensitiveCacheFailClosed(t *testing.T) {
	root, idx := fixture(t)
	write(t, root, "sensitive.go", "package pkg\nfunc SensitiveSourceSymbol(){}\nvar privateValue=\""+"ghp_"+strings.Repeat("d", 36)+"\"\n")
	git(t, root, "add", "sensitive.go")
	git(t, root, "commit", "-m", "excluded source fixture")
	if _, err := idx.Refresh(t.Context()); err != nil {
		t.Fatal("fixture refresh failed")
	}
	db, err := idx.connection(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "sensitive.go"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	// Test-only corruption cannot turn previously excluded content into authority.
	if _, err = db.ExecContext(t.Context(), "INSERT INTO files VALUES(?,?,?,?,?)", -1, "sensitive.go", hash, "pkg", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), "INSERT INTO entries VALUES(?,?,?,?,?,?)", -1, 2, "SensitiveSourceSymbol", "function", "", ""); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err = idx.Query(t.Context(), Query{Mode: "symbol", Text: "SensitiveSourceSymbol", Limit: 10}); !errors.Is(err, ErrStale) {
		t.Fatal("poisoned retained sensitive metadata exported")
	}
	db, err = idx.connection(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), "UPDATE metadata SET snapshot=json_set(snapshot,'$.policy_digest','wrong-policy')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err = idx.Query(t.Context(), Query{Mode: "symbol", Text: "Helper", Limit: 10}); !errors.Is(err, ErrStale) {
		t.Fatal("different source-filter policy silently reused")
	}
}
