package cli

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
)

func TestNativeCodexArgumentsPassThrough(t *testing.T) {
	want := []string{"resume", "--last", "-i", "picture with spaces.png", "--model", "chosen-model"}
	options := parseWorkspaceArgs(append([]string{"--codex"}, want...))
	if !reflect.DeepEqual(options.nativeCodex, want) {
		t.Fatalf("native args changed: %q", options.nativeCodex)
	}
	if parseWorkspaceArgs([]string{"--codex"}).nativeCodex == nil {
		t.Fatal("empty native launch lost")
	}
}

func TestNativeClaudeArgumentsPassThrough(t *testing.T) {
	want := []string{"--resume", "session name", "--model", "chosen-model"}
	options := parseWorkspaceArgs(append([]string{"--claude"}, want...))
	if !reflect.DeepEqual(options.nativeClaude, want) {
		t.Fatalf("native args changed: %q", options.nativeClaude)
	}
	if parseWorkspaceArgs([]string{"--claude"}).nativeClaude == nil {
		t.Fatal("empty native launch lost")
	}
}

func TestNativeOpenCodeArgumentsPassThrough(t *testing.T) {
	want := []string{"--session", "session name", "--model", "openai/gpt-5"}
	options := parseWorkspaceArgs(append([]string{"--opencode"}, want...))
	if !reflect.DeepEqual(options.nativeOpenCode, want) {
		t.Fatalf("native args changed: %q", options.nativeOpenCode)
	}
	if parseWorkspaceArgs([]string{"--opencode"}).nativeOpenCode == nil {
		t.Fatal("empty native launch lost")
	}
}

func TestTUICLIInvocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer

	// 1. Initialize project so runtime exists
	code := Execute(ctx, repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("init failed: %s", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	// 2. Invoke `tui` subcommand with /quit
	in := strings.NewReader("/quit\n")
	code = Execute(ctx, repo.Path(), []string{"tui"}, in, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("tui subcommand returned non-zero code %d: %s", code, stderr.String())
	}
	outStr := stdout.String()
	// The workspace identifies itself in a compact header; project, mode and
	// runtime state live in the statusline rather than being repeated here.
	if !strings.Contains(outStr, "MARSHAL") {
		t.Fatalf("expected the MARSHAL header in tui output:\n%s", outStr)
	}
	expectedStatus := "MANUAL"
	if cloud.ExecutionOnAtStartup {
		expectedStatus = "Marshal — approval authority"
		if !strings.Contains(outStr, "[ULTRA ACTIVE]") {
			t.Fatalf("expected active Marshal execution in tui output:\n%s", outStr)
		}
	}
	if !strings.Contains(outStr, expectedStatus) {
		t.Fatalf("expected %q in the statusline:\n%s", expectedStatus, outStr)
	}
	if !strings.Contains(outStr, "Exiting MARSHAL terminal workspace") {
		t.Fatalf("expected exit in tui output:\n%s", outStr)
	}

	// 3. Invoke empty args with initialized project - opens TUI directly!
	stdout.Reset()
	stderr.Reset()
	in = strings.NewReader("/quit\n")
	code = Execute(ctx, repo.Path(), []string{}, in, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("empty-arg invocation returned non-zero code %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "MARSHAL") {
		t.Fatalf("expected empty args to open TUI in initialized project:\n%s", stdout.String())
	}
}

// Once app.Open has admitted a project, workspace control must use that
// runtime's root. A later Git lookup can fail under load and must not silently
// drop the mutation authority from an otherwise running terminal.
func TestRunWorkspaceBindsOpenedRuntimeWithoutRediscoveringRoot(t *testing.T) {
	t.Setenv("MARSHAL_NO_UPDATE_CHECK", "1")
	repo := cliRepo(t)
	if _, err := app.Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	folder := t.TempDir()
	var stdout bytes.Buffer
	cmd := &command{
		root:   t.TempDir(), // Git discovery here cannot find the admitted repo.
		stdin:  strings.NewReader("/permission read allow " + folder + "\n/quit\n"),
		stdout: &stdout,
		stderr: &bytes.Buffer{},
	}
	if err := cmd.runWorkspace(t.Context(), runtime, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Read decision recorded: "+folder) {
		t.Fatalf("the admitted runtime was not attached to workspace control:\n%s", stdout.String())
	}
}

func TestRunWorkspaceRefusesBindingChangedAfterRuntimeAdmission(t *testing.T) {
	t.Setenv("MARSHAL_NO_UPDATE_CHECK", "1")
	repo := cliRepo(t)
	if _, err := app.Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	binding, found := projectid.LoadBinding(statepath.Path(repo.Path()))
	if !found {
		t.Fatal("opened project has no identity binding")
	}
	binding.Evidence.CreatedNonce += "-replacement"
	binding.ID, err = projectid.Derive(binding.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := projectid.SaveBinding(statepath.Path(repo.Path()), binding); err != nil {
		t.Fatal(err)
	}

	folder := t.TempDir()
	var stdout bytes.Buffer
	cmd := &command{
		root:   repo.Path(),
		stdin:  strings.NewReader("/permission read allow " + folder + "\n/quit\n"),
		stdout: &stdout,
		stderr: &bytes.Buffer{},
	}
	if err := cmd.runWorkspace(t.Context(), runtime, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "no runtime is attached to this workspace") {
		t.Fatalf("workspace accepted a binding changed after admission:\n%s", stdout.String())
	}
}
