package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Zen1th53/marshal/internal/api"
)

// Keep this independent of the help catalog: include aliases and all dispatcher branches.
var sweepTree = map[string]string{
	"init": "", "doctor": "", "status": "", "agent": "register", "agents": "", "tasks": "",
	"task": "import show claim release", "run": "", "logs": "", "cancel": "", "adapters": "", "adapter": "probe",
	"mcp": "serve status", "a2a": "serve status", "events": "", "artifacts": "", "verify": "", "reconcile": "",
	"memory": "status recall show list promote tombstone audit remember write", "policy": "test", "legal": "audit export",
	"setup": "status", "update": "check status install apply", "goal": "explain", "plan": "create show approve cancel handoff",
	"exec": "start run status approve rollback handoff", "execution": "start run status approve rollback handoff",
	"review": "start status evaluate attest", "verification": "start status evaluate attest",
	"learning":     "commit show item history search context invalidate trust fingerprints playbooks replays benchmarks export restore",
	"optimization": "start show candidates counterfactuals manifests canaries", "help": "why",
	"constitution": "version invariants decisions violations", "auth": "token", "auth token": "create list revoke",
	"gc": "worktrees artifacts", "state": "backup verify-backup restore", "daemon": "", "version": "", "tui": "",
}

// Native provider commands pass --help to the provider's own CLI; the
// dispatcher must not answer it for them.
func TestDispatcherHelpLeavesNativeProviderHelpToProvider(t *testing.T) {
	for _, name := range []string{"codex", "claude", "opencode", "agy", "antigravity"} {
		for _, flag := range []string{"--help", "-h"} {
			var out bytes.Buffer
			if dispatcherHelp([]string{name, flag}, &out) || out.Len() != 0 {
				t.Fatalf("%s %s was answered by the dispatcher: %q", name, flag, out.String())
			}
		}
	}
}

type unreadableInput struct{}

func (unreadableInput) Read([]byte) (int, error) { panic("command tried to read stdin") }

type denyHelpNetwork struct{ calls atomic.Int64 }

func (d *denyHelpNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	d.calls.Add(1)
	return nil, fmt.Errorf("network forbidden during help")
}

func snapshotSweep(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := fmt.Sprintf("%s %s", info.Mode(), info.ModTime())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += fmt.Sprintf(" %x", sha256.Sum256(data))
		}
		result[strings.TrimPrefix(path, root)] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDispatcherHelpWholeTreeHasNoEffects(t *testing.T) {
	repo := cliRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	// Unix API calls are counted too, even though they use a private HTTP transport.
	runtimeDir := filepath.Join(repo.Path(), ".marshal")
	if err := os.Mkdir(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(runtimeDir, "runtime.sock"))
	if err != nil {
		t.Fatal(err)
	}
	var localCalls atomic.Int64
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localCalls.Add(1)
		http.Error(w, "help must not call runtime", 500)
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	t.Setenv("PATH", t.TempDir()) // Help must not invoke Git or any provider binary.
	deny := &denyHelpNetwork{}
	original := http.DefaultTransport
	http.DefaultTransport = deny
	t.Cleanup(func() { http.DefaultTransport = original })
	before := snapshotSweep(t, repo.Path())
	homeBefore := snapshotSweep(t, home)
	for parent, children := range sweepTree {
		paths := []string{parent}
		for _, child := range strings.Fields(children) {
			paths = append(paths, parent+" "+child)
		}
		for _, path := range paths {
			for _, form := range []string{"--help", "-h", "help"} {
				t.Run(path+"/"+form, func(t *testing.T) {
					args := strings.Fields(path)
					if form == "help" {
						args = append([]string{"help"}, args...)
					} else {
						args = append(args, form)
					}
					var out, stderr bytes.Buffer
					code := Execute(t.Context(), repo.Path(), args, unreadableInput{}, &out, &stderr)
					if code != 0 || !strings.Contains(out.String(), "Usage: marshal "+path) || stderr.Len() != 0 {
						t.Errorf("code=%d stdout=%q stderr=%q", code, out.String(), stderr.String())
					}
					if !reflect.DeepEqual(before, snapshotSweep(t, repo.Path())) {
						t.Fatal("help changed repository files")
					}
					if !reflect.DeepEqual(homeBefore, snapshotSweep(t, home)) {
						t.Fatal("help changed home files")
					}
					if deny.calls.Load() != 0 || localCalls.Load() != 0 {
						t.Fatalf("help attempted network: remote=%d local=%d", deny.calls.Load(), localCalls.Load())
					}
				})
			}
		}
	}
}

