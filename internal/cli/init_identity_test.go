package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateGitIdentity points Git at an empty home with no system config and
// useConfigOnly, so no author identity can be guessed from the host either.
func isolateGitIdentity(t *testing.T, globalConfig string) {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(globalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	for _, key := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
}

func baselineRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testGit(t, dir, "init", "-q")
	testGit(t, dir, "-c", "user.name=Seed", "-c", "user.email=seed@example.invalid", "commit", "--allow-empty", "-q", "-m", "Initial commit")
	return dir
}

func TestInitSucceedsWithoutGitIdentity(t *testing.T) {
	isolateGitIdentity(t, "[user]\n\tuseConfigOnly = true\n")
	dir := baselineRepository(t)
	_, stderr, code := runCLI(t, dir, "init")
	if code != 0 {
		t.Fatalf("init without a Git identity failed (%d): %s", code, stderr)
	}
	if got := testGit(t, dir, "log", "-1", "--format=%an <%ae>|%cn <%ce>|%s"); got != "MARSHAL <marshal@localhost>|MARSHAL <marshal@localhost>|Initialize MARSHAL project" {
		t.Fatalf("unexpected initialization commit: %q", got)
	}
	assertInitializedProject(t, dir)
}

func TestInitKeepsConfiguredGitIdentity(t *testing.T) {
	isolateGitIdentity(t, "[user]\n\tname = Real Person\n\temail = real@example.test\n")
	dir := baselineRepository(t)
	_, stderr, code := runCLI(t, dir, "init")
	if code != 0 {
		t.Fatalf("init failed (%d): %s", code, stderr)
	}
	got := testGit(t, dir, "log", "-1", "--format=%an <%ae>|%cn <%ce>")
	if want := "Real Person <real@example.test>|Real Person <real@example.test>"; got != want {
		t.Fatalf("configured identity overridden: %q", got)
	}
	if strings.Contains(testGit(t, dir, "log", "--format=%an"), "MARSHAL") {
		t.Fatal("MARSHAL identity used despite configured identity")
	}
}
