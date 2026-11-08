package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestOneShotProviderArgsDetection(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		args     []string
		want     bool
	}{
		{"codex exec", "codex", []string{"exec", "prompt"}, true},
		{"codex resume", "codex", []string{"resume", "--last"}, false},
		{"codex empty", "codex", nil, false},
		{"opencode run", "opencode", []string{"run", "prompt"}, true},
		{"opencode session", "opencode", []string{"--session", "id"}, false},
		{"opencode empty", "opencode", nil, false},
		{"claude -p", "claude", []string{"-p", "prompt"}, true},
		{"claude --print", "claude", []string{"--print"}, true},
		{"claude interactive", "claude", []string{"--resume", "name"}, false},
		{"claude empty", "claude", nil, false},
		{"agy -p", "antigravity", []string{"-p", "prompt"}, true},
		{"agy --print", "antigravity", []string{"--print"}, true},
		{"agy --prompt", "antigravity", []string{"--prompt", "prompt"}, true},
		{"agy interactive", "antigravity", []string{"resume"}, false},
		{"agy empty", "antigravity", nil, false},
		{"flag after separator is positional", "claude", []string{"--", "-p"}, false},
		{"unknown provider", "unknown", []string{"exec"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := oneShotProviderArgs(tc.provider, tc.args); got != tc.want {
				t.Fatalf("oneShotProviderArgs(%q, %v) = %t, want %t", tc.provider, tc.args, got, tc.want)
			}
		})
	}
}

func TestNativeTuiFlag(t *testing.T) {
	for provider, want := range map[string]string{
		"codex":       "--codex",
		"claude":      "--claude",
		"opencode":    "--opencode",
		"antigravity": "--antigravity",
	} {
		if got := nativeTuiFlag(provider); got != want {
			t.Fatalf("nativeTuiFlag(%q) = %q, want %q", provider, got, want)
		}
	}
}

