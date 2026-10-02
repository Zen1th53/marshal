package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func goldenSeatbeltRequest(network bool) seatbeltProfileRequest {
	return seatbeltProfileRequest{
		Request:     model.SandboxRequest{Worktree: "/project/.marshal/worktrees/task", RuntimeDir: "/project/.marshal", WritableDirs: []string{"/build-output"}, NetworkAllowed: network},
		Scratch:     "/private/tmp/marshal-seatbelt-example",
		SystemPaths: []string{"/usr", "/bin", "/sbin", "/System/Library", "/Library/Apple/System/Library", "/private/etc"},
		ReadPaths:   []seatbeltReadPath{{Path: "/tools/provider", Directory: false}, {Path: "/project/.git", Directory: true}},
	}
}

func TestSeatbeltProfileGolden(t *testing.T) {
	for _, network := range []bool{false, true} {
		name := "denied"
		if network {
			name = "allowed"
		}
		t.Run(name, func(t *testing.T) {
			profile, parameters, err := seatbeltProfile(goldenSeatbeltRequest(network))
			if err != nil {
				t.Fatal(err)
			}
			got := profile + "\nParameters:\n" + strings.Join(parameters, "\n") + "\n"
			want, err := os.ReadFile(filepath.Join("testdata", "seatbelt-"+name+".golden"))
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Fatalf("profile differs from golden:\n%s", got)
			}
		})
	}
}

