package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/startup"
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

// Init commits files to the user's repository, so it says which ones; a
// repeated init commits nothing and says nothing about committing.
func TestInitSaysWhichFilesItCommitted(t *testing.T) {
	isolateGitIdentity(t, "[user]\n\tname = Real Person\n\temail = real@example.test\n")
	dir := baselineRepository(t)
	out, stderr, code := runCLI(t, dir, "init")
	if code != 0 {
		t.Fatalf("init failed (%d): %s", code, stderr)
	}
	want := "Committed CAPABILITIES.yaml, PACK-VERSION.yaml and RUNTIME-VERSION.yaml to your repository (commit \"Initialize MARSHAL project\")."
	if !strings.Contains(out, want) {
		t.Fatalf("init did not say what it committed: %q", out)
	}
	if got := testGit(t, dir, "show", "--name-only", "--format=", "HEAD"); got != "CAPABILITIES.yaml\nPACK-VERSION.yaml\nRUNTIME-VERSION.yaml" {
		t.Fatalf("committed files differ from the notice: %q", got)
	}
	again, stderr, code := runCLI(t, dir, "init")
	if code != 0 {
		t.Fatalf("repeated init failed (%d): %s", code, stderr)
	}
	if strings.Contains(again, "to your repository") {
		t.Fatalf("repeated init claims a commit: %q", again)
	}
	assertNoInternalLeak(t, out+again)
}

// Accepting the setup offer runs the same init, so it says the same thing.
func TestSetupInitSaysWhichFilesItCommitted(t *testing.T) {
	isolateGitIdentity(t, "[user]\n\tname = Real Person\n\temail = real@example.test\n")
	dir := baselineRepository(t)
	assessment := startup.Assessment{Checks: []startup.Check{{ID: "project.initialized", Required: true, Status: startup.StatusMissing, Reason: startup.ReasonProjectNotInit}}}
	fixes := setupFixes(assessment)
	if len(fixes) != 1 || fixes[0].reason != startup.ReasonProjectNotInit {
		t.Fatalf("setup offers: %+v", fixes)
	}
	var out bytes.Buffer
	if err := fixes[0].apply(t.Context(), dir, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Committed CAPABILITIES.yaml, PACK-VERSION.yaml and RUNTIME-VERSION.yaml to your repository") {
		t.Fatalf("setup did not say what it committed: %q", out.String())
	}
}