// installFakeProvider puts an argv-recording provider double first on PATH. The
// double logs its arguments and stdin, writes a marker to stdout and stderr,
// and exits with the code in FAKE_EXIT (default 0).
func installFakeProvider(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$FAKE_LOG"
cat >> "$FAKE_STDIN_LOG"
printf 'fake-stdout\n'
printf 'fake-stderr\n' >&2
exit "${FAKE_EXIT:-0}"
`
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestNativeOneShotRunsProviderDirectly(t *testing.T) {
	installFakeProvider(t, "codex")
	log := filepath.Join(t.TempDir(), "argv")
	stdinLog := filepath.Join(t.TempDir(), "stdin")
	t.Setenv("FAKE_LOG", log)
	t.Setenv("FAKE_STDIN_LOG", stdinLog)

	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), t.TempDir(), []string{"codex", "exec", "fix the bug"}, strings.NewReader("piped input\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("one-shot codex exit = %d, want 0: %s", code, stderr.String())
	}
	if got, want := stdout.String(), "fake-stdout\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "fake-stderr\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	argv, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(argv)), "exec\nfix the bug"; got != want {
		t.Fatalf("provider argv = %q, want %q", got, want)
	}
	stdin, err := os.ReadFile(stdinLog)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(stdin), "piped input\n"; got != want {
		t.Fatalf("provider stdin = %q, want %q", got, want)
	}
}

func TestNativeOneShotExitsWithProviderCode(t *testing.T) {
	installFakeProvider(t, "opencode")
	t.Setenv("FAKE_LOG", filepath.Join(t.TempDir(), "argv"))
	t.Setenv("FAKE_STDIN_LOG", filepath.Join(t.TempDir(), "stdin"))
	t.Setenv("FAKE_EXIT", "7")

	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), t.TempDir(), []string{"opencode", "run", "prompt"}, strings.NewReader(""), &stdout, &stderr)
	if code != 7 {
		t.Fatalf("one-shot opencode exit = %d, want provider exit 7", code)
	}
}

func TestNativeOneShotAgentsUseAgyBinary(t *testing.T) {
	installFakeProvider(t, "agy")
	log := filepath.Join(t.TempDir(), "argv")
	t.Setenv("FAKE_LOG", log)
	t.Setenv("FAKE_STDIN_LOG", filepath.Join(t.TempDir(), "stdin"))

	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), t.TempDir(), []string{"agy", "-p", "hello"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("agy one-shot exit = %d, want 0: %s", code, stderr.String())
	}
	argv, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(argv)), "-p\nhello"; got != want {
		t.Fatalf("agy argv = %q, want %q", got, want)
	}
}

func TestNativeOneShotMissingBinaryExplainsInstall(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // No provider binary on PATH.
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), t.TempDir(), []string{"codex", "exec", "prompt"}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatal("missing provider binary succeeded")
	}
	msg := stderr.String()
	if !strings.Contains(msg, "Install Codex") || !strings.Contains(msg, "codex available on PATH") {
		t.Fatalf("stderr = %q, want install hint", msg)
	}
}

func TestNativeOneShotDoesNotReorderProviderArgs(t *testing.T) {
	installFakeProvider(t, "claude")
	log := filepath.Join(t.TempDir(), "argv")
	t.Setenv("FAKE_LOG", log)
	t.Setenv("FAKE_STDIN_LOG", filepath.Join(t.TempDir(), "stdin"))

	want := []string{"-p", "--model", "chosen-model", "do the thing"}
	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), t.TempDir(), append([]string{"claude"}, want...), strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("claude one-shot exit = %d: %s", code, stderr.String())
	}
	argv, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(argv)), "\n")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provider argv = %q, want %q", got, want)
	}
}

type fakeOneShotRuntime struct {
	transcripts []importer.SessionTranscript
	allow       bool
}

func (r *fakeOneShotRuntime) HasReadGrant(string) bool { return r.allow }
func (r *fakeOneShotRuntime) ProposeContinuation(_ context.Context, tr importer.SessionTranscript) ([]model.MemoryRecordV2, bool, error) {
	r.transcripts = append(r.transcripts, tr)
	return []model.MemoryRecordV2{{}}, false, nil
}
func (r *fakeOneShotRuntime) Close() error { return nil }

func fakeProvider(t *testing.T, body string) (string, string) {
	t.Helper()
	binDir, home := t.TempDir(), t.TempDir()
	path := filepath.Join(binDir, "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	t.Setenv("CODEX_HOME", home)
	return path, home
}

func TestNativeOneShotCapturesOnlyItsSessionAndPreservesExitCode(t *testing.T) {
	root := gitDirectory(t)
	if _, stderr, code := runCLI(t, root, "init"); code != 0 {
		t.Fatalf("initialize: %d %s", code, stderr)
	}
	_, historyHome := fakeProvider(t, `mkdir -p "$CODEX_HOME/sessions"
cat > "$CODEX_HOME/sessions/own.jsonl" <<'JSON'
{"type":"session_meta","payload":{"id":"oneshot-own","cwd":"`+root+`"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"this run only"}]}}
JSON
echo provider-out
echo provider-err >&2
exit 7`)
	sessions := filepath.Join(historyHome, "sessions")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, "old.jsonl"), []byte(`{"type":"session_meta","payload":{"id":"old","cwd":"`+root+`"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeOneShotRuntime{allow: true}
	previous := openNativeOneShotRuntime
	openNativeOneShotRuntime = func(context.Context, string) (nativeOneShotRuntime, error) { return fake, nil }
	t.Cleanup(func() { openNativeOneShotRuntime = previous })
	var stdout, stderr bytes.Buffer
	c := command{root: root, stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}
	code := c.runNativeOneShot(context.Background(), "codex", []string{"exec", "do it"})
	if code != 7 {
		t.Fatalf("provider code = %d, want 7", code)
	}
	if stdout.String() != "provider-out\n" || !strings.Contains(stderr.String(), "provider-err\n") {
		t.Fatalf("provider streams changed: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if len(fake.transcripts) != 1 || fake.transcripts[0].SessionID != "oneshot-own" || !reflect.DeepEqual(fake.transcripts[0].Messages[0].Content, "this run only") {
		t.Fatalf("proposed transcripts = %+v; stdout=%q stderr=%q", fake.transcripts, stdout.String(), stderr.String())
	}
}

func TestNativeOneShotRunsWithoutInitializedRuntime(t *testing.T) {
	root := t.TempDir()
	fakeProvider(t, `echo still-runs`)
	opened := false
	previous := openNativeOneShotRuntime
	openNativeOneShotRuntime = func(context.Context, string) (nativeOneShotRuntime, error) {
		opened = true
		return nil, errors.New("not initialized")
	}
	t.Cleanup(func() { openNativeOneShotRuntime = previous })
	var stdout, stderr bytes.Buffer
	c := command{root: root, stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}
	if code := c.runNativeOneShot(context.Background(), "codex", []string{"exec", "do it"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if stdout.String() != "still-runs\n" || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if opened {
		t.Fatal("runtime was opened for an uninitialized project")
	}
}

func TestNativeOneShotSkipsHistoryWithoutReadGrant(t *testing.T) {
	root := gitDirectory(t)
	if _, stderr, code := runCLI(t, root, "init"); code != 0 {
		t.Fatalf("initialize: %d %s", code, stderr)
	}
	fakeProvider(t, `mkdir -p "$CODEX_HOME/sessions"
cat > "$CODEX_HOME/sessions/own.jsonl" <<'JSON'
{"type":"session_meta","payload":{"id":"no-grant","cwd":"`+root+`"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"private session"}]}}
JSON`)
	fake := &fakeOneShotRuntime{}
	previous := openNativeOneShotRuntime
	openNativeOneShotRuntime = func(context.Context, string) (nativeOneShotRuntime, error) { return fake, nil }
	t.Cleanup(func() { openNativeOneShotRuntime = previous })
	var stdout, stderr bytes.Buffer
	c := command{root: root, stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}
	if code := c.runNativeOneShot(context.Background(), "codex", []string{"exec", "do it"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if len(fake.transcripts) != 0 {
		t.Fatalf("history read without grant: %+v", fake.transcripts)
	}
	if !strings.Contains(stderr.String(), "skipped sources without a read grant") {
		t.Fatalf("missing skip note: %q", stderr.String())
	}
}

func TestNativeHelpAndVersionSkipCapture(t *testing.T) {
	for _, args := range [][]string{{"exec", "--help"}, {"run", "--version"}, {"-h"}} {
		if !nativeHelpOrVersionArgs(args) {
			t.Errorf("not skipped: %q", args)
		}
	}
}

// blockingOneShotRuntime models a proposal that ignores cancellation and only
// returns once its context is done, so the capture goroutine cannot finish
// before the one-shot scan deadline. It records whether the runtime is closed
// before the command returns and whether any proposal happens after it.
type blockingOneShotRuntime struct {
	proposed atomic.Int64
	closed   atomic.Bool
}

func (r *blockingOneShotRuntime) HasReadGrant(string) bool { return true }
func (r *blockingOneShotRuntime) ProposeContinuation(ctx context.Context, tr importer.SessionTranscript) ([]model.MemoryRecordV2, bool, error) {
	r.proposed.Add(1)
	<-ctx.Done()
	return nil, false, ctx.Err()
}
func (r *blockingOneShotRuntime) Close() error { r.closed.Store(true); return nil }

func TestNativeOneShotReturnsWithinBoundWithSlowHistory(t *testing.T) {
	root := gitDirectory(t)
	if _, stderr, code := runCLI(t, root, "init"); code != 0 {
		t.Fatalf("initialize: %d %s", code, stderr)
	}
	fakeProvider(t, `mkdir -p "$CODEX_HOME/sessions"
cat > "$CODEX_HOME/sessions/own.jsonl" <<'JSON'
{"type":"session_meta","payload":{"id":"slow","cwd":"`+root+`"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"slow scan"}]}}
JSON`)
	fake := &blockingOneShotRuntime{}
	previous := openNativeOneShotRuntime
	openNativeOneShotRuntime = func(context.Context, string) (nativeOneShotRuntime, error) { return fake, nil }
	t.Cleanup(func() { openNativeOneShotRuntime = previous })
	var stdout, stderr bytes.Buffer
	c := command{root: root, stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}
	start := time.Now()
	code := c.runNativeOneShot(context.Background(), "codex", []string{"exec", "do it"})
	elapsed := time.Since(start)
	if code != 0 {
		t.Fatalf("exit code = %d: %s", code, stderr.String())
	}
	if elapsed > 12*time.Second {
		t.Fatalf("one-shot returned in %v, want within the capture bound", elapsed)
	}
	// The runtime is closed before the command returns, and the capture
	// goroutine finished first, so nothing proposes after return.
	if !fake.closed.Load() {
		t.Fatal("runtime was not closed before one-shot returned")
	}
	if got := fake.proposed.Load(); got != 1 {
		t.Fatalf("proposed %d times, want exactly 1", got)
	}
	time.Sleep(50 * time.Millisecond)
	if got := fake.proposed.Load(); got != 1 {
		t.Fatalf("proposed after one-shot returned: %d", got)
	}
}
