package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/architecturereview"
	"github.com/Zen1th53/marshal/internal/model"
)

var browserArchitectureFixture = json.RawMessage(`{"title":"Atlas","purpose":"Keep architecture decisions","summary":"Runtime and UI","components":[{"id":"runtime","name":"Runtime","responsibility":"Own state","technology":"Go","files":[],"evidence":[]},{"id":"browser","name":"Browser","responsibility":"Review architecture","technology":"HTML","files":[],"evidence":[]}],"connections":[{"id":"review","source":"browser","target":"runtime","label":"Explicit feedback","protocol":"HTTP"}],"constraints":[],"assumptions":[],"workers":[]}`)

func browserContextRuntime(t *testing.T) *Runtime {
	t.Helper()
	root := runtimeRepo(t).Path()
	if _, err := Bootstrap(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func browserContextState(t *testing.T, runtime *Runtime, approved bool) string {
	t.Helper()
	view, err := architecturereview.GenericLayouts(browserArchitectureFixture)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(runtime.layout.RuntimeDir, "architecture", "review")
	review, err := architecturereview.New(architecturereview.Config{Context: t.Context(), Project: runtime.ProjectIdentity(), ArchitectureID: "architecture-context", StateDir: directory, Proposal: browserArchitectureFixture, Layouts: view})
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		state, err := review.State()
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err := json.Unmarshal(state, &value); err != nil {
			t.Fatal(err)
		}
		request, _ := json.Marshal(map[string]any{"protocol_version": architecturereview.Version, "action": "approve", "revision": value["revision"], "digest": value["digest"], "ui_digest": value["ui_digest"], "language": value["language"], "builders_digest": value["builders_digest"]})
		if _, err := review.Apply(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(directory, "state.json")
}

func TestBrowserArchitectureContextKeepsDraftAndAcceptanceDistinct(t *testing.T) {
	for _, approved := range []bool{false, true} {
		t.Run(map[bool]string{false: "draft", true: "accepted"}[approved], func(t *testing.T) {
			runtime := browserContextRuntime(t)
			browserContextState(t, runtime, approved)
			raw, err := runtime.acceptedArchitectureContext(t.Context(), runtime.layout.Root)
			if err != nil {
				t.Fatal(err)
			}
			var packet map[string]any
			if err := json.Unmarshal(raw, &packet); err != nil {
				t.Fatal(err)
			}
			if packet["project"] != runtime.ProjectIdentity() || packet["revision"] != float64(1) || packet["status"] != map[bool]string{false: "draft", true: "accepted"}[approved] {
				t.Fatalf("browser context lost exact approval binding: %s", raw)
			}
			if !strings.Contains(string(raw), "Own state") || strings.Contains(string(raw), "token") {
				t.Fatalf("architecture content missing or credential exposed: %s", raw)
			}
		})
	}
}

func TestBrowserArchitectureContextReadsOnlyOwnerStateForOwnedWorker(t *testing.T) {
	runtime := browserContextRuntime(t)
	browserContextState(t, runtime, true)
	worker := filepath.Join(runtime.layout.Worktrees, "owned-worker")
	foreignState := filepath.Join(worker, statepath.DirName, "architecture", "review")
	if err := os.MkdirAll(foreignState, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreignState, "state.json"), []byte(`{"project":"foreign","approved":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	owner, err := runtime.acceptedArchitectureContext(t.Context(), runtime.layout.Root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runtime.acceptedArchitectureContext(t.Context(), worker)
	if err != nil || string(got) != string(owner) {
		t.Fatalf("worker substituted its own review: %s %v", got, err)
	}
	if _, err := runtime.acceptedArchitectureContext(t.Context(), t.TempDir()); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("foreign checkout permitted: %v", err)
	}
}

func TestBrowserArchitectureContextMissingAndCancelled(t *testing.T) {
	runtime := browserContextRuntime(t)
	if raw, err := runtime.acceptedArchitectureContext(t.Context(), runtime.layout.Root); err != nil || raw != nil {
		t.Fatalf("missing state created a claim: %s %v", raw, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := runtime.acceptedArchitectureContext(ctx, runtime.layout.Root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read proceeded: %v", err)
	}
}

func TestBrowserArchitectureContextRejectsInvalidRuntimeBindings(t *testing.T) {
	runtime := browserContextRuntime(t)
	for _, mutate := range []func(*Runtime){
		func(r *Runtime) { r.layout.Root = "relative-owner" },
		func(r *Runtime) { r.layout.RuntimeDir = filepath.Join(r.layout.Root, "foreign-state") },
		func(r *Runtime) { r.layout.Worktrees = t.TempDir() },
	} {
		fake := &Runtime{store: runtime.store, layout: runtime.layout}
		mutate(fake)
		if _, err := fake.acceptedArchitectureContext(t.Context(), filepath.Join(runtime.layout.Worktrees, "worker")); !errors.Is(err, model.ErrPolicyDenied) {
			t.Fatalf("invalid runtime binding permitted: %v", err)
		}
	}
	for _, unavailable := range []*Runtime{nil, {}} {
		if _, err := unavailable.acceptedArchitectureContext(t.Context(), runtime.layout.Root); !errors.Is(err, model.ErrUnavailable) {
			t.Fatalf("unavailable runtime read state: %v", err)
		}
	}
	if _, err := runtime.acceptedArchitectureContext(t.Context(), runtime.layout.Worktrees); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("worktree parent accepted as a worker checkout: %v", err)
	}
	alias := filepath.Join(runtime.layout.Worktrees, "alias")
	if err := os.Symlink(runtime.layout.Root, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.acceptedArchitectureContext(t.Context(), alias); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("symlink worker binding permitted: %v", err)
	}
}

func TestBrowserArchitectureContextFailsClosedForCorruptOrPublicState(t *testing.T) {
	for _, corruption := range []string{"project", "digest", "approval", "public-file", "public-parent", "symlink-file", "symlink-parent", "oversized", "invalid-json"} {
		t.Run(corruption, func(t *testing.T) {
			runtime := browserContextRuntime(t)
			path := browserContextState(t, runtime, true)
			saved, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(saved, &value); err != nil {
				t.Fatal(err)
			}
			switch corruption {
			case "project":
				value["project"] = "PROJECT-foreign"
			case "digest":
				value["digest"] = strings.Repeat("0", 64)
			case "approval":
				value["approval"].(map[string]any)["revision"] = 77
			case "public-file":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "public-parent":
				if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink-file":
				outside := filepath.Join(t.TempDir(), "state.json")
				if err := os.WriteFile(outside, saved, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "symlink-parent":
				previous := filepath.Dir(path) + "-saved"
				if err := os.Rename(filepath.Dir(path), previous); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(previous, filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(path, []byte(strings.Repeat(" ", architectureContextLimit+1)), 0600); err != nil {
					t.Fatal(err)
				}
			case "invalid-json":
				if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if corruption == "project" || corruption == "digest" || corruption == "approval" {
				bad, _ := json.Marshal(value)
				if err := os.WriteFile(path, bad, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if raw, err := runtime.acceptedArchitectureContext(t.Context(), runtime.layout.Root); err == nil || raw != nil {
				t.Fatalf("corrupt architecture credited: %s %v", raw, err)
			}
		})
	}
}