func TestInitNonInteractiveNeverReadsInput(t *testing.T) {
	root := t.TempDir()
	var out, stderr bytes.Buffer
	code := Execute(t.Context(), root, []string{"init"}, unreadableInput{}, &out, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "not part of a Git repository") || !strings.Contains(stderr.String(), "git init") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestVerifyMissingRunnerExplainsConfiguration(t *testing.T) {
	for _, args := range [][]string{{"verify"}, {"--json", "verify"}, {"verify", "--json"}, {"verify", "--"}} {
		var out, stderr bytes.Buffer
		code := Execute(t.Context(), t.TempDir(), args, unreadableInput{}, &out, &stderr)
		if code == 0 {
			t.Fatal("missing runner succeeded")
		}
		message := stderr.String()
		if strings.Contains(strings.Join(args, " "), "--json") {
			var value map[string]any
			if err := json.Unmarshal(out.Bytes(), &value); err != nil {
				t.Fatalf("invalid error JSON: %q (%v)", out.String(), err)
			}
			message = fmt.Sprint(value["error"])
		}
		for _, want := range []string{"conformance/runner.py", "marshal verify --"} {
			if !strings.Contains(message, want) {
				t.Errorf("missing %q in %q", want, message)
			}
		}
	}
}

func TestTrailingJSONMatchesLeading(t *testing.T) {
	repo := cliRepo(t)
	var out, stderr bytes.Buffer
	if code := Execute(t.Context(), repo.Path(), []string{"init"}, unreadableInput{}, &out, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	for _, cmd := range [][]string{{"version"}, {"memory", "status"}, {"memory", "list"}} {
		var leading, trailing bytes.Buffer
		if code := Execute(t.Context(), repo.Path(), append([]string{"--json"}, cmd...), unreadableInput{}, &leading, &stderr); code != 0 {
			t.Fatal(stderr.String())
		}
		if code := Execute(t.Context(), repo.Path(), append(append([]string{}, cmd...), "--json"), unreadableInput{}, &trailing, &stderr); code != 0 {
			t.Fatal(stderr.String())
		}
		var a, b any
		if json.Unmarshal(leading.Bytes(), &a) != nil || json.Unmarshal(trailing.Bytes(), &b) != nil || !reflect.DeepEqual(a, b) {
			t.Fatalf("leading=%q trailing=%q", leading.String(), trailing.String())
		}
	}
}

func TestJSONTaskReleaseWithoutLease(t *testing.T) {
	repo := cliRepo(t)
	dir := filepath.Join(repo.Path(), ".marshal")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(dir, "runtime.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/tasks/TASK-001/release" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(api.Envelope{Error: &api.Error{Code: "not_found", Message: "active lease for task TASK-001"}})
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	var out, stderr bytes.Buffer
	code := Execute(context.Background(), repo.Path(), []string{"--json", "task", "release", "TASK-001"}, unreadableInput{}, &out, &stderr)
	var value map[string]any
	if code != 1 || json.Unmarshal(out.Bytes(), &value) != nil || !strings.Contains(fmt.Sprint(value["error"]), "active lease for task TASK-001") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestDispatcherArgumentBoundaries(t *testing.T) {
	for _, tc := range []struct {
		args, want []string
		json       bool
	}{
		{[]string{"--json", "verify", "--", "echo", "--help", "--json"}, []string{"verify", "--", "echo", "--help", "--json"}, true},
		{[]string{"memory", "status", "--json"}, []string{"memory", "status"}, true},
		{[]string{"--json", "codex", "--json"}, []string{"codex", "--json"}, true},
		{[]string{"claude", "--json"}, []string{"claude", "--json"}, false},
	} {
		original := append([]string{}, tc.args...)
		got, enabled := globalJSON(tc.args)
		if enabled != tc.json || !reflect.DeepEqual(got, tc.want) || !reflect.DeepEqual(original, tc.args) {
			t.Fatalf("args=%v got=%v json=%t", tc.args, got, enabled)
		}
	}
	var out bytes.Buffer
	if dispatcherHelp([]string{"verify", "--", "echo", "--help"}, &out) {
		t.Fatal("verification child help was intercepted")
	}
	if !dispatcherHelp([]string{"verify", "--help", "--", "echo"}, &out) {
		t.Fatal("marshal help before separator was missed")
	}
}

func TestHelpCatalogCoversSweepTree(t *testing.T) {
	// Native provider commands keep a catalog entry for `marshal help`, but
	// their -h/--help belongs to the provider and is not walked here.
	expected := map[string]bool{"codex": true, "claude": true, "opencode": true, "agy": true, "antigravity": true}
	for parent, children := range sweepTree {
		expected[parent] = true
		for _, child := range strings.Fields(children) {
			expected[parent+" "+child] = true
		}
	}
	for path := range expected {
		if _, ok := commandHelp[path]; !ok {
			t.Errorf("missing help for %s", path)
		}
	}
	for path := range commandHelp {
		if !expected[path] {
			t.Errorf("help path %s is not walked by the sweep", path)
		}
	}
}