func TestSeatbeltPathsCanonicalized(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(actual, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actual, link); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(actual)
	if err != nil {
		t.Fatal(err)
	}
	request := model.SandboxRequest{Worktree: link, WritableDirs: []string{link}, ReadOnlyBinds: []model.Bind{{Source: link, Target: link}}}
	input, err := normalizeSeatbeltRequest(request, "/private/tmp/scratch")
	if err != nil {
		t.Fatal(err)
	}
	if input.Request.Worktree != want || !reflect.DeepEqual(input.Request.WritableDirs, []string{want}) || input.ReadPaths[0].Path != want {
		t.Fatalf("canonical paths: %#v", input)
	}
	profile, params, err := seatbeltProfile(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(profile, want) || strings.Contains(strings.Join(params, "\n"), link) {
		t.Fatalf("paths were interpolated or not resolved: %s %v", profile, params)
	}
}

func TestSeatbeltRejectsMalformedPaths(t *testing.T) {
	for _, bad := range []string{"", "relative", "/path\nrule", "/path\x00rule", "/path\rrule", "/path/../other"} {
		input := goldenSeatbeltRequest(false)
		input.Request.Worktree = bad
		if _, _, err := seatbeltProfile(input); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	root := t.TempDir()
	for _, bad := range []string{"/bad\npath", "/bad\x00path"} {
		for _, request := range []model.SandboxRequest{
			{Worktree: bad}, {Worktree: root, WritableDirs: []string{bad}},
			{Worktree: root, RuntimeDir: bad}, {Worktree: root, ReadOnlyBinds: []model.Bind{{Source: bad, Target: root}}},
			{Worktree: root, ReadOnlyBinds: []model.Bind{{Source: root, Target: bad}}},
			{Worktree: root, WritableTmpfs: []string{bad}},
		} {
			if _, err := NewSeatbelt("/usr/bin/sandbox-exec").envelope(request, []string{"/bin/true"}); !errors.Is(err, model.ErrInvalid) {
				t.Fatalf("request: %#v error: %v", request, err)
			}
		}
	}
}

func TestSeatbeltProtectedPathsNeverAllowed(t *testing.T) {
	for _, path := range []string{"/home/test/.codex", "/home/test/.claude", "/home/test/.opencode", "/home/test/.gemini", "/home/test/.config/codex", "/home/test/.codex/logs", "/home/test/.ssh", "/home/test/.local/share/opencode", "/home/test/.cache/gemini"} {
		input := goldenSeatbeltRequest(false)
		input.ReadPaths = append(input.ReadPaths, seatbeltReadPath{Path: path, Directory: true})
		if _, _, err := seatbeltProfile(input); err == nil {
			t.Fatalf("provider/credential path allowed: %s", path)
		}
	}
	input := goldenSeatbeltRequest(false)
	profile, _, err := seatbeltProfile(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(profile, "\n") {
		if strings.HasPrefix(line, "(allow") && (strings.Contains(line, "RUNTIME") || strings.Contains(line, ".codex") || strings.Contains(line, ".claude")) {
			t.Fatalf("protected allow: %s", line)
		}
	}
	if !strings.Contains(profile, `(require-not (subpath (param "WORKTREE")))`) {
		t.Fatal("runtime denial lacks assigned-worktree exception")
	}
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "state")
	worktree := filepath.Join(root, "tree")
	for _, path := range []string{runtimeDir, worktree} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, request := range []model.SandboxRequest{
		{Worktree: runtimeDir, RuntimeDir: runtimeDir},
		{Worktree: worktree, RuntimeDir: runtimeDir, WritableDirs: []string{runtimeDir}},
		{Worktree: worktree, RuntimeDir: runtimeDir, ReadOnlyBinds: []model.Bind{{Source: runtimeDir, Target: runtimeDir}}},
	} {
		if _, err := normalizeSeatbeltRequest(request, "/private/tmp/scratch"); err == nil {
			t.Fatalf("runtime scope accepted: %#v", request)
		}
	}
}

func TestSeatbeltEnvelopeEnvironmentAndCleanup(t *testing.T) {
	t.Setenv("MARSHAL_HOST_SECRET", "must-not-inherit")
	request := model.SandboxRequest{Worktree: t.TempDir(), WritableTmpfs: []string{"/home/marshal/.codex", "/home/marshal/.local/share/codex", "/tmp"}, ExtraEnv: []string{"XDG_DATA_HOME=/home/marshal/.local/share", "EXPLICIT=value", "HOME=/host/home", "TMPDIR=/host/tmp"}}
	spec, err := NewSeatbelt("/usr/bin/sandbox-exec").envelope(request, []string{"/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	defer spec.Cleanup()
	values := map[string]string{}
	for _, kv := range spec.Env {
		key, value, _ := strings.Cut(kv, "=")
		values[key] = value
	}
	if values["PATH"] != "/usr/bin:/bin" || values["EXPLICIT"] != "value" || values["MARSHAL_HOST_SECRET"] != "" {
		t.Fatalf("environment: %v", spec.Env)
	}
	scratch := filepath.Dir(values["HOME"])
	if values["TMPDIR"] != filepath.Join(scratch, "tmp") || values["XDG_DATA_HOME"] != filepath.Join(values["HOME"], ".local/share") {
		t.Fatalf("mapped environment: %v", values)
	}
	if _, err := os.Stat(filepath.Join(values["HOME"], ".codex")); err != nil {
		t.Fatal(err)
	}
	if err := spec.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("scratch retained: %v", err)
	}
}

func TestSeatbeltRejectsHomeAndRemappedBinds(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	for _, request := range []model.SandboxRequest{
		{Worktree: home},
		{Worktree: worktree, WritableDirs: []string{home}},
		{Worktree: worktree, ReadOnlyBinds: []model.Bind{{Source: home, Target: home}}},
		{Worktree: worktree, ReadOnlyBinds: []model.Bind{{Source: worktree, Target: home}}},
	} {
		if _, err := normalizeSeatbeltRequest(request, "/private/tmp/scratch"); err == nil {
			t.Fatalf("accepted protected/remapped scope: %#v", request)
		}
	}
}

func TestSeatbeltRuntimeInsideWorktreeIsDenied(t *testing.T) {
	input := goldenSeatbeltRequest(false)
	input.Request.Worktree = "/project"
	input.Request.RuntimeDir = "/project/.marshal"
	profile, _, err := seatbeltProfile(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(profile, `(deny file-read* file-write* process-exec (subpath (param "RUNTIME")))`) {
		t.Fatalf("nested runtime must be fully denied: %s", profile)
	}
	input.ReadPaths = []seatbeltReadPath{{Path: input.Request.RuntimeDir, Directory: true}}
	if _, _, err := seatbeltProfile(input); err == nil {
		t.Fatal("runtime directory explicitly allowed")
	}
}
